package postgres

import (
	"context"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestEnsureInstrument_mergeWhenMultipleInstrumentsMatch verifies that when multiple identifiers
// resolve to different instruments (e.g. A has ISIN 1, B has CUSIP 1), EnsureInstrument merges
// them and returns the survivor; both identifiers end up on the survivor and txs are updated.
func TestEnsureInstrument_mergeWhenMultipleInstrumentsMatch(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// Create instrument A with (ISIN, 1) and B with (CUSIP, 1).
	idA, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "ISIN", Value: "1", Canonical: true}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure A: %v", err)
	}
	idB, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "CUSIP", Value: "1", Canonical: true}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure B: %v", err)
	}
	if idA == idB {
		t.Fatal("A and B should be different instruments")
	}
	// Attach one tx to A and one to B.
	userID, _ := p.GetOrCreateUser(ctx, "sub|merge", "U", "u@u.com")
	_, _ = p.CreatePortfolio(ctx, userID, "P")
	now := time.Now()
	from := timestamppb.New(now.Add(-2 * time.Hour))
	to := timestamppb.New(now)
	ts1 := timestamppb.New(now.Add(-90 * time.Minute))
	ts2 := timestamppb.New(now.Add(-30 * time.Minute))
	txs := []*apiv1.Tx{
		{Timestamp: ts1, InstrumentDescription: "StockA", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: ""},
		{Timestamp: ts2, InstrumentDescription: "StockB", Type: apiv1.TxType_BUYSTOCK, Quantity: 5, Account: ""},
	}
	err = p.ReplaceTxsInPeriod(ctx, userID, "IBKR", from, to, txs, []string{idA, idB})
	if err != nil {
		t.Fatalf("replace txs: %v", err)
	}
	// Resolve with identifiers that match both A and B; should merge and return survivor.
	brokerDesc := "SomeStock"
	result, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{
		{Type: "IBKR", Value: brokerDesc, Canonical: false},
		{Type: "ISIN", Value: "1", Canonical: true},
		{Type: "CUSIP", Value: "1", Canonical: true},
	}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure merge: %v", err)
	}
	if result != idA && result != idB {
		t.Fatalf("result %s should be either A %s or B %s", result, idA, idB)
	}
	survivor, mergedAway := result, idA
	if result == idA {
		mergedAway = idB
	}
	// Merged-away instrument should be gone.
	gone, _ := p.GetInstrument(ctx, mergedAway)
	if gone != nil {
		t.Fatalf("merged-away instrument %s should be deleted, got %+v", mergedAway, gone)
	}
	// Survivor should have both identifiers.
	row, err := p.GetInstrument(ctx, survivor)
	if err != nil || row == nil {
		t.Fatalf("get survivor: %v %v", err, row)
	}
	hasISIN, hasCUSIP := false, false
	for _, idn := range row.Identifiers {
		if idn.Type == "ISIN" && idn.Value == "1" {
			hasISIN = true
			if !idn.Canonical {
				t.Fatal("ISIN identifier should have Canonical true after merge")
			}
		}
		if idn.Type == "CUSIP" && idn.Value == "1" {
			hasCUSIP = true
			if !idn.Canonical {
				t.Fatal("CUSIP identifier should have Canonical true after merge")
			}
		}
		if idn.Type == "IBKR" && idn.Value == brokerDesc && idn.Canonical {
			t.Fatal("broker description identifier should have Canonical false after merge")
		}
	}
	if !hasISIN || !hasCUSIP {
		t.Fatalf("survivor should have both ISIN 1 and CUSIP 1, got %+v", row.Identifiers)
	}
	// Both txs should now point at survivor (holdings or re-query would show one instrument).
	holdings, _, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	// We had two txs (10 and 5) on two instruments; after merge both are on survivor, so one holding with quantity 15.
	var totalQty float64
	for _, h := range holdings {
		if h.InstrumentId == survivor {
			totalQty += h.Quantity
		}
	}
	if totalQty != 15 {
		t.Fatalf("expected merged holding quantity 15, got %v (holdings: %+v)", totalQty, holdings)
	}
}

func TestListInstrumentsForExport_ExcludesBrokerDescriptionOnly(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// Instrument with only broker description (canonical=false) - should be excluded.
	brokerOnlyID, err := p.EnsureInstrument(ctx, "", "", "", "BrokerOnly", []db.IdentifierInput{{Type: "IBKR", Value: "BRK", Canonical: false}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure broker-only: %v", err)
	}
	// Instrument with canonical identifier - should be included.
	withCanonID, err := p.EnsureInstrument(ctx, "EQUITY", "XNAS", "USD", "Apple", []db.IdentifierInput{
		{Type: "ISIN", Value: "US0378331005", Canonical: true},
		{Type: "IBKR", Value: "AAPL", Canonical: false},
	}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure with canonical: %v", err)
	}
	list, err := p.ListInstrumentsForExport(ctx, "")
	if err != nil {
		t.Fatalf("ListInstrumentsForExport: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 instrument (exclude broker-only), got %d", len(list))
	}
	if list[0].ID != withCanonID {
		t.Fatalf("expected instrument %s, got %s", withCanonID, list[0].ID)
	}
	if list[0].Name != "Apple" || len(list[0].Identifiers) != 2 {
		t.Fatalf("expected Apple with 2 identifiers, got %+v", list[0])
	}
	_ = brokerOnlyID
}

func TestListInstrumentsForExport_ExchangeFilter(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, err := p.EnsureInstrument(ctx, "EQUITY", "XNAS", "USD", "Nasdaq", []db.IdentifierInput{{Type: "ISIN", Value: "N1", Canonical: true}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure XNAS: %v", err)
	}
	_, err = p.EnsureInstrument(ctx, "EQUITY", "XNYS", "USD", "NYSE", []db.IdentifierInput{{Type: "ISIN", Value: "Y1", Canonical: true}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure XNYS: %v", err)
	}
	list, err := p.ListInstrumentsForExport(ctx, "XNAS")
	if err != nil {
		t.Fatalf("ListInstrumentsForExport: %v", err)
	}
	if len(list) != 1 || list[0].Exchange != "XNAS" {
		t.Fatalf("expected 1 instrument with exchange XNAS, got %d (first exchange %q)", len(list), list[0].Exchange)
	}
	listAll, err := p.ListInstrumentsForExport(ctx, "")
	if err != nil {
		t.Fatalf("ListInstrumentsForExport all: %v", err)
	}
	if len(listAll) != 2 {
		t.Fatalf("expected 2 instruments with no filter, got %d", len(listAll))
	}
}

func TestEnsureInstrument_WithUnderlyingAndValidDates(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// Create underlying first (EQUITY).
	underlyingID, err := p.EnsureInstrument(ctx, "EQUITY", "XNAS", "USD", "Apple Inc.", []db.IdentifierInput{
		{Type: "ISIN", Value: "US0378331005", Canonical: true},
	}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure underlying: %v", err)
	}
	validFrom := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	validTo := time.Date(2025, 1, 17, 0, 0, 0, 0, time.UTC)
	// Create option with underlying_id and valid dates.
	optionID, err := p.EnsureInstrument(ctx, "OPTION", "SMART", "USD", "AAPL Call", []db.IdentifierInput{
		{Type: "IBKR", Value: "AAPL 20250117C200", Canonical: false},
	}, underlyingID, &validFrom, &validTo)
	if err != nil {
		t.Fatalf("ensure option: %v", err)
	}
	row, err := p.GetInstrument(ctx, optionID)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	if row.UnderlyingID != underlyingID {
		t.Errorf("UnderlyingID = %q, want %q", row.UnderlyingID, underlyingID)
	}
	if row.ValidFrom == nil || !row.ValidFrom.Equal(validFrom) {
		t.Errorf("ValidFrom = %v, want %v", row.ValidFrom, validFrom)
	}
	if row.ValidTo == nil || !row.ValidTo.Equal(validTo) {
		t.Errorf("ValidTo = %v, want %v", row.ValidTo, validTo)
	}
	// ListInstrumentsByIDs returns the option with same fields.
	rows, err := p.ListInstrumentsByIDs(ctx, []string{optionID})
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListInstrumentsByIDs: %v (len=%d)", err, len(rows))
	}
	if rows[0].UnderlyingID != underlyingID || rows[0].ValidFrom == nil {
		t.Errorf("ListInstrumentsByIDs row: UnderlyingID=%q ValidFrom=%v", rows[0].UnderlyingID, rows[0].ValidFrom)
	}
}

func TestEnsureInstrument_OptionWithoutUnderlying_Rejected(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, err := p.EnsureInstrument(ctx, "OPTION", "SMART", "USD", "Option", []db.IdentifierInput{
		{Type: "IBKR", Value: "OPT1", Canonical: false},
	}, "", nil, nil)
	if err == nil {
		t.Fatal("expected error when OPTION has no underlying_id")
	}
	if err.Error() != "underlying_id required when asset_class is OPTION" {
		t.Errorf("got error: %v", err)
	}
}

func TestEnsureInstrument_InvalidAssetClass_Rejected(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, err := p.EnsureInstrument(ctx, "unknown", "XNAS", "USD", "X", []db.IdentifierInput{
		{Type: "IBKR", Value: "X", Canonical: false},
	}, "", nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid asset_class")
	}
}
