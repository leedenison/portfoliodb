package postgres

import (
	"context"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "GOOG", Canonical: false}}, "", nil, nil)
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
