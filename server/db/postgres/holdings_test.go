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
		[]db.IdentifierInput{{Type: "CURRENCY", Value: "USD", Canonical: true}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure cash instrument: %v", err)
	}

	// An income transaction whose tx description is the source security, not
	// the cash instrument name.
	txs := []*apiv1.Tx{
		{Timestamp: ts, InstrumentDescription: "MSFT MICROSOFT CORP", Type: apiv1.TxType_INCOME, Quantity: 137.08, Account: ""},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", from, to, txs, []string{cashID}); err != nil {
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
		{Timestamp: ts, InstrumentDescription: "GOOG", Type: apiv1.TxType_SELLSTOCK, Quantity: -5, Account: ""},
	}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "GOOG", Canonical: false}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	err = p.ReplaceTxsInPeriod(ctx, userID, "IBKR", from, to, txs, []string{instID})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	holdings, _, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	var googQty float64
	for _, h := range holdings {
		if h.InstrumentDescription == "GOOG" {
			googQty = h.Quantity
			break
		}
	}
	if googQty != -5 {
		t.Fatalf("expected GOOG quantity -5 (signed quantity, no type-based flip), got %v", googQty)
	}
}
