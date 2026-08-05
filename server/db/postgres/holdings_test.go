package postgres

import (
	"context"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestComputeHoldings_instrumentNameOverTxDescription verifies that the holdings
// instrument_description field uses the instrument's canonical name when the
// instrument has been resolved, falling back to the transaction description
// only when no instrument name is set.  This is important because the
// transaction description reflects the broker's label (e.g. "MSFT MICROSOFT
// CORP" on a dividend) while the instrument name reflects what the instrument
// actually is (e.g. "USD" for a cash instrument).
func TestComputeHoldings_instrumentNameOverTxDescription(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|desc", "U", "u@u.com")
	_, _ = p.CreatePortfolio(ctx, userID, "P")
	now := time.Now()
	from := timestamppb.New(now.Add(-1 * time.Hour))
	to := timestamppb.New(now)
	ts := timestamppb.New(now.Add(-30 * time.Minute))

	// Create a cash instrument with a canonical name "USD".
	cashID, err := p.EnsureInstrument(ctx, "CASH", "", "USD", "USD", "", "",
		[]db.IdentifierInput{{Type: "CURRENCY", Value: "USD", Canonical: true}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure cash instrument: %v", err)
	}

	// An income transaction whose tx description is the source security, not
	// the cash instrument name.
	txs := []*apiv1.Tx{
		{Timestamp: ts, InstrumentDescription: "MSFT MICROSOFT CORP", Type: apiv1.TxType_INCOME, Quantity: "137.08", Account: ""},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{cashID}, nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	holdings, _, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	if len(holdings) != 1 {
		t.Fatalf("expected 1 holding, got %d", len(holdings))
	}
	h := holdings[0]
	if h.InstrumentDescription != "USD" {
		t.Errorf("holding instrument_description = %q, want %q (instrument name, not tx description)", h.InstrumentDescription, "USD")
	}
}

// TestComputeHoldings_signedQuantity verifies holdings are SUM(quantity) with no type-based sign flip.
// Sells have negative quantity; buys positive. A position that is net short has negative holding.
func TestComputeHoldings_signedQuantity(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|hold", "U", "u@u.com")
	_, _ = p.CreatePortfolio(ctx, userID, "P")
	now := time.Now()
	from := timestamppb.New(now.Add(-1 * time.Hour))
	to := timestamppb.New(now)
	ts := timestamppb.New(now.Add(-30 * time.Minute))
	// Only a sell with negative quantity: no buys. Net position should be -5.
	txs := []*apiv1.Tx{
		{Timestamp: ts, InstrumentDescription: "GOOG", Type: apiv1.TxType_SELLSTOCK, Quantity: "-5", Account: ""},
	}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "GOOG", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	err = p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	holdings, _, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	var googQty string
	for _, h := range holdings {
		if h.InstrumentDescription == "GOOG" {
			googQty = h.Quantity
			break
		}
	}
	if googQty != "-5" {
		t.Fatalf("expected GOOG quantity -5 (signed quantity, no type-based flip), got %v", googQty)
	}
}

// TestComputeHoldings_fractionalQuantitiesSumExactly pins the reason the quantity
// columns are NUMERIC. Ten buys of 0.1 sum to exactly 1 in decimal; in binary
// floating point they sum to 0.9999999999999999, and that residual reached the
// user as the holding quantity. qty_is_zero's old epsilon hid the closed-position
// case but nothing guarded the open one.
func TestComputeHoldings_fractionalQuantitiesSumExactly(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|exact", "U", "u@u.com")
	now := time.Now()
	from := timestamppb.New(now.Add(-1 * time.Hour))
	to := timestamppb.New(now)
	at := timestamppb.New(now.Add(-30 * time.Minute))

	txs := make([]*apiv1.Tx, 0, 10)
	instIDs := make([]string, 0, 10)
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "FRAC", Canonical: false},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	for i := 0; i < 10; i++ {
		txs = append(txs, &apiv1.Tx{
			Timestamp: at, InstrumentDescription: "FRAC",
			Type: apiv1.TxType_BUYSTOCK, Quantity: "0.1",
		})
		instIDs = append(instIDs, instID)
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, instIDs, nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	holdings, _, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	var got string
	for _, h := range holdings {
		if h.InstrumentDescription == "FRAC" {
			got = h.Quantity
			break
		}
	}
	// Exact equality, not approxEq: that is the whole point.
	if got != "1" {
		t.Fatalf("ten buys of 0.1: got %v want exactly 1", got)
	}
}

// TestComputeHoldings_excludesNonUserAccountTypes verifies the counterparty leg of a
// one-sided event does not net against the holding it balances. The EQUITY leg here is
// what an INITIALIZE pad's counterparty looks like: same instrument, same broker
// account, equal and opposite. Without the account_type predicate the two sum to zero
// and the declared opening balance silently disappears.
func TestComputeHoldings_excludesNonUserAccountTypes(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|acct-type-hold", "U", "u@ah.com")
	now := time.Now()
	from := timestamppb.New(now.Add(-1 * time.Hour))
	to := timestamppb.New(now)
	ts := timestamppb.New(now.Add(-30 * time.Minute))
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "",
		[]db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "TSCO", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	txs := []*apiv1.Tx{
		{Timestamp: ts, InstrumentDescription: "TSCO", Type: apiv1.TxType_BUYSTOCK, Quantity: "40", Account: "A", GroupRef: "pad"},
		{Timestamp: ts, InstrumentDescription: "TSCO", Type: apiv1.TxType_BUYSTOCK, Quantity: "-40", Account: "A", GroupRef: "pad", AccountType: apiv1.AccountType_ACCOUNT_TYPE_EQUITY},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID, instID}, nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	holdings, _, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	if len(holdings) != 1 {
		t.Fatalf("expected 1 holding, got %d", len(holdings))
	}
	if got := holdings[0].Quantity; got != "40" {
		t.Errorf("holding quantity = %v, want 40: the EQUITY counter-leg must not net against the position", got)
	}
}
