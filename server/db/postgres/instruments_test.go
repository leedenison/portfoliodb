package postgres

import (
	"context"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func strPtr(s string) *string { return &s }

// TestEnsureInstrument_mergeWhenMultipleInstrumentsMatch verifies that when multiple identifiers
// resolve to different instruments (e.g. A has ISIN 1, B has CUSIP 1), EnsureInstrument merges
// them and returns the survivor; both identifiers end up on the survivor and txs are updated.
func TestEnsureInstrument_mergeWhenMultipleInstrumentsMatch(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// Create instrument A with (ISIN, 1) and B with (CUSIP, 1).
	idA, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "ISIN", Value: "1", Canonical: true}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure A: %v", err)
	}
	idB, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{Type: "CUSIP", Value: "1", Canonical: true}}, "", nil, nil, nil)
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
	result, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{Type: "IBKR", Value: brokerDesc, Canonical: false},
		{Type: "ISIN", Value: "1", Canonical: true},
		{Type: "CUSIP", Value: "1", Canonical: true},
	}, "", nil, nil, nil)
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
	brokerOnlyID, err := p.EnsureInstrument(ctx, "", "", "", "BrokerOnly", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "BRK", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure broker-only: %v", err)
	}
	// Instrument with canonical identifier - should be included.
	withCanonID, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Apple", "", "", []db.IdentifierInput{
		{Type: "ISIN", Value: "US0378331005", Canonical: true},
		{Type: "IBKR", Value: "AAPL", Canonical: false},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure with canonical: %v", err)
	}
	list, err := p.ListInstrumentsForExport(ctx, "")
	if err != nil {
		t.Fatalf("ListInstrumentsForExport: %v", err)
	}
	// List includes seeded CASH instruments (migration 002) plus any we create; broker-only must be excluded.
	var foundApple bool
	for _, row := range list {
		if row.ID == brokerOnlyID {
			t.Fatalf("broker-only instrument %s should be excluded from export", brokerOnlyID)
		}
		if row.ID == withCanonID {
			foundApple = true
			if row.Name == nil || *row.Name != "Apple" || len(row.Identifiers) != 2 {
				t.Fatalf("expected Apple with 2 identifiers, got %+v", row)
			}
		}
	}
	if !foundApple {
		t.Fatalf("expected instrument %s (Apple) in export list (len=%d)", withCanonID, len(list))
	}
}

func TestListInstrumentsForExport_ExchangeFilter(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Nasdaq", "", "", []db.IdentifierInput{{Type: "ISIN", Value: "N1", Canonical: true}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure XNAS: %v", err)
	}
	_, err = p.EnsureInstrument(ctx, "STOCK", "XNYS", "USD", "NYSE", "", "", []db.IdentifierInput{{Type: "ISIN", Value: "Y1", Canonical: true}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure XNYS: %v", err)
	}
	list, err := p.ListInstrumentsForExport(ctx, "XNAS")
	if err != nil {
		t.Fatalf("ListInstrumentsForExport: %v", err)
	}
	// XNAS filter: seeded CASH instruments have exchange NULL so only our Nasdaq instrument matches.
	if len(list) != 1 || list[0].ExchangeMIC == nil || *list[0].ExchangeMIC != "XNAS" {
		var ex string
		if len(list) > 0 && list[0].ExchangeMIC != nil {
			ex = *list[0].ExchangeMIC
		}
		t.Fatalf("expected 1 instrument with exchange XNAS, got %d (first exchange %q)", len(list), ex)
	}
	listAll, err := p.ListInstrumentsForExport(ctx, "")
	if err != nil {
		t.Fatalf("ListInstrumentsForExport all: %v", err)
	}
	// No filter: seeded CASH instruments (canonical CURRENCY) plus our 2 STOCK instruments.
	if len(listAll) < 2 {
		t.Fatalf("expected at least 2 instruments with no filter, got %d", len(listAll))
	}
	var foundNasdaq, foundNYSE bool
	for _, row := range listAll {
		if row.Name != nil && *row.Name == "Nasdaq" && row.ExchangeMIC != nil && *row.ExchangeMIC == "XNAS" {
			foundNasdaq = true
		}
		if row.Name != nil && *row.Name == "NYSE" && row.ExchangeMIC != nil && *row.ExchangeMIC == "XNYS" {
			foundNYSE = true
		}
	}
	if !foundNasdaq || !foundNYSE {
		t.Fatalf("expected Nasdaq (XNAS) and NYSE (XNYS) in list (foundNasdaq=%v foundNYSE=%v)", foundNasdaq, foundNYSE)
	}
}

func TestEnsureInstrument_WithUnderlyingAndValidDates(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// Create underlying first (STOCK).
	underlyingID, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Apple Inc.", "0000320193", "3571", []db.IdentifierInput{
		{Type: "ISIN", Value: "US0378331005", Canonical: true},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure underlying: %v", err)
	}
	// Verify CIK and SICCode round-trip on the underlying.
	uRow, err := p.GetInstrument(ctx, underlyingID)
	if err != nil || uRow == nil {
		t.Fatalf("GetInstrument underlying: %v", err)
	}
	if uRow.CIK == nil || *uRow.CIK != "0000320193" {
		t.Errorf("CIK = %v, want %q", uRow.CIK, "0000320193")
	}
	if uRow.SICCode == nil || *uRow.SICCode != "3571" {
		t.Errorf("SICCode = %v, want %q", uRow.SICCode, "3571")
	}
	validFrom := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	validTo := time.Date(2025, 1, 17, 0, 0, 0, 0, time.UTC)
	// Create option with underlying_id and valid dates (empty exchange -- SMART is not a MIC).
	optionID, err := p.EnsureInstrument(ctx, "OPTION", "", "USD", "AAPL Call", "", "", []db.IdentifierInput{
		{Type: "IBKR", Value: "AAPL 20250117C200", Canonical: false},
	}, underlyingID, &validFrom, &validTo, &db.OptionFields{Strike: 230, Expiry: time.Date(2025, 12, 19, 0, 0, 0, 0, time.UTC), PutCall: "C"})
	if err != nil {
		t.Fatalf("ensure option: %v", err)
	}
	row, err := p.GetInstrument(ctx, optionID)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	if row.UnderlyingID == nil || *row.UnderlyingID != underlyingID {
		t.Errorf("UnderlyingID = %v, want %q", row.UnderlyingID, underlyingID)
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
	if rows[0].UnderlyingID == nil || *rows[0].UnderlyingID != underlyingID || rows[0].ValidFrom == nil {
		t.Errorf("ListInstrumentsByIDs row: UnderlyingID=%v ValidFrom=%v", rows[0].UnderlyingID, rows[0].ValidFrom)
	}
}

func TestEnsureInstrument_OptionWithoutUnderlying_Rejected(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, err := p.EnsureInstrument(ctx, "OPTION", "", "USD", "Option", "", "", []db.IdentifierInput{
		{Type: "IBKR", Value: "OPT1", Canonical: false},
	}, "", nil, nil, nil)
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
	_, err := p.EnsureInstrument(ctx, "unknown", "XNAS", "USD", "X", "", "", []db.IdentifierInput{
		{Type: "IBKR", Value: "X", Canonical: false},
	}, "", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid asset_class")
	}
}

// TestSeedCurrencyInstruments verifies migration 002_seed_currency_instruments populated CASH instruments
// and CURRENCY identifiers (USD, EUR, etc.). Requires TEST_DATABASE_URL and migrations applied.
func TestSeedCurrencyInstruments(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	for _, code := range []string{"USD", "EUR", "JPY", "GBP", "CHF"} {
		id, err := p.FindInstrumentByIdentifier(ctx, "CURRENCY", "", code)
		if err != nil {
			t.Fatalf("FindInstrumentByIdentifier CURRENCY %s: %v", code, err)
		}
		if id == "" {
			t.Fatalf("FindInstrumentByIdentifier CURRENCY %s: not found (migration 002 may not have run)", code)
		}
		row, err := p.GetInstrument(ctx, id)
		if err != nil || row == nil {
			t.Fatalf("GetInstrument %s: %v", id, err)
		}
		if row.AssetClass == nil || *row.AssetClass != "CASH" {
			t.Errorf("instrument %s asset_class = %v, want CASH", id, row.AssetClass)
		}
		if row.Currency == nil || *row.Currency != code {
			t.Errorf("instrument %s currency = %v, want %s", id, row.Currency, code)
		}
		hasCurrencyId := false
		for _, idn := range row.Identifiers {
			if idn.Type == "CURRENCY" && idn.Value == code {
				hasCurrencyId = true
				break
			}
		}
		if !hasCurrencyId {
			t.Errorf("instrument %s missing CURRENCY identifier %s", id, code)
		}
	}
}

func TestListInstruments_NullAssetClassMatchesUnknown(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// Create an instrument with no asset class (empty string stored as NULL).
	nullID, err := p.EnsureInstrument(ctx, "", "", "", "NoClass", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "test", Value: "NOCLASS", Canonical: false},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure null-class: %v", err)
	}
	// Create a STOCK instrument for comparison.
	stockID, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "StockCo", "", "", []db.IdentifierInput{
		{Type: "ISIN", Value: "US1234567890", Canonical: true},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure stock: %v", err)
	}

	// Filter by UNKNOWN should include the null-class instrument.
	rows, total, _, err := p.ListInstruments(ctx, "", []string{"UNKNOWN"}, 100, "")
	if err != nil {
		t.Fatalf("ListInstruments UNKNOWN: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.ID == nullID {
			found = true
		}
		if r.ID == stockID {
			t.Fatal("STOCK instrument should not appear in UNKNOWN filter")
		}
	}
	if !found {
		t.Fatalf("expected null-class instrument %s in UNKNOWN results (total=%d, rows=%d)", nullID, total, len(rows))
	}

	// Filter by STOCK should include stock but not null-class.
	rows, _, _, err = p.ListInstruments(ctx, "", []string{"STOCK"}, 100, "")
	if err != nil {
		t.Fatalf("ListInstruments STOCK: %v", err)
	}
	foundStock, foundNull := false, false
	for _, r := range rows {
		if r.ID == stockID {
			foundStock = true
		}
		if r.ID == nullID {
			foundNull = true
		}
	}
	if !foundStock {
		t.Fatal("expected STOCK instrument in STOCK filter")
	}
	if foundNull {
		t.Fatal("null-class instrument should not appear in STOCK filter")
	}

	// Filter by both STOCK and UNKNOWN should include both.
	rows, _, _, err = p.ListInstruments(ctx, "", []string{"STOCK", "UNKNOWN"}, 100, "")
	if err != nil {
		t.Fatalf("ListInstruments STOCK+UNKNOWN: %v", err)
	}
	foundStock, foundNull = false, false
	for _, r := range rows {
		if r.ID == stockID {
			foundStock = true
		}
		if r.ID == nullID {
			foundNull = true
		}
	}
	if !foundStock || !foundNull {
		t.Fatalf("expected both instruments in STOCK+UNKNOWN filter (stock=%v, null=%v)", foundStock, foundNull)
	}
}

func TestListInstruments_PaginationPastEnd(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// Create 3 instruments so we have a known small set (plus seeded CASH instruments).
	for i, name := range []string{"Alpha", "Beta", "Gamma"} {
		_, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", name, "", "", []db.IdentifierInput{
			{Type: "ISIN", Value: "TEST" + string(rune('A'+i)), Canonical: true},
		}, "", nil, nil, nil)
		if err != nil {
			t.Fatalf("ensure %s: %v", name, err)
		}
	}

	// Page 1: fetch with small page size to get a next token.
	rows, total, nextToken, err := p.ListInstruments(ctx, "", []string{"STOCK"}, 2, "")
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("page 1: expected 2 rows, got %d", len(rows))
	}
	if total != 3 {
		t.Fatalf("page 1: expected total=3, got %d", total)
	}
	if nextToken == "" {
		t.Fatal("page 1: expected next_page_token")
	}

	// Page 2: use the token; should get 1 result and no next token.
	rows, total, nextToken, err = p.ListInstruments(ctx, "", []string{"STOCK"}, 2, nextToken)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("page 2: expected 1 row, got %d", len(rows))
	}
	if total != 3 {
		t.Fatalf("page 2: expected total=3, got %d", total)
	}
	if nextToken != "" {
		t.Fatalf("page 2: expected empty next_page_token, got %q", nextToken)
	}

	// Page 3 (past end): use a fabricated token for offset beyond total.
	pastEndToken := "OTk5" // base64("999")
	rows, total, nextToken, err = p.ListInstruments(ctx, "", []string{"STOCK"}, 2, pastEndToken)
	if err != nil {
		t.Fatalf("past-end page: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("past-end page: expected 0 rows, got %d", len(rows))
	}
	if nextToken != "" {
		t.Fatalf("past-end page: expected empty next_page_token, got %q", nextToken)
	}
	// Total should still reflect the full count.
	if total != 3 {
		t.Fatalf("past-end page: expected total=3, got %d", total)
	}
}
