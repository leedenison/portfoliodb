package postgres

import (
	"context"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
	"strings"
	"testing"
	"time"
)

// The balance constraint is DEFERRABLE INITIALLY DEFERRED, so it fires at COMMIT --
// and testDBTx rolls back and never commits, which would leave every test here
// vacuous. SET CONSTRAINTS ... IMMEDIATE drains the queued checks at the point COMMIT
// would have run them, and makes every later statement in the transaction check as it
// goes. That is what these tests assert against.
//
// Because it also switches later statements to immediate checking, a test that wants
// to write a group leg by leg must write it all first and drain afterwards, which is
// the same order a real transaction uses.
func drainBalanceChecks(t *testing.T, p *Postgres) error {
	t.Helper()
	_, err := p.q.ExecContext(context.Background(),
		`SET CONSTRAINTS trg_tx_group_balance, trg_tx_group_balance_update IMMEDIATE`)
	return err
}

// balanceSeed creates a user and the USD currency instrument the postings below weigh
// in, and returns both.
func balanceSeed(t *testing.T, p *Postgres, sub string) (string, string) {
	t.Helper()
	ctx := context.Background()
	userID, err := p.GetOrCreateUser(ctx, sub, "U", sub+"@b.com")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	usd, err := p.EnsureInstrument(ctx, "CASH", "", "USD", "USD", "", "",
		[]db.IdentifierInput{{Type: "CURRENCY", Value: "USD", Canonical: true}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure USD: %v", err)
	}
	return userID, usd
}

// insertRawPosting writes one posting with a declared weight, bypassing the balancing
// path, which is the only way to construct the states these tests are about.
func insertRawPosting(t *testing.T, p *Postgres, userID, instID, groupID, qty, weight, commodity string) {
	t.Helper()
	_, err := p.q.ExecContext(context.Background(), `
		INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type,
		                 quantity, instrument_id, weight, weight_commodity, group_id)
		VALUES ($1::uuid, 'IBKR', 'A', now(), 'USD', 'JRNLFUND', $2::numeric, $3::uuid,
		        $4::numeric, $5, $6::uuid)
	`, userID, qty, instID, weight, commodity, groupID)
	if err != nil {
		t.Fatalf("insert posting: %v", err)
	}
}

// TestTxGroupBalance_LegsMayArriveInAnyOrder is the reason the constraint is deferred.
// Each leg on its own leaves the group unbalanced, so an immediate check would reject
// whichever arrived first however the writer ordered them.
func TestTxGroupBalance_LegsMayArriveInAnyOrder(t *testing.T) {
	p := testDBTx(t)
	userID, usd := balanceSeed(t, p, "sub|balance-order")
	groupID := newTxGroup(t, p, userID)

	// Negative leg first: the group is -1000 USD until the second arrives.
	insertRawPosting(t, p, userID, usd, groupID, "-1000", "-1000", "cur:USD")
	insertRawPosting(t, p, userID, usd, groupID, "1000", "1000", "cur:USD")

	if err := drainBalanceChecks(t, p); err != nil {
		t.Fatalf("balanced group rejected: %v", err)
	}
}

// TestTxGroupBalance_UnbalancedGroupIsRejected is the invariant itself. The message
// has to name the group and the commodity, because a rejection that says only "does
// not balance" leaves whoever hits it nowhere to start.
func TestTxGroupBalance_UnbalancedGroupIsRejected(t *testing.T) {
	p := testDBTx(t)
	userID, usd := balanceSeed(t, p, "sub|balance-reject")
	groupID := newTxGroup(t, p, userID)

	insertRawPosting(t, p, userID, usd, groupID, "-1000", "-1000", "cur:USD")

	err := drainBalanceChecks(t, p)
	if err == nil {
		t.Fatal("unbalanced group accepted: want a constraint violation")
	}
	for _, want := range []string{groupID, "-1000", "cur:USD"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("violation message %q does not name %q", err.Error(), want)
		}
	}
}

// TestTxGroupBalance_PerCommodity verifies the check is per commodity rather than over
// the group as a whole. A buy is +10 AAPL and -1855 USD, and a group whose two
// commodities happen to cancel numerically has not balanced in either of them.
func TestTxGroupBalance_PerCommodity(t *testing.T) {
	p := testDBTx(t)
	userID, usd := balanceSeed(t, p, "sub|balance-commodity")
	groupID := newTxGroup(t, p, userID)

	insertRawPosting(t, p, userID, usd, groupID, "10", "10", "cur:USD")
	insertRawPosting(t, p, userID, usd, groupID, "-10", "-10", "inst:"+usd)

	if err := drainBalanceChecks(t, p); err == nil {
		t.Fatal("a group summing to zero only across commodities was accepted")
	}
}

// TestTxGroupBalance_DeletingAWholeGroupPasses verifies the cascade replace-by-period
// depends on. Deleting the group takes its postings with it, and each deleted posting
// queues a check against a group that no longer has any -- which has to pass
// vacuously rather than read as an unbalanced group.
func TestTxGroupBalance_DeletingAWholeGroupPasses(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, usd := balanceSeed(t, p, "sub|balance-delete")
	groupID := newTxGroup(t, p, userID)

	insertRawPosting(t, p, userID, usd, groupID, "-1000", "-1000", "cur:USD")
	insertRawPosting(t, p, userID, usd, groupID, "1000", "1000", "cur:USD")

	if _, err := p.q.ExecContext(ctx, `DELETE FROM tx_groups WHERE id = $1::uuid`, groupID); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if err := drainBalanceChecks(t, p); err != nil {
		t.Fatalf("deleting a whole group was rejected: %v", err)
	}
}

// TestTxGroupBalance_DeletingOneLegIsRejected is the other half of the delete case:
// the cascade is safe, but removing a single posting is not, and the constraint has to
// tell them apart.
func TestTxGroupBalance_DeletingOneLegIsRejected(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, usd := balanceSeed(t, p, "sub|balance-delete-leg")
	groupID := newTxGroup(t, p, userID)

	insertRawPosting(t, p, userID, usd, groupID, "-1000", "-1000", "cur:USD")
	insertRawPosting(t, p, userID, usd, groupID, "1000", "1000", "cur:USD")
	if err := drainBalanceChecks(t, p); err != nil {
		t.Fatalf("balanced group rejected: %v", err)
	}

	_, err := p.q.ExecContext(ctx, `DELETE FROM txs WHERE group_id = $1::uuid AND weight > 0`, groupID)
	if err == nil {
		t.Fatal("deleting one leg of a balanced group was accepted")
	}
}

// TestTxGroupBalance_UpdatingAWeightIsChecked verifies the UPDATE trigger's WHEN guard
// lets through the thing it must: a change to weight itself. The guard exists so that
// the split-adjustment recompute does not queue a check per row, and a guard that was
// too broad would stop the constraint noticing an edit to the value it reads.
func TestTxGroupBalance_UpdatingAWeightIsChecked(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, usd := balanceSeed(t, p, "sub|balance-update")
	groupID := newTxGroup(t, p, userID)

	insertRawPosting(t, p, userID, usd, groupID, "-1000", "-1000", "cur:USD")
	insertRawPosting(t, p, userID, usd, groupID, "1000", "1000", "cur:USD")
	if err := drainBalanceChecks(t, p); err != nil {
		t.Fatalf("balanced group rejected: %v", err)
	}

	_, err := p.q.ExecContext(ctx,
		`UPDATE txs SET weight = 999 WHERE group_id = $1::uuid AND weight > 0`, groupID)
	if err == nil {
		t.Fatal("editing a weight out of balance was accepted")
	}
}

// TestTxGroupBalance_SplitRecomputeIsNotAffected verifies the recompute the WHEN guard
// exists for. It rewrites split_adjusted_* for every posting of an instrument and
// leaves weight alone, so it must neither fail nor be checked -- weight is computed
// from the raw columns the recompute does not touch.
func TestTxGroupBalance_SplitRecomputeIsNotAffected(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, usd := balanceSeed(t, p, "sub|balance-recompute")
	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "",
		[]db.IdentifierInput{{Type: "MIC_TICKER", Domain: "XNAS", Value: "SPLT", Canonical: true}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	price := "100"
	now := time.Now()
	from, to := timestamppb.New(now.Add(-time.Hour)), timestamppb.New(now.Add(time.Hour))
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(now), InstrumentDescription: "SPLT", Type: typev1.TxType_BUYSTOCK,
			Quantity: "10", UnitPrice: &price, SettlementCurrency: "USD", GroupRef: "g1"},
		{Timestamp: timestamppb.New(now), InstrumentDescription: "USD", Type: typev1.TxType_BUYSTOCK,
			Quantity: "-1000", SettlementCurrency: "USD", TradingCurrency: "USD", GroupRef: "g1"},
	}
	ws := []db.Weight{{Amount: decf(1000), Commodity: "cur:USD"}, {Amount: decf(-1000), Commodity: "cur:USD"}}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID, usd}, ws, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}
	if _, err := p.q.ExecContext(ctx, `
		INSERT INTO stock_splits (instrument_id, ex_date, split_from, split_to, data_provider)
		VALUES ($1::uuid, $2::date, 1, 3, 'test')
	`, instID, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("insert split: %v", err)
	}
	if err := drainBalanceChecks(t, p); err != nil {
		t.Fatalf("balanced group rejected before the recompute: %v", err)
	}

	// A 3:1 split triples the security leg's quantity and thirds its price, which
	// does not divide exactly -- the point of weighing the raw columns.
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute rejected by the balance constraint: %v", err)
	}
}
