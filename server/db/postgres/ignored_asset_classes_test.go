package postgres

import (
	"context"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// seedIgnoreFixture writes one FIXED_INCOME posting and one STOCK posting for the
// user under broker IBKR account A, returning the two instrument ids.
func seedIgnoreFixture(t *testing.T, p *Postgres, userID string) (bondID, stockID string) {
	t.Helper()
	ctx := context.Background()
	bondID = setupInstrumentWithCurrency(t, p, "BOND-1", db.AssetClassFixedIncome, "USD")
	stockID = setupInstrumentWithCurrency(t, p, "STK-1", db.AssetClassStock, "USD")
	at := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	for desc, id := range map[string]string{"BOND-1": bondID, "STK-1": stockID} {
		tx := &apiv1.Tx{Timestamp: timestamppb.New(at), InstrumentDescription: desc,
			BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "1", Account: "A"}
		if err := createTx(ctx, p, userID, "IBKR", "A", "", tx, id, nil); err != nil {
			t.Fatalf("create tx %s: %v", desc, err)
		}
	}
	return bondID, stockID
}

// remainingDescs returns the instrument descriptions of the user's surviving
// postings, keyed for membership checks.
func remainingDescs(t *testing.T, p *Postgres, userID string) map[string]bool {
	t.Helper()
	rows, err := p.q.QueryxContext(context.Background(),
		`SELECT instrument_description FROM txs WHERE user_id = $1`, userID)
	if err != nil {
		t.Fatalf("list remaining: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[d] = true
	}
	return out
}

// A rule deletes every posting whose instrument carries the rule's asset class,
// synthetic INITIALIZE pads included, and touches nothing else -- in particular
// a posting whose instrument is unresolved has no asset class and survives.
func TestSetIgnoredAssetClasses_DeletesByInstrumentAssetClass(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	bondID, _ := seedIgnoreFixture(t, p, userID)

	at := time.Date(2024, 3, 2, 10, 0, 0, 0, time.UTC)
	insertRawPostingAs(t, p, userID, bondID, newTxGroup(t, p, userID), "IBKR", at, "INITIALIZE")
	if _, err := p.q.ExecContext(ctx, `
		INSERT INTO txs (user_id, broker, account, timestamp, instrument_description,
		                 broker_tx_type, resolved_tx_type, quantity, instrument_id, weight,
		                 weight_commodity, group_id)
		VALUES ($1::uuid, 'IBKR', 'A', $2, 'UNRESOLVED', ARRAY['TRADE_ASSET'], 'TRADE_ASSET', 1, NULL, 0, 'desc:UNRESOLVED', $3::uuid)
	`, userID, at, newTxGroup(t, p, userID)); err != nil {
		t.Fatalf("insert unresolved posting: %v", err)
	}

	rules := []db.IgnoredAssetClass{{Broker: "IBKR", AssetClass: db.AssetClassFixedIncome}}
	if err := p.SetIgnoredAssetClasses(ctx, userID, rules); err != nil {
		t.Fatalf("set ignored asset classes: %v", err)
	}

	got := remainingDescs(t, p, userID)
	// insertRawPostingAs writes the synthetic pad with description USD.
	for desc, want := range map[string]bool{"BOND-1": false, "USD": false, "STK-1": true, "UNRESOLVED": true} {
		if got[desc] != want {
			t.Errorf("posting %s survived = %v, want %v", desc, got[desc], want)
		}
	}

	stored, err := p.ListIgnoredAssetClasses(ctx, userID)
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if len(stored) != 1 || stored[0].AssetClass != db.AssetClassFixedIncome {
		t.Fatalf("stored rules = %v, want the FIXED_INCOME rule", stored)
	}
}

// An account-scoped rule deletes only that account's postings.
func TestSetIgnoredAssetClasses_AccountScopedRule(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	bondID := setupInstrumentWithCurrency(t, p, "BOND-2", db.AssetClassFixedIncome, "USD")
	at := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	for _, account := range []string{"A", "B"} {
		tx := &apiv1.Tx{Timestamp: timestamppb.New(at), InstrumentDescription: "BOND-2",
			BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "1", Account: account}
		if err := createTx(ctx, p, userID, "IBKR", account, "", tx, bondID, nil); err != nil {
			t.Fatalf("create tx in %s: %v", account, err)
		}
	}

	rules := []db.IgnoredAssetClass{{Broker: "IBKR", Account: "A", AssetClass: db.AssetClassFixedIncome}}
	if err := p.SetIgnoredAssetClasses(ctx, userID, rules); err != nil {
		t.Fatalf("set ignored asset classes: %v", err)
	}

	var accounts []string
	if err := p.q.SelectContext(ctx, &accounts,
		`SELECT account FROM txs WHERE user_id = $1`, userID); err != nil {
		t.Fatalf("list remaining: %v", err)
	}
	if len(accounts) != 1 || accounts[0] != "B" {
		t.Fatalf("remaining accounts = %v, want [B]", accounts)
	}
}

// The count previews the regular postings a rule would delete. Synthetic pads
// are excluded: they are derived from holding declarations, which are counted
// separately.
func TestCountIgnoredTxs_CountsRegularTxsOnly(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	bondID, _ := seedIgnoreFixture(t, p, userID)
	at := time.Date(2024, 3, 2, 10, 0, 0, 0, time.UTC)
	insertRawPostingAs(t, p, userID, bondID, newTxGroup(t, p, userID), "IBKR", at, "INITIALIZE")

	rules := []db.IgnoredAssetClass{{Broker: "IBKR", AssetClass: db.AssetClassFixedIncome}}
	txCount, declCount, err := p.CountIgnoredTxs(ctx, userID, rules)
	if err != nil {
		t.Fatalf("count ignored txs: %v", err)
	}
	if txCount != 1 || declCount != 0 {
		t.Fatalf("count = (%d, %d), want (1, 0)", txCount, declCount)
	}
}
