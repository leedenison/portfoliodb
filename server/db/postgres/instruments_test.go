package postgres

import (
	"context"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
	"time"
)

// TestEnsureInstrument_mergeWhenMultipleInstrumentsMatch verifies that when multiple identifiers
// resolve to different instruments (e.g. A has ISIN 1, B has CUSIP 1), EnsureInstrument merges
// them and returns the survivor; both identifiers end up on the survivor and txs are updated.
func TestEnsureInstrument_mergeWhenMultipleInstrumentsMatch(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// Create instrument A with (ISIN, 1) and B with (CUSIP, 1).
	idA, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "ISIN", Value: "1"},
		Canonical: true,
	}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure A: %v", err)
	}
	idB, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "CUSIP", Value: "1"},
		Canonical: true,
	}}, nil, "", nil, nil, nil)
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
		{OrderDate: ts1,
			TradeDate: ts1, InstrumentDescription: "StockA", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: ""},
		{OrderDate: ts2,
			TradeDate: ts2, InstrumentDescription: "StockB", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "5", Account: ""},
	}
	err = p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{idA, idB}, nil, nil)
	if err != nil {
		t.Fatalf("replace txs: %v", err)
	}
	// Resolve with identifiers that match both A and B; should merge and return survivor.
	brokerDesc := "SomeStock"
	result, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "IBKR", Value: brokerDesc},
			Canonical: false,
		},
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "1"},
			Canonical: true,
		},
		{
			Ref:       db.InstrumentRef{Type: "CUSIP", Value: "1"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
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
		if idn.Ref.Type == "ISIN" && idn.Ref.Value == "1" {
			hasISIN = true
			if !idn.Canonical {
				t.Fatal("ISIN identifier should have Canonical true after merge")
			}
		}
		if idn.Ref.Type == "CUSIP" && idn.Ref.Value == "1" {
			hasCUSIP = true
			if !idn.Canonical {
				t.Fatal("CUSIP identifier should have Canonical true after merge")
			}
		}
		if idn.Ref.Type == "IBKR" && idn.Ref.Value == brokerDesc && idn.Canonical {
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
	totalQty := decimal.Zero
	for _, h := range holdings {
		if h.InstrumentId == survivor {
			totalQty = totalQty.Add(decimal.RequireFromString(h.SplitAdjustedQuantity))
		}
	}
	if !totalQty.Equal(decimal.NewFromInt(15)) {
		t.Fatalf("expected merged holding quantity 15, got %v (holdings: %+v)", totalQty, holdings)
	}
}

func TestListInstrumentsForExport_ExcludesBrokerDescriptionOnly(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// Instrument with only broker description (canonical=false) - should be excluded.
	brokerOnlyID, err := p.EnsureInstrument(ctx, "", "", "", "BrokerOnly", "", "", []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "BRK", Domain: "IBKR"},
		Canonical: false,
	}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure broker-only: %v", err)
	}
	// Instrument with canonical identifier - should be included.
	withCanonID, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Apple", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0378331005"},
			Canonical: true,
		},
		{
			Ref:       db.InstrumentRef{Type: "IBKR", Value: "AAPL"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure with canonical: %v", err)
	}
	list, err := p.ListInstrumentsForExport(ctx, "", nil)
	if err != nil {
		t.Fatalf("ListInstrumentsForExport: %v", err)
	}
	// List excludes seeded CASH/FX instruments (reference data); broker-only must also be excluded.
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
	_, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Nasdaq", "", "", []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "ISIN", Value: "N1"},
		Canonical: true,
	}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure XNAS: %v", err)
	}
	_, err = p.EnsureInstrument(ctx, "STOCK", "XNYS", "USD", "NYSE", "", "", []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "ISIN", Value: "Y1"},
		Canonical: true,
	}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure XNYS: %v", err)
	}
	list, err := p.ListInstrumentsForExport(ctx, "XNAS", nil)
	if err != nil {
		t.Fatalf("ListInstrumentsForExport: %v", err)
	}
	// XNAS filter: only our Nasdaq STOCK instrument matches. The seeded currency
	// and FX rows name no exchange, so the filter excludes them.
	if len(list) != 1 || list[0].ExchangeMIC == nil || *list[0].ExchangeMIC != "XNAS" {
		var ex string
		if len(list) > 0 && list[0].ExchangeMIC != nil {
			ex = *list[0].ExchangeMIC
		}
		t.Fatalf("expected 1 instrument with exchange XNAS, got %d (first exchange %q)", len(list), ex)
	}
	listAll, err := p.ListInstrumentsForExport(ctx, "", nil)
	if err != nil {
		t.Fatalf("ListInstrumentsForExport all: %v", err)
	}
	// No filter means everything, so the seeded reference data comes too and the
	// count is not the interesting thing -- that both exchanges are there is.
	if len(listAll) <= 2 {
		t.Fatalf("expected the seeded reference data alongside both stocks, got %d", len(listAll))
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
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0378331005"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
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
	// Exclusive, so the option's last tradeable day is 17 January.
	validBefore := time.Date(2025, 1, 18, 0, 0, 0, 0, time.UTC)
	// Create option with underlying_id and valid dates (empty exchange -- SMART is not a MIC).
	optionID, err := p.EnsureInstrument(ctx, "OPTION", "", "USD", "AAPL Call", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "IBKR", Value: "AAPL 20250117C200"},
			Canonical: false,
		}}, nil, underlyingID, &validFrom, &validBefore, &db.OptionFields{Strike: decf(230), Expiry: time.Date(2025, 12, 19, 0, 0, 0, 0, time.UTC), PutCall: "C"})
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
	if row.ValidBefore == nil || !row.ValidBefore.Equal(validBefore) {
		t.Errorf("ValidBefore = %v, want %v", row.ValidBefore, validBefore)
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

// TestEnsureInstrument_LeavesNameDatesAlone pins the write discipline behind
// issue 0055, now that it lives on the name: EnsureInstrument must never move an
// identifier's valid_from on a match. Treating a re-sighting as evidence that
// the name became correct today is what used to make an option look
// already-restated and permanently skip a restatement it needed. When a name
// became correct is a market fact; seeing it again is not a new one.
func TestEnsureInstrument_LeavesNameDatesAlone(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	idns := []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "ISIN", Value: "US4581401001"},
		Canonical: true,
		ValidFrom: day(2024, 1, 1),
	}}
	id, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Intel", "", "", idns, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	row, err := p.GetInstrument(ctx, id)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	stored := findIdentifier(row, "ISIN", "US4581401001")
	if stored == nil || stored.ValidFrom == nil || !stored.ValidFrom.Equal(*day(2024, 1, 1)) {
		t.Fatalf("valid_from after create = %v, want 2024-01-01", stored)
	}

	// The same identifier stated again with a later vintage matches rather than
	// inserting, and the stored bound must not move.
	later := []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "ISIN", Value: "US4581401001"},
		Canonical: true,
		ValidFrom: day(2025, 6, 1),
	}}
	again, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Intel", "", "", later, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("re-ensure: %v", err)
	}
	if again != id {
		t.Fatalf("re-ensure returned %q, want %q", again, id)
	}
	row, err = p.GetInstrument(ctx, id)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument after re-ensure: %v", err)
	}
	stored = findIdentifier(row, "ISIN", "US4581401001")
	if stored == nil || stored.ValidFrom == nil || !stored.ValidFrom.Equal(*day(2024, 1, 1)) {
		t.Errorf("valid_from moved on an incidental touch: got %v, want 2024-01-01", stored)
	}
	if n := len(row.Identifiers); n != 1 {
		t.Errorf("identifiers = %d, want the one row restated rather than a second", n)
	}
}

// TestMergeInstrumentFromArchive_RestoresNameDates covers the archive round
// trip: a file states the interval each name was correct over, and an import
// that dropped it would leave an already-restated option looking unrestated and
// lose the symbol the contract traded under before the split.
func TestMergeInstrumentFromArchive_RestoresNameDates(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	idns := []db.IdentifierInput{
		{
			Ref:         db.InstrumentRef{Type: "OCC", Value: "CSCO250117C00060000"},
			Canonical:   true,
			ValidBefore: day(2024, 6, 10),
		},
		{
			Ref:       db.InstrumentRef{Type: "OCC", Value: "CSCO250117C00030000"},
			Canonical: true,
			ValidFrom: day(2024, 6, 10),
		}}
	id, err := p.EnsureInstrument(ctx, "", "", "USD", "", "", "", idns, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := p.MergeInstrumentFromArchive(ctx, id, db.InstrumentMerge{Identifiers: idns}); err != nil {
		t.Fatalf("merge: %v", err)
	}

	row, err := p.GetInstrument(ctx, id)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	if n := len(row.Identifiers); n != 2 {
		t.Fatalf("identifiers = %d, want 2 -- the merge restated them rather than duplicating", n)
	}
	closed := findIdentifier(row, "OCC", "CSCO250117C00060000")
	if closed == nil || closed.ValidBefore == nil || !closed.ValidBefore.Equal(*day(2024, 6, 10)) {
		t.Errorf("the given-up name = %v, want it closed at 2024-06-10", closed)
	}
	open := findIdentifier(row, "OCC", "CSCO250117C00030000")
	if open == nil || open.ValidFrom == nil || !open.ValidFrom.Equal(*day(2024, 6, 10)) || open.ValidBefore != nil {
		t.Errorf("the name in force = %v, want it open from 2024-06-10", open)
	}
	// The name in force is the one the trigger derives from.
	if row.Name == nil || *row.Name != "CSCO250117C00030000" {
		t.Errorf("name = %v, want the name in force", row.Name)
	}
}

func TestEnsureInstrument_OptionWithoutUnderlying_Rejected(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, err := p.EnsureInstrument(ctx, "OPTION", "", "USD", "Option", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "IBKR", Value: "OPT1"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
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
		{
			Ref:       db.InstrumentRef{Type: "IBKR", Value: "X"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
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
			if idn.Ref.Type == "CURRENCY" && idn.Ref.Value == code {
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
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "NOCLASS", Domain: "test"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure null-class: %v", err)
	}
	// Create a STOCK instrument for comparison.
	stockID, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "StockCo", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US1234567890"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
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

// TestFindInstrumentWithMetaByIdentifier_ReturnsMIC verifies that FindInstrumentWithMetaByIdentifier
// returns the exchange_mic column (e.g. "XNAS"), not the denormalized exchange acronym (e.g. "NASDAQ").
func TestFindInstrumentWithMetaByIdentifier_ReturnsMIC(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// Create instrument with exchange_mic = "XNAS". The DB trigger sets the
	// denormalized exchange column to "NASDAQ" (the acronym from the exchanges table).
	_, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "TestCo", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "TEST", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}

	// Look up by exact (type, domain, value).
	_, _, exch, _, err := p.FindInstrumentWithMetaByIdentifier(ctx, "MIC_TICKER", "XNAS", "TEST")
	if err != nil {
		t.Fatalf("FindInstrumentWithMetaByIdentifier: %v", err)
	}
	if exch != "XNAS" {
		t.Errorf("exchange = %q, want %q (must be MIC code, not acronym)", exch, "XNAS")
	}

	// Also verify the domain-less path returns MIC.
	_, err = p.EnsureInstrument(ctx, "STOCK", "XNYS", "USD", "TestCo2", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US9999999999"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure XNYS: %v", err)
	}
	_, _, exch2, _, err := p.FindInstrumentWithMetaByIdentifier(ctx, "ISIN", "", "US9999999999")
	if err != nil {
		t.Fatalf("FindInstrumentWithMetaByIdentifier domain-less: %v", err)
	}
	if exch2 != "XNYS" {
		t.Errorf("exchange (domain-less) = %q, want %q", exch2, "XNYS")
	}
}

func TestListInstruments_PaginationPastEnd(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// Create 3 instruments so we have a known small set (plus seeded CASH instruments).
	for i, name := range []string{"Alpha", "Beta", "Gamma"} {
		_, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", name, "", "", []db.IdentifierInput{
			{
				Ref:       db.InstrumentRef{Type: "ISIN", Value: "TEST" + string(rune('A'+i))},
				Canonical: true,
			}}, nil, "", nil, nil, nil)
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

func TestLookupOperatingMIC(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	// Operating MIC returns itself.
	mic, err := p.LookupOperatingMIC(ctx, "XNAS")
	if err != nil {
		t.Fatalf("lookup XNAS: %v", err)
	}
	if mic != "XNAS" {
		t.Fatalf("expected XNAS, got %s", mic)
	}

	// Segment MIC returns its operating MIC.
	// XNGS (NASDAQ/NGS Global Select Market) is a segment of XNAS.
	mic, err = p.LookupOperatingMIC(ctx, "XNGS")
	if err != nil {
		t.Fatalf("lookup XNGS: %v", err)
	}
	if mic != "XNAS" {
		t.Fatalf("expected XNAS for segment XNGS, got %s", mic)
	}

	// Unknown MIC returns error.
	_, err = p.LookupOperatingMIC(ctx, "ZZZZ")
	if err == nil {
		t.Fatal("expected error for unknown MIC")
	}
}

func TestSaveAndFindProviderIdentifiers(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	// Create an instrument to attach provider identifiers to.
	instID, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "AAPL", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			Canonical: true,
		}},
		nil,
		"", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	// Save provider identifiers.
	err = p.SaveProviderIdentifiers(ctx, instID, []db.ProviderIdentifierInput{
		{Provider: "massive", Type: "SEGMENT_MIC_TICKER", Domain: "XNGS", Value: "AAPL"},
		{Provider: "eodhd", Type: "EODHD_EXCH_CODE", Value: "US"},
		{Provider: "openfigi", Type: "FIGI", Value: "BBG000B9XRY4"},
	})
	if err != nil {
		t.Fatalf("save provider identifiers: %v", err)
	}

	// Find by provider.
	ids, err := p.FindProviderIdentifiers(ctx, instID, "massive")
	if err != nil {
		t.Fatalf("find massive: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 massive identifier, got %d", len(ids))
	}
	if ids[0].Type != "SEGMENT_MIC_TICKER" || ids[0].Domain != "XNGS" || ids[0].Value != "AAPL" {
		t.Fatalf("unexpected massive identifier: %+v", ids[0])
	}

	ids, err = p.FindProviderIdentifiers(ctx, instID, "eodhd")
	if err != nil {
		t.Fatalf("find eodhd: %v", err)
	}
	if len(ids) != 1 || ids[0].Type != "EODHD_EXCH_CODE" || ids[0].Value != "US" {
		t.Fatalf("unexpected eodhd identifiers: %+v", ids)
	}

	// Duplicate insert is idempotent.
	err = p.SaveProviderIdentifiers(ctx, instID, []db.ProviderIdentifierInput{
		{Provider: "massive", Type: "SEGMENT_MIC_TICKER", Domain: "XNGS", Value: "AAPL"},
	})
	if err != nil {
		t.Fatalf("duplicate save: %v", err)
	}
	ids, err = p.FindProviderIdentifiers(ctx, instID, "massive")
	if err != nil {
		t.Fatalf("find after dup: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 after dup, got %d", len(ids))
	}

	// Provider identifiers are loaded by GetInstrument.
	inst, err := p.GetInstrument(ctx, instID)
	if err != nil {
		t.Fatalf("get instrument: %v", err)
	}
	if len(inst.ProviderIdentifiers) != 3 {
		t.Fatalf("expected 3 provider identifiers on instrument, got %d", len(inst.ProviderIdentifiers))
	}

	// Unknown provider returns empty.
	ids, err = p.FindProviderIdentifiers(ctx, instID, "unknown")
	if err != nil {
		t.Fatalf("find unknown: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected 0 for unknown provider, got %d", len(ids))
	}
}

func TestEnsureInstrument_NormalizesSegmentMIC(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	// Create instrument with segment MIC XNGS (segment of XNAS).
	instID, err := p.EnsureInstrument(ctx, "STOCK", "XNGS", "USD", "AAPL", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNGS"},
			Canonical: true,
		}},
		nil,
		"", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	// Verify exchangeMIC was normalized to operating MIC.
	inst, err := p.GetInstrument(ctx, instID)
	if err != nil {
		t.Fatalf("get instrument: %v", err)
	}
	if inst.ExchangeMIC == nil || *inst.ExchangeMIC != "XNAS" {
		t.Fatalf("expected exchangeMIC XNAS, got %v", inst.ExchangeMIC)
	}

	// Verify MIC_TICKER domain was normalized to operating MIC.
	var found bool
	for _, id := range inst.Identifiers {
		if id.Ref.Type == "MIC_TICKER" {
			if id.Ref.Domain != "XNAS" {
				t.Fatalf("expected MIC_TICKER domain XNAS, got %s", id.Ref.Domain)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("MIC_TICKER identifier not found")
	}

	// Ensure same instrument is found when looking up with segment MIC.
	// The domain was normalized, so direct lookup with segment MIC should NOT match.
	id, err := p.FindInstrumentByIdentifier(ctx, "MIC_TICKER", "XNGS", "AAPL")
	if err != nil {
		t.Fatalf("find by segment MIC: %v", err)
	}
	if id != "" {
		t.Fatal("expected no match for segment MIC domain (was normalized)")
	}

	// But lookup with operating MIC should find it.
	id, err = p.FindInstrumentByIdentifier(ctx, "MIC_TICKER", "XNAS", "AAPL")
	if err != nil {
		t.Fatalf("find by operating MIC: %v", err)
	}
	if id != instID {
		t.Fatalf("expected %s, got %s", instID, id)
	}
}

func TestInsertInstrumentIdentifier_NormalizesSegmentMIC(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	// Create instrument with operating MIC.
	instID, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "AAPL", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0378331005"},
			Canonical: true,
		}},
		nil,
		"", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	// Insert MIC_TICKER with segment MIC.
	err = p.InsertInstrumentIdentifier(ctx, instID, db.IdentifierInput{
		Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNGS"},
		Canonical: true,
	})
	if err != nil {
		t.Fatalf("insert identifier: %v", err)
	}

	// Verify domain was normalized.
	inst, err := p.GetInstrument(ctx, instID)
	if err != nil {
		t.Fatalf("get instrument: %v", err)
	}
	for _, id := range inst.Identifiers {
		if id.Ref.Type == "MIC_TICKER" && id.Ref.Domain != "XNAS" {
			t.Fatalf("expected MIC_TICKER domain XNAS, got %s", id.Ref.Domain)
		}
	}
}

// TestMergeInstruments_RewritesWeightCommodity verifies the one thing a merge has to
// do to keep the balance invariant: a posting weighing in its own security names that
// commodity by instrument, so leaving the name behind would split one commodity into
// two and unbalance a group that was balanced when it was written. Both legs of an
// unpaired securities journal move together, so the group stays balanced.
func TestMergeInstruments_RewritesWeightCommodity(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	idA, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "ISIN", Value: "W1"},
		Canonical: true,
	}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure A: %v", err)
	}
	idB, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "CUSIP", Value: "W1"},
		Canonical: true,
	}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure B: %v", err)
	}
	userID, _ := p.GetOrCreateUser(ctx, "sub|weight-merge", "U", "u@wm.com")
	now := time.Now()
	from, to := timestamppb.New(now.Add(-time.Hour)), timestamppb.New(now.Add(time.Hour))

	// A journal leg against A and its clearing counterparty against B: two names for
	// what the merge is about to decide is one commodity.
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(now),
			TradeDate: timestamppb.New(now), InstrumentDescription: "StockA", BrokerTxType: []typev1.TxType{typev1.TxType_TRANSFER}, ResolvedTxType: typev1.TxType_TRANSFER, Quantity: "-10"},
		{OrderDate: timestamppb.New(now),
			TradeDate: timestamppb.New(now), InstrumentDescription: "StockB", BrokerTxType: []typev1.TxType{typev1.TxType_TRANSFER}, ResolvedTxType: typev1.TxType_TRANSFER, Quantity: "10"},
	}
	ws := []db.Weight{
		{Amount: decf(-10), Commodity: "inst:" + idA},
		{Amount: decf(10), Commodity: "inst:" + idB},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{idA, idB}, ws, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	survivor, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "W1"},
			Canonical: true,
		},
		{
			Ref:       db.InstrumentRef{Type: "CUSIP", Value: "W1"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	rows, err := p.q.QueryContext(ctx, `SELECT weight_commodity FROM txs WHERE user_id = $1`, userID)
	if err != nil {
		t.Fatalf("read commodities: %v", err)
	}
	defer rows.Close()
	want := "inst:" + survivor
	n := 0
	for rows.Next() {
		var got string
		if err := rows.Scan(&got); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if got != want {
			t.Errorf("weight_commodity = %q, want %q (the survivor)", got, want)
		}
		n++
	}
	// Four, not two. Until the merge the two legs are in different commodities, so
	// the group does not balance and the store routes a counterparty for each --
	// which is the state the merge exists to resolve, and the routed legs are
	// rewritten with the rest.
	if n != 4 {
		t.Fatalf("expected 4 postings, got %d", n)
	}
}

func TestListInstrumentsForExport_CarriesWhatAFileNeeds(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	validFrom := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	underlyingID, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Apple Inc.", "0000320193", "3571",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", &validFrom, nil, nil)
	if err != nil {
		t.Fatalf("ensure underlying: %v", err)
	}
	expiry := time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)
	optionID, err := p.EnsureInstrument(ctx, "OPTION", "", "USD", "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "OCC", Value: "AAPL  260116C00150500"},
			Canonical: true,
		}}, nil, underlyingID, nil, nil,
		&db.OptionFields{Strike: decimal.RequireFromString("150.5"), Expiry: expiry, PutCall: "C"})
	if err != nil {
		t.Fatalf("ensure option: %v", err)
	}

	// The recorded output of the identifier lookups, which is what the archive
	// exists to avoid paying for twice.
	if err := p.SaveProviderIdentifiers(ctx, underlyingID, []db.ProviderIdentifierInput{
		{Provider: "eodhd", Type: "EODHD_EXCH_CODE", Value: "US"},
		{Provider: "openfigi", Type: "FIGI", Domain: "XNAS", Value: "BBG000B9XRY4"},
	}); err != nil {
		t.Fatalf("save provider identifiers: %v", err)
	}

	list, err := p.ListInstrumentsForExport(ctx, "", nil)
	if err != nil {
		t.Fatalf("ListInstrumentsForExport: %v", err)
	}
	byID := make(map[string]*db.InstrumentRow, len(list))
	for _, row := range list {
		byID[row.ID] = row
	}

	stock := byID[underlyingID]
	if stock == nil {
		t.Fatalf("underlying missing from export (%d rows)", len(list))
	}
	// The columns the hand-written JSON dropped, and which nothing recomputes.
	if stock.CIK == nil || *stock.CIK != "0000320193" || stock.SICCode == nil || *stock.SICCode != "3571" {
		t.Fatalf("cik/sic_code not selected: %+v", stock)
	}
	if stock.ValidFrom == nil || !stock.ValidFrom.Equal(validFrom) {
		t.Fatalf("valid_from not selected: %v", stock.ValidFrom)
	}
	if stock.Underlying != nil {
		t.Fatalf("a non-derivative names no underlying, got %+v", stock.Underlying)
	}
	if len(stock.ProviderIdentifiers) != 2 {
		t.Fatalf("provider identifiers not loaded for export: %+v", stock.ProviderIdentifiers)
	}

	opt := byID[optionID]
	if opt == nil {
		t.Fatalf("option missing from export")
	}
	if opt.Strike == nil || !opt.Strike.Equal(decimal.RequireFromString("150.5")) {
		t.Fatalf("strike not selected: %v", opt.Strike)
	}
	if opt.Expiry == nil || !opt.Expiry.Equal(expiry) || opt.PutCall == nil || *opt.PutCall != "C" {
		t.Fatalf("expiry/put_call not selected: %+v", opt)
	}
	if !opt.ContractMultiplier.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("contract_multiplier = %v, want the column default", opt.ContractMultiplier)
	}
	// The underlying is named by its highest-priority identifier, because a file
	// cannot name it by UUID.
	wantUnderlying := db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"}
	if opt.Underlying == nil || *opt.Underlying != wantUnderlying {
		t.Fatalf("underlying = %+v, want %+v", opt.Underlying, wantUnderlying)
	}
}

func TestListInstrumentsForExport_PullsInUnderlyingOutsideTheFilter(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	underlyingID, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Apple Inc.", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure underlying: %v", err)
	}
	optionID, err := p.EnsureInstrument(ctx, "OPTION", "", "USD", "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "OCC", Value: "AAPL  260116C00150500"},
			Canonical: true,
		}}, nil, underlyingID, nil, nil,
		&db.OptionFields{Strike: decimal.RequireFromString("150.5"), Expiry: time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC), PutCall: "C"})
	if err != nil {
		t.Fatalf("ensure option: %v", err)
	}
	// An archive naming an underlying it does not carry is invalid, so a filter
	// of {OPTION} still has to yield the STOCK the option points at.
	list, err := p.ListInstrumentsForExport(ctx, "", []string{"OPTION"})
	if err != nil {
		t.Fatalf("ListInstrumentsForExport: %v", err)
	}
	var gotOption, gotUnderlying bool
	for _, row := range list {
		switch row.ID {
		case optionID:
			gotOption = true
		case underlyingID:
			gotUnderlying = true
		}
	}
	if !gotOption || !gotUnderlying {
		t.Fatalf("expected both the option and its underlying, got option=%v underlying=%v (%d rows)", gotOption, gotUnderlying, len(list))
	}
}

// TestListInstrumentsForExport_UnfilteredMeansEverything covers what a rebuild
// needs rather than what browsing wants. An FX pair is an instrument, and an
// instrument still waiting for an asset class -- one a price import created
// before identification reached it -- is exactly the row nothing else could
// reconstruct. A stated filter still selects a subset.
func TestListInstrumentsForExport_UnfilteredMeansEverything(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	unclassifiedID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "NOCLASS", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure unclassified: %v", err)
	}
	stockID, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure stock: %v", err)
	}
	// Seeded by migration 002 rather than created here: the FX pairs a rebuild
	// has to carry are the ones already in the instance.
	fxID, err := p.FindInstrumentByIdentifier(ctx, "FX_PAIR", "", "EURUSD")
	if err != nil || fxID == "" {
		t.Fatalf("find seeded EURUSD: id=%q err=%v", fxID, err)
	}
	cashID, err := p.FindInstrumentByIdentifier(ctx, "CURRENCY", "", "USD")
	if err != nil || cashID == "" {
		t.Fatalf("find seeded USD: id=%q err=%v", cashID, err)
	}

	unfiltered, err := p.ListInstrumentsForExport(ctx, "", nil)
	if err != nil {
		t.Fatalf("ListInstrumentsForExport: %v", err)
	}
	for _, want := range []string{unclassifiedID, stockID, fxID, cashID} {
		if !containsInstrument(unfiltered, want) {
			t.Errorf("unfiltered export dropped %s", want)
		}
	}

	// A stated filter still narrows, and pulls in nothing it did not ask for.
	filtered, err := p.ListInstrumentsForExport(ctx, "", []string{"STOCK"})
	if err != nil {
		t.Fatalf("ListInstrumentsForExport STOCK: %v", err)
	}
	if !containsInstrument(filtered, stockID) {
		t.Errorf("STOCK filter dropped the stock")
	}
	for _, unwanted := range []string{fxID, cashID, unclassifiedID} {
		if containsInstrument(filtered, unwanted) {
			t.Errorf("STOCK filter carried %s", unwanted)
		}
	}
}

func containsInstrument(rows []*db.InstrumentRow, id string) bool {
	for _, r := range rows {
		if r.ID == id {
			return true
		}
	}
	return false
}

// TestMergeInstrumentFromArchive_FillsGapsWithoutOverwriting covers the
// collision every rebuild hits: the instance already has the instrument, so the
// import must add what the file knows and change nothing the instance already
// knew.
func TestMergeInstrumentFromArchive_FillsGapsWithoutOverwriting(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	id, err := p.EnsureInstrument(ctx, "", "", "USD", "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	validFrom := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	err = p.MergeInstrumentFromArchive(ctx, id, db.InstrumentMerge{
		AssetClass:  "STOCK",
		ExchangeMIC: "XNAS",
		Currency:    "EUR", // already USD: the stored value wins
		CIK:         "0000320193",
		SICCode:     "3571",
		ValidFrom:   &validFrom,
		Identifiers: []db.IdentifierInput{
			{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"}, Canonical: true}, // already held
			{Ref: db.InstrumentRef{Type: "ISIN", Value: "US0378331005"}, Canonical: true},               // new
		},
	})
	if err != nil {
		t.Fatalf("MergeInstrumentFromArchive: %v", err)
	}

	row, err := p.GetInstrument(ctx, id)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	if row.AssetClass == nil || *row.AssetClass != "STOCK" {
		t.Errorf("asset_class = %v, want the file's STOCK filled into a NULL", row.AssetClass)
	}
	if row.ExchangeMIC == nil || *row.ExchangeMIC != "XNAS" {
		t.Errorf("exchange_mic = %v", row.ExchangeMIC)
	}
	if row.CIK == nil || *row.CIK != "0000320193" || row.SICCode == nil || *row.SICCode != "3571" {
		t.Errorf("cik/sic_code = %v/%v", row.CIK, row.SICCode)
	}
	if row.ValidFrom == nil || !row.ValidFrom.Equal(validFrom) {
		t.Errorf("valid_from = %v", row.ValidFrom)
	}
	// The one the instance already had: a file cannot rewrite it.
	if row.Currency == nil || *row.Currency != "USD" {
		t.Errorf("currency = %v, want the stored USD to survive the file's EUR", row.Currency)
	}
	if len(row.Identifiers) != 2 {
		t.Fatalf("expected the held identifier plus the new one, got %+v", row.Identifiers)
	}
}

// The collision a rebuild actually hits: migration 002 already seeded this row,
// so the import must leave its reference data alone while attaching what the
// lookups produced.
func TestMergeInstrumentFromArchive_LeavesSeededReferenceDataAlone(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	id, err := p.FindInstrumentByIdentifier(ctx, "CURRENCY", "", "USD")
	if err != nil || id == "" {
		t.Fatalf("find seeded USD: id=%q err=%v", id, err)
	}
	before, err := p.GetInstrument(ctx, id)
	if err != nil || before == nil {
		t.Fatalf("GetInstrument: %v", err)
	}

	if err := p.MergeInstrumentFromArchive(ctx, id, db.InstrumentMerge{
		AssetClass: "STOCK", // wrong on purpose
		Currency:   "EUR",   // wrong on purpose
		CIK:        "0000000001",
		Identifiers: []db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "CURRENCY", Value: "USD"},
			Canonical: true,
		}},
	}); err != nil {
		t.Fatalf("MergeInstrumentFromArchive: %v", err)
	}

	after, err := p.GetInstrument(ctx, id)
	if err != nil || after == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	if *after.AssetClass != *before.AssetClass || *after.Currency != *before.Currency {
		t.Fatalf("seeded reference data was rewritten: %v/%v -> %v/%v",
			*before.AssetClass, *before.Currency, *after.AssetClass, *after.Currency)
	}
	if after.Name == nil || before.Name == nil || *after.Name != *before.Name {
		t.Fatalf("seeded name changed: %v -> %v", before.Name, after.Name)
	}
	// A column the seed left empty is still fillable.
	if after.CIK == nil || *after.CIK != "0000000001" {
		t.Errorf("cik = %v, want the gap filled", after.CIK)
	}
	if len(after.Identifiers) != len(before.Identifiers) {
		t.Errorf("identifier count %d -> %d: an identifier already held was duplicated",
			len(before.Identifiers), len(after.Identifiers))
	}
}

// The lookup exists so an OCC root reaches the equity ticker it names. It has to
// match across the separator in both directions and stay silent when two
// instruments collapse to the same spelling, because at that point the root does
// not name one of them.
func TestFindInstrumentByTickerIgnoringSeparators(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	brk, err := p.EnsureInstrument(ctx, "STOCK", "XNYS", "USD", "Berkshire", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "BRK.B", Domain: "XNYS"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}

	// An OCC root finds the dotted ticker.
	got, err := p.FindInstrumentByTickerIgnoringSeparators(ctx, "BRKB")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got != brk {
		t.Errorf("BRKB -> %q, want %q", got, brk)
	}

	// And the reverse, so the caller need not know which spelling was stored.
	if got, err = p.FindInstrumentByTickerIgnoringSeparators(ctx, "BRK-B"); err != nil || got != brk {
		t.Errorf("BRK-B -> %q, %v; want %q", got, err, brk)
	}

	// A ticker with no separator at all still matches itself.
	aapl, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Apple", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure aapl: %v", err)
	}
	if got, err = p.FindInstrumentByTickerIgnoringSeparators(ctx, "AAPL"); err != nil || got != aapl {
		t.Errorf("AAPL -> %q, %v; want %q", got, err, aapl)
	}

	// Nothing that collapses to this spelling.
	if got, err = p.FindInstrumentByTickerIgnoringSeparators(ctx, "NOSUCH"); err != nil || got != "" {
		t.Errorf("NOSUCH -> %q, %v; want empty", got, err)
	}

	// Two instruments collapsing to one spelling is ambiguous, not a match.
	if _, err = p.EnsureInstrument(ctx, "STOCK", "XBUE", "ARS", "Berkshire CEDEAR", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "BRKB", Domain: "XBUE"},
			Canonical: true,
		}}, nil, "", nil, nil, nil); err != nil {
		t.Fatalf("ensure cedear: %v", err)
	}
	if got, err = p.FindInstrumentByTickerIgnoringSeparators(ctx, "BRK.B"); err != nil || got != "" {
		t.Errorf("ambiguous BRK.B -> %q, %v; want empty", got, err)
	}
}

// day is a midnight-UTC date bound, the form every identifier validity interval
// takes (see docs/adr/0018-half-open-date-intervals.md).
func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

// findIdentifier returns the identifier with the given type and value, or nil.
func findIdentifier(row *db.InstrumentRow, idType, value string) *db.IdentifierInput {
	for i := range row.Identifiers {
		if row.Identifiers[i].Ref.Type == idType && row.Identifiers[i].Ref.Value == value {
			return &row.Identifiers[i]
		}
	}
	return nil
}

// TestInstrumentIdentifiers_OverlapExcluded pins the constraint that replaced the
// global unique index. Two instruments may hold one value over disjoint
// intervals -- a 2:1 split makes one option's new OCC symbol another's old one --
// but never over overlapping ones.
func TestInstrumentIdentifiers_OverlapExcluded(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	idA, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:         db.InstrumentRef{Type: "OCC", Value: "XYZ250117C00100000"},
			Canonical:   true,
			ValidBefore: day(2024, 6, 10),
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure A: %v", err)
	}
	idB, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0000000001"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure B: %v", err)
	}

	// B takes the name up where A gave it up. The intervals abut, so they do not
	// overlap and both rows stand.
	if err := p.InsertInstrumentIdentifier(ctx, idB, db.IdentifierInput{
		Ref:       db.InstrumentRef{Type: "OCC", Value: "XYZ250117C00100000"},
		Canonical: true,
		ValidFrom: day(2024, 6, 10),
	}); err != nil {
		t.Fatalf("insert abutting identifier: %v", err)
	}

	// A third claim overlapping either of them is refused.
	err = p.InsertInstrumentIdentifier(ctx, idA, db.IdentifierInput{
		Ref:       db.InstrumentRef{Type: "OCC", Value: "XYZ250117C00100000"},
		Canonical: true,
		ValidFrom: day(2024, 8, 1),
	})
	if err == nil {
		t.Fatal("overlapping identifier accepted, want exclusion violation")
	}
	if !isIdentifierConflict(err) {
		t.Errorf("overlap error = %v, want an identifier conflict", err)
	}
}

// TestFindInstrumentByIdentifier_PrefersNameInForce covers what retained history
// does to a lookup by value: the name in force wins, and a value only ever held
// in the past still resolves, which is what lets a broker file exported before a
// split find the contract it names.
func TestFindInstrumentByIdentifier_PrefersNameInForce(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	// The two holders are built directly rather than through EnsureInstrument,
	// which resolves a value to whoever has ever held it and so would hand the
	// second one back the first. Telling them apart by date is issue 0122.
	past, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0000000003"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure past holder: %v", err)
	}
	if err := p.InsertInstrumentIdentifier(ctx, past, db.IdentifierInput{
		Ref:         db.InstrumentRef{Type: "MIC_TICKER", Value: "REUSED", Domain: "XNAS"},
		Canonical:   true,
		ValidBefore: day(2020, 1, 1),
	}); err != nil {
		t.Fatalf("insert closed ticker: %v", err)
	}

	// Only a closed row exists, so it is the answer.
	got, err := p.FindInstrumentByIdentifier(ctx, "MIC_TICKER", "XNAS", "REUSED")
	if err != nil {
		t.Fatalf("find with only a closed row: %v", err)
	}
	if got != past {
		t.Errorf("find with only a closed row = %q, want %q", got, past)
	}

	current, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0000000004"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure current holder: %v", err)
	}
	if err := p.InsertInstrumentIdentifier(ctx, current, db.IdentifierInput{
		Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "REUSED", Domain: "XNAS"},
		Canonical: true,
		ValidFrom: day(2020, 1, 1),
	}); err != nil {
		t.Fatalf("insert open ticker: %v", err)
	}

	got, err = p.FindInstrumentByIdentifier(ctx, "MIC_TICKER", "XNAS", "REUSED")
	if err != nil {
		t.Fatalf("find with both rows: %v", err)
	}
	if got != current {
		t.Errorf("find with both rows = %q, want the current holder %q", got, current)
	}

	gotID, _, _, _, err := p.FindInstrumentWithMetaByIdentifier(ctx, "MIC_TICKER", "XNAS", "REUSED")
	if err != nil {
		t.Fatalf("find with meta: %v", err)
	}
	if gotID != current {
		t.Errorf("find with meta = %q, want the current holder %q", gotID, current)
	}
}

// TestMergeInstruments_CarriesIdentifierHistory: a merge moves the loser's names
// to the survivor with their intervals intact. Dropping the bounds would re-open
// a name the loser had already given up, which the exclusion constraint would
// then refuse against whoever holds it now.
func TestMergeInstruments_CarriesIdentifierHistory(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	// Three identifiers against the loser's two, so the survivor is settled by the
	// first sort key. The rows are written in one transaction and `created_at`
	// defaults to now(), which is transaction time, so a tie on the count would
	// tie on the age as well and leave the winner to the id.
	survivor, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0000000002"},
			Canonical: true,
		},
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "KEEP", Domain: "XNAS"},
			Canonical: true,
		},
		{
			Ref:       db.InstrumentRef{Type: "SEDOL", Value: "0000002"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure survivor: %v", err)
	}
	loser, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "CUSIP", Value: "000000001"},
			Canonical: true,
		},
		{
			Ref:         db.InstrumentRef{Type: "OCC", Value: "OLD250117C00100000"},
			Canonical:   true,
			ValidBefore: day(2024, 6, 10),
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure loser: %v", err)
	}
	if survivor == loser {
		t.Fatal("survivor and loser should be different instruments")
	}

	// Naming both instruments at once merges them; the survivor is the row
	// holding more identifiers.
	got, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0000000002"},
			Canonical: true,
		},
		{
			Ref:       db.InstrumentRef{Type: "CUSIP", Value: "000000001"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("merging ensure: %v", err)
	}
	if got != survivor {
		t.Fatalf("merge survivor = %q, want %q", got, survivor)
	}

	row, err := p.GetInstrument(ctx, survivor)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	moved := findIdentifier(row, "OCC", "OLD250117C00100000")
	if moved == nil {
		t.Fatal("the loser's closed OCC did not move to the survivor")
	}
	if moved.ValidBefore == nil || !moved.ValidBefore.Equal(*day(2024, 6, 10)) {
		t.Errorf("moved identifier valid_before = %v, want 2024-06-10", moved.ValidBefore)
	}
}

// TestRecomputeInstrumentName_IgnoresClosedIdentifier: the derived name is what
// the instrument is called now. Without the validity filter the priority order
// would still reach a name the instrument has given up -- here a MIC_TICKER,
// which outranks everything.
func TestRecomputeInstrumentName_IgnoresClosedIdentifier(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	id, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:         db.InstrumentRef{Type: "MIC_TICKER", Value: "GONE", Domain: "XNAS"},
			Canonical:   true,
			ValidBefore: day(2024, 6, 10),
		},
		{
			Ref:       db.InstrumentRef{Type: "OCC", Value: "NOW250117C00050000"},
			Canonical: true,
			ValidFrom: day(2024, 6, 10),
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	row, err := p.GetInstrument(ctx, id)
	if err != nil || row == nil || row.Name == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	if *row.Name != "NOW250117C00050000" {
		t.Errorf("name = %q, want the name still in force %q", *row.Name, "NOW250117C00050000")
	}
}
