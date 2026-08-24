package postgres

import (
	"context"
	"github.com/google/uuid"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"
	"strings"
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
	idA, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "ISIN", Value: "1"},
		Canonical: true,
	}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure A: %v", err)
	}
	idB, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "CUSIP", Value: "1"},
		Canonical: true,
	}}, nil, "", nil)
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
	err = p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, resolvedFor(t, p, []string{idA, idB}), nil, nil)
	if err != nil {
		t.Fatalf("replace txs: %v", err)
	}
	// Resolve with identifiers that match both A and B; should merge and return survivor.
	brokerDesc := "SomeStock"
	result, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
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
		}}, nil, "", nil)
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
	brokerOnlyID, _, err := p.EnsureInstrument(ctx, "", "", "", "BrokerOnly", "", "", []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "BRK", Domain: "IBKR"},
		Canonical: false,
	}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure broker-only: %v", err)
	}
	// Instrument with canonical identifier - should be included.
	withCanonID, _, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Apple", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0378331005"},
			Canonical: true,
		},
		{
			Ref:       db.InstrumentRef{Type: "IBKR", Value: "AAPL"},
			Canonical: false,
		}}, nil, "", nil)
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
	_, _, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Nasdaq", "", "", []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "ISIN", Value: "N1"},
		Canonical: true,
	}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure XNAS: %v", err)
	}
	_, _, err = p.EnsureInstrument(ctx, "STOCK", "XNYS", "USD", "NYSE", "", "", []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "ISIN", Value: "Y1"},
		Canonical: true,
	}}, nil, "", nil)
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

func TestEnsureInstrument_WithUnderlying(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// Create underlying first (STOCK).
	underlyingID, _, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Apple Inc.", "0000320193", "3571", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0378331005"},
			Canonical: true,
		}}, nil, "", nil)
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
	// Create option with underlying_id (empty exchange -- SMART is not a MIC).
	optionID, _, err := p.EnsureInstrument(ctx, "OPTION", "", "USD", "AAPL Call", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "IBKR", Value: "AAPL 20250117C200"},
			Canonical: false,
		}}, nil, lineOf(t, p, underlyingID), &db.OptionFields{Strike: decf(230), Expiry: time.Date(2025, 12, 19, 0, 0, 0, 0, time.UTC), PutCall: "C"})
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
	// ListInstrumentsByIDs returns the option with same fields.
	rows, err := p.ListInstrumentsByIDs(ctx, []string{optionID})
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListInstrumentsByIDs: %v (len=%d)", err, len(rows))
	}
	if rows[0].UnderlyingID == nil || *rows[0].UnderlyingID != underlyingID {
		t.Errorf("ListInstrumentsByIDs row: UnderlyingID=%v", rows[0].UnderlyingID)
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
	id, _, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Intel", "", "", idns, nil, "", nil)
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
	again, _, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Intel", "", "", later, nil, "", nil)
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

// descriptionOnly is the instrument a first upload leaves behind when nothing
// identified the security: one non-canonical BROKER_DESCRIPTION and no other
// name, with every column still null.
func descriptionOnly(t *testing.T, p *Postgres, source, desc string) string {
	t.Helper()
	id, _, err := p.EnsureInstrument(context.Background(), "", "", "", desc, "", "", []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: desc, Domain: source},
		Canonical: false,
	}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure broker-description-only instrument: %v", err)
	}
	return id
}

// TestEnsureInstrument_CompletesADescriptionOnlyInstrument covers issue 0135's
// second half. An instrument holding nothing but a broker's text for a security
// has no identity, so the resolution that identifies it writes what it found on
// to it rather than finding it and dropping everything but the match.
func TestEnsureInstrument_CompletesADescriptionOnlyInstrument(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	const source = "IBKR:acct:statement"
	const desc = "VANGUARD FTSE ALL-WORLD UCITS ETF"
	id := descriptionOnly(t, p, source, desc)

	// The same description, now arriving with what the identifier plugins
	// resolved for it.
	again, _, err := p.EnsureInstrument(ctx, "STOCK", "XLON", "USD", "", "0000320193", "6770", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "IE00BK5BQT80"}, Canonical: true, ValidFrom: day(2025, 3, 1)},
		{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "VWRP", Domain: "XLON"}, Canonical: true, ValidFrom: day(2025, 3, 1)},
		{Ref: db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: desc, Domain: source}, Canonical: false},
	}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if again != id {
		t.Fatalf("ensure returned %q, want the existing instrument %q rather than a second one", again, id)
	}

	row, err := p.GetInstrument(ctx, id)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	if isin := findIdentifier(row, "ISIN", "IE00BK5BQT80"); isin == nil {
		t.Errorf("ISIN not written onto the instrument that had no identity")
	} else if isin.ValidFrom == nil || !isin.ValidFrom.Equal(*day(2025, 3, 1)) {
		t.Errorf("ISIN valid_from = %v, want the resolution's own vintage 2025-03-01", isin.ValidFrom)
	}
	// The ticker names a line, so it lands on the listing the stated currency
	// named rather than beside the ISIN on the security.
	if tkr, lst := findListingIdentifier(row, "MIC_TICKER", "VWRP"); tkr == nil {
		t.Errorf("MIC_TICKER not written onto the instrument that had no identity")
	} else if lst.Currency != "USD" {
		t.Errorf("MIC_TICKER landed on listing %v, want the USD line", lst.Currency)
	}
	if row.AssetClass == nil || *row.AssetClass != "STOCK" {
		t.Errorf("asset_class = %v, want STOCK", row.AssetClass)
	}
	if row.ExchangeMIC == nil || *row.ExchangeMIC != "XLON" {
		t.Errorf("exchange_mic = %v, want XLON", row.ExchangeMIC)
	}
	if row.Currency == nil || *row.Currency != "USD" {
		t.Errorf("currency = %v, want USD", row.Currency)
	}
	if row.CIK == nil || *row.CIK != "0000320193" {
		t.Errorf("cik = %v, want it filled", row.CIK)
	}
	if row.SICCode == nil || *row.SICCode != "6770" {
		t.Errorf("sic_code = %v, want it filled", row.SICCode)
	}
	// The name is trigger-derived from the identifiers in force, so the ticker
	// takes over from the broker description without either caller writing it.
	if row.Name == nil || *row.Name != "VWRP" {
		t.Errorf("name = %v, want the ticker to have displaced the broker description", row.Name)
	}
}

// TestEnsureInstrument_LeavesAnIdentifiedInstrumentAlone is the other side of the
// line 0135 draws. An instrument that already holds a canonical identifier has an
// identity, and what may be added to one is 0136's question, asked under a
// corroboration rule this path does not apply.
func TestEnsureInstrument_LeavesAnIdentifiedInstrumentAlone(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	id, _, err := p.EnsureInstrument(ctx, "STOCK", "", "", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "US0378331005"}, Canonical: true},
	}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}

	again, _, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "US0378331005"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"}, Canonical: true},
	}, nil, "", nil)
	if err != nil {
		t.Fatalf("re-ensure: %v", err)
	}
	if again != id {
		t.Fatalf("re-ensure returned %q, want %q", again, id)
	}

	row, err := p.GetInstrument(ctx, id)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	if findIdentifier(row, "MIC_TICKER", "AAPL") != nil {
		t.Errorf("ticker written onto an instrument that already had an identity; that is 0136's to decide")
	}
	if row.ExchangeMIC != nil {
		t.Errorf("exchange_mic = %v, want it left null", row.ExchangeMIC)
	}
	if row.Currency != nil {
		t.Errorf("currency = %v, want it left null", row.Currency)
	}
}

// TestEnsureInstrument_CompletionLeavesAStoredNameDateAlone extends the write
// discipline of TestEnsureInstrument_LeavesNameDatesAlone over the completion
// path. Inserting a name the instrument does not hold is not the same act as
// moving one it does: the first takes the resolution's own vintage (adr/0055),
// the second is what used to disarm the retroactive option-split guard.
func TestEnsureInstrument_CompletionLeavesAStoredNameDateAlone(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	const source = "IBKR:acct:statement"
	const desc = "CISCO SYSTEMS INC JAN25 60 CALL"
	id := descriptionOnly(t, p, source, desc)

	// The description-only instrument gains a name at the vintage the export
	// stated it as of.
	if _, _, err := p.EnsureInstrument(ctx, "", "", "USD", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "OCC", Value: "CSCO250117C00060000"}, Canonical: true, ValidFrom: day(2024, 1, 1)},
		{Ref: db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: desc, Domain: source}, Canonical: false},
	}, nil, "", nil); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// A later upload restating the same name must not move the bound, and the
	// instrument now holds an identity, so nothing else is written either.
	if _, _, err := p.EnsureInstrument(ctx, "", "", "USD", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "OCC", Value: "CSCO250117C00060000"}, Canonical: true, ValidFrom: day(2025, 6, 1)},
		{Ref: db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: desc, Domain: source}, Canonical: false},
	}, nil, "", nil); err != nil {
		t.Fatalf("re-ensure: %v", err)
	}

	row, err := p.GetInstrument(ctx, id)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	stored := findIdentifier(row, "OCC", "CSCO250117C00060000")
	if stored == nil || stored.ValidFrom == nil || !stored.ValidFrom.Equal(*day(2024, 1, 1)) {
		t.Errorf("valid_from moved on an incidental touch: got %v, want 2024-01-01", stored)
	}
	if n := len(row.Identifiers); n != 2 {
		t.Errorf("identifiers = %d, want the OCC restated rather than a second row", n)
	}
}

// TestFindDescriptionOnlyInstrument covers the lookup issue 0135 adds to the
// hinted path: it answers only for a description naming an instrument with no
// identity, and stops answering the moment that instrument has one.
func TestFindDescriptionOnlyInstrument(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	const source = "IBKR:acct:statement"
	const desc = "SOME UNLISTED FUND ACC"
	id := descriptionOnly(t, p, source, desc)

	got, err := p.FindDescriptionOnlyInstrument(ctx, source, desc)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got != id {
		t.Fatalf("find = %q, want %q", got, id)
	}

	// A description nobody has seen names nothing.
	got, err = p.FindDescriptionOnlyInstrument(ctx, source, "A DESCRIPTION NOBODY UPLOADED")
	if err != nil {
		t.Fatalf("find unknown: %v", err)
	}
	if got != "" {
		t.Errorf("find unknown = %q, want empty", got)
	}

	// The same description under another source is another mapping.
	got, err = p.FindDescriptionOnlyInstrument(ctx, "IBKR:acct:confirmation", desc)
	if err != nil {
		t.Fatalf("find other source: %v", err)
	}
	if got != "" {
		t.Errorf("find other source = %q, want empty", got)
	}

	// Once the instrument has an identity the description no longer answers for
	// it: what may be added to an identified instrument is 0136's question.
	if err := p.InsertInstrumentIdentifier(ctx, id, "", db.IdentifierInput{
		Ref: db.InstrumentRef{Type: "ISIN", Value: "IE00B3RBWM25"}, Canonical: true,
	}); err != nil {
		t.Fatalf("insert canonical identifier: %v", err)
	}
	got, err = p.FindDescriptionOnlyInstrument(ctx, source, desc)
	if err != nil {
		t.Fatalf("find after identification: %v", err)
	}
	if got != "" {
		t.Errorf("find after identification = %q, want empty", got)
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
	id, _, err := p.EnsureInstrument(ctx, "", "", "USD", "", "", "", idns, nil, "", nil)
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
	_, _, err := p.EnsureInstrument(ctx, "OPTION", "", "USD", "Option", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "IBKR", Value: "OPT1"},
			Canonical: false,
		}}, nil, "", nil)
	if err == nil {
		t.Fatal("expected error when OPTION names no underlying line")
	}
	if err.Error() != "underlying_listing_id required when asset_class is OPTION" {
		t.Errorf("got error: %v", err)
	}
}

func TestEnsureInstrument_InvalidAssetClass_Rejected(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, _, err := p.EnsureInstrument(ctx, "unknown", "XNAS", "USD", "X", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "IBKR", Value: "X"},
			Canonical: false,
		}}, nil, "", nil)
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
	nullID, _, err := p.EnsureInstrument(ctx, "", "", "", "NoClass", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "NOCLASS", Domain: "test"},
			Canonical: false,
		}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure null-class: %v", err)
	}
	// Create a STOCK instrument for comparison.
	stockID, _, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "StockCo", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US1234567890"},
			Canonical: true,
		}}, nil, "", nil)
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
	_, _, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "TestCo", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "TEST", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil)
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
	_, _, err = p.EnsureInstrument(ctx, "STOCK", "XNYS", "USD", "TestCo2", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US9999999999"},
			Canonical: true,
		}}, nil, "", nil)
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
		_, _, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", name, "", "", []db.IdentifierInput{
			{
				Ref:       db.InstrumentRef{Type: "ISIN", Value: "TEST" + string(rune('A'+i))},
				Canonical: true,
			}}, nil, "", nil)
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
	instID, listingID, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "AAPL", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			Canonical: true,
		}},
		nil,
		"", nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	// Save provider identifiers. All three name a listing, so they land on the
	// line the ensure minted and come back through the instrument all the same:
	// FindProviderIdentifiers asks what the security can be keyed on.
	err = p.SaveProviderIdentifiers(ctx, instID, listingID, []db.ProviderIdentifierInput{
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
	err = p.SaveProviderIdentifiers(ctx, instID, listingID, []db.ProviderIdentifierInput{
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
	// All three name a listing, so none is on the security itself and all three
	// come back through the flattening.
	if len(inst.ProviderIdentifiers) != 0 {
		t.Fatalf("expected no security-grain provider identifiers, got %d", len(inst.ProviderIdentifiers))
	}
	if got := len(inst.AllProviderIdentifiers()); got != 3 {
		t.Fatalf("expected 3 provider identifiers across the security and its lines, got %d", got)
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
	instID, _, err := p.EnsureInstrument(ctx, "STOCK", "XNGS", "USD", "AAPL", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNGS"},
			Canonical: true,
		}},
		nil,
		"", nil)
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
	tkr, lst := findListingIdentifier(inst, "MIC_TICKER", "AAPL")
	if tkr == nil {
		t.Fatal("MIC_TICKER identifier not found")
	}
	if tkr.Ref.Domain != "XNAS" {
		t.Fatalf("expected MIC_TICKER domain XNAS, got %s", tkr.Ref.Domain)
	}
	// Normalised before the venue set is derived from it, so the listing is
	// admitted to the operating MIC and not to the segment.
	if len(lst.Venues) != 1 || lst.Venues[0] != "XNAS" {
		t.Fatalf("listing venues = %v, want [XNAS]", lst.Venues)
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
	instID, listingID, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "AAPL", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0378331005"},
			Canonical: true,
		}},
		nil,
		"", nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	// Insert MIC_TICKER with segment MIC. A ticker names a line, so the listing
	// the ensure minted is the one it goes on.
	err = p.InsertInstrumentIdentifier(ctx, instID, listingID, db.IdentifierInput{
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
	idA, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "ISIN", Value: "W1"},
		Canonical: true,
	}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure A: %v", err)
	}
	idB, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "CUSIP", Value: "W1"},
		Canonical: true,
	}}, nil, "", nil)
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
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, resolvedFor(t, p, []string{idA, idB}), ws, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	survivor, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "W1"},
			Canonical: true,
		},
		{
			Ref:       db.InstrumentRef{Type: "CUSIP", Value: "W1"},
			Canonical: true,
		}}, nil, "", nil)
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
	underlyingID, underlyingListing, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Apple Inc.", "0000320193", "3571",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure underlying: %v", err)
	}
	expiry := time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)
	optionID, _, err := p.EnsureInstrument(ctx, "OPTION", "", "USD", "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "OCC", Value: "AAPL  260116C00150500"},
			Canonical: true,
		}}, nil, lineOf(t, p, underlyingID),
		&db.OptionFields{Strike: decimal.RequireFromString("150.5"), Expiry: expiry, PutCall: "C"})
	if err != nil {
		t.Fatalf("ensure option: %v", err)
	}

	// The recorded output of the identifier lookups, which is what the archive
	// exists to avoid paying for twice.
	if err := p.SaveProviderIdentifiers(ctx, underlyingID, underlyingListing, []db.ProviderIdentifierInput{
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
	if stock.Underlying != nil {
		t.Fatalf("a non-derivative names no underlying, got %+v", stock.Underlying)
	}
	// Both name a listing, so the export finds them through the security's lines
	// rather than on the security itself.
	if got := len(stock.AllProviderIdentifiers()); got != 2 {
		t.Fatalf("provider identifiers not loaded for export: %+v", stock.AllProviderIdentifiers())
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
	underlyingID, _, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Apple Inc.", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure underlying: %v", err)
	}
	optionID, _, err := p.EnsureInstrument(ctx, "OPTION", "", "USD", "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "OCC", Value: "AAPL  260116C00150500"},
			Canonical: true,
		}}, nil, lineOf(t, p, underlyingID),
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

	unclassifiedID, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "NOCLASS", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure unclassified: %v", err)
	}
	stockID, _, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil)
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
	id, _, err := p.EnsureInstrument(ctx, "", "", "USD", "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	err = p.MergeInstrumentFromArchive(ctx, id, db.InstrumentMerge{
		AssetClass:  "STOCK",
		ExchangeMIC: "XNAS",
		Currency:    "EUR", // already USD: the stored value wins
		CIK:         "0000320193",
		SICCode:     "3571",
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
	// The one the instance already had: a file cannot rewrite it.
	if row.Currency == nil || *row.Currency != "USD" {
		t.Errorf("currency = %v, want the stored USD to survive the file's EUR", row.Currency)
	}
	// The ISIN is the security's; the ticker is a line's. Between them that is
	// the held identifier plus the new one, filed where each belongs.
	if len(row.Identifiers) != 1 || row.Identifiers[0].Ref.Type != "ISIN" {
		t.Fatalf("expected the new ISIN on the security, got %+v", row.Identifiers)
	}
	if tkr, _ := findListingIdentifier(row, "MIC_TICKER", "AAPL"); tkr == nil {
		t.Fatalf("the held ticker is no longer on any line: %+v", row.Listings)
	}
	// The merge's own currency names one line. Its EUR is a line the instance did
	// not have, and gaining one is not the same as rewriting the security's own
	// currency column, which the check above pins. A file naming several lines at
	// once goes through EnsureArchiveInstrument instead.
	if len(row.Listings) != 2 {
		t.Fatalf("expected the stored USD line and the file's EUR line, got %+v", row.Listings)
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

	brk, _, err := p.EnsureInstrument(ctx, "STOCK", "XNYS", "USD", "Berkshire", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "BRK.B", Domain: "XNYS"},
			Canonical: true,
		}}, nil, "", nil)
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
	aapl, _, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "Apple", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil)
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
	if _, _, err = p.EnsureInstrument(ctx, "STOCK", "XBUE", "ARS", "Berkshire CEDEAR", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "BRKB", Domain: "XBUE"},
			Canonical: true,
		}}, nil, "", nil); err != nil {
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
// findIdentifier looks among the security's own names. A listing-grain type is
// deliberately not found here -- which line it names is the point -- so a test
// after a ticker asks findListingIdentifier.
func findIdentifier(row *db.InstrumentRow, idType, value string) *db.IdentifierInput {
	for i := range row.Identifiers {
		if row.Identifiers[i].Ref.Type == idType && row.Identifiers[i].Ref.Value == value {
			return &row.Identifiers[i]
		}
	}
	return nil
}

// findListingIdentifier looks among the names of every line of the security, and
// returns the line as well: a ticker that landed on the wrong listing is a
// different failure from one that was never written.
func findListingIdentifier(row *db.InstrumentRow, idType, value string) (*db.IdentifierInput, *db.Listing) {
	for _, l := range row.Listings {
		for i := range l.Identifiers {
			if l.Identifiers[i].Ref.Type == idType && l.Identifiers[i].Ref.Value == value {
				return &l.Identifiers[i], l
			}
		}
	}
	return nil, nil
}

// TestInstrumentIdentifiers_OverlapExcluded pins the constraint that replaced the
// global unique index. Two instruments may hold one value over disjoint
// intervals -- a 2:1 split makes one option's new OCC symbol another's old one --
// but never over overlapping ones.
func TestInstrumentIdentifiers_OverlapExcluded(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	idA, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:         db.InstrumentRef{Type: "OCC", Value: "XYZ250117C00100000"},
			Canonical:   true,
			ValidBefore: day(2024, 6, 10),
		}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure A: %v", err)
	}
	idB, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0000000001"},
			Canonical: true,
		}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure B: %v", err)
	}

	// B takes the name up where A gave it up. The intervals abut, so they do not
	// overlap and both rows stand.
	if err := p.InsertInstrumentIdentifier(ctx, idB, "", db.IdentifierInput{
		Ref:       db.InstrumentRef{Type: "OCC", Value: "XYZ250117C00100000"},
		Canonical: true,
		ValidFrom: day(2024, 6, 10),
	}); err != nil {
		t.Fatalf("insert abutting identifier: %v", err)
	}

	// A third claim overlapping either of them is refused.
	err = p.InsertInstrumentIdentifier(ctx, idA, "", db.IdentifierInput{
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
	past, pastListing, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0000000003"},
			Canonical: true,
		}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure past holder: %v", err)
	}
	if err := p.InsertInstrumentIdentifier(ctx, past, pastListing, db.IdentifierInput{
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

	current, currentListing, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0000000004"},
			Canonical: true,
		}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure current holder: %v", err)
	}
	if err := p.InsertInstrumentIdentifier(ctx, current, currentListing, db.IdentifierInput{
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
	survivor, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
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
		}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure survivor: %v", err)
	}
	loser, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "CUSIP", Value: "000000001"},
			Canonical: true,
		},
		{
			Ref:         db.InstrumentRef{Type: "OCC", Value: "OLD250117C00100000"},
			Canonical:   true,
			ValidBefore: day(2024, 6, 10),
		}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure loser: %v", err)
	}
	if survivor == loser {
		t.Fatal("survivor and loser should be different instruments")
	}

	// Naming both instruments at once merges them; the survivor is the row
	// holding more identifiers.
	got, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0000000002"},
			Canonical: true,
		},
		{
			Ref:       db.InstrumentRef{Type: "CUSIP", Value: "000000001"},
			Canonical: true,
		}}, nil, "", nil)
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

	id, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{
			Ref:         db.InstrumentRef{Type: "MIC_TICKER", Value: "GONE", Domain: "XNAS"},
			Canonical:   true,
			ValidBefore: day(2024, 6, 10),
		},
		{
			Ref:       db.InstrumentRef{Type: "OCC", Value: "NOW250117C00050000"},
			Canonical: true,
			ValidFrom: day(2024, 6, 10),
		}}, nil, "", nil)
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

// A merge moves its loser's postings onto the survivor's line of the same currency
// family. The loser's listings cascade away with it, so leaving a posting pointing
// at one would either fail the foreign key or, worse, leave the ledger naming a
// line of a security that no longer exists.
func TestMergeInstruments_PostingsMoveToTheSurvivorsLine(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|merge-line", "U", "u@merge-line.com")

	survivor, survivorLine, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "S", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "US0000000SS1"}, Canonical: true}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure survivor: %v", err)
	}
	loser, loserLine, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "L", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "US0000000LL2"}, Canonical: true}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure loser: %v", err)
	}
	if survivorLine == "" || loserLine == "" || survivorLine == loserLine {
		t.Fatalf("fixture wants two distinct lines, got %q and %q", survivorLine, loserLine)
	}

	from := timestamppb.New(time.Now().Add(-time.Hour))
	to := timestamppb.New(time.Now().Add(time.Hour))
	txs := []*apiv1.Tx{{
		OrderDate: timestamppb.New(time.Now()), TradeDate: timestamppb.New(time.Now()),
		InstrumentDescription: "L", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET},
		ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "3", Account: "ACC",
	}}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs,
		[]db.Resolution{{InstrumentID: loser, ListingID: loserLine}}, nil, nil); err != nil {
		t.Fatalf("seed posting on the loser's line: %v", err)
	}

	if err := p.runInTx(ctx, func(exec queryable) error {
		return mergeInstruments(ctx, exec, uuid.MustParse(survivor), uuid.MustParse(loser))
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}

	var gotInst, gotLine string
	if err := p.q.QueryRowContext(ctx, `
		SELECT instrument_id::text, listing_id::text FROM txs WHERE instrument_description = 'L'
	`).Scan(&gotInst, &gotLine); err != nil {
		t.Fatalf("read posting: %v", err)
	}
	if gotInst != survivor {
		t.Errorf("posting names security %s, want the survivor %s", gotInst, survivor)
	}
	if gotLine != survivorLine {
		t.Errorf("posting names line %s, want the survivor's USD line %s", gotLine, survivorLine)
	}
}

// A posting that named no line still names none afterwards, and follows its
// security across. Under a sentinel model there was nowhere for such a posting to
// go; the null column is what makes "this security, line not known" survive a
// merge. See docs/adr/0072-a-posting-names-a-security-and-a-line.md.
func TestMergeInstruments_PostingOnNoLineStaysOnNone(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|merge-line-none", "U", "u@merge-line-none.com")

	survivor, _, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "S", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "US0000000SS1"}, Canonical: true}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure survivor: %v", err)
	}
	if _, err := p.EnsureListing(ctx, survivor, "GBP"); err != nil {
		t.Fatalf("second survivor line: %v", err)
	}
	// Nothing stated a currency for the loser, so it has no line and the posting
	// below is on none.
	loser, loserLine, err := p.EnsureInstrument(ctx, "STOCK", "", "", "L", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "US0000000LL2"}, Canonical: true}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure loser: %v", err)
	}
	if loserLine != "" {
		t.Fatalf("loser has line %s, want none: nothing stated a currency", loserLine)
	}

	from := timestamppb.New(time.Now().Add(-time.Hour))
	to := timestamppb.New(time.Now().Add(time.Hour))
	txs := []*apiv1.Tx{{
		OrderDate: timestamppb.New(time.Now()), TradeDate: timestamppb.New(time.Now()),
		InstrumentDescription: "L", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET},
		ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "3", Account: "ACC",
	}}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs,
		[]db.Resolution{{InstrumentID: loser}}, nil, nil); err != nil {
		t.Fatalf("seed posting on no line: %v", err)
	}

	if err := p.runInTx(ctx, func(exec queryable) error {
		return mergeInstruments(ctx, exec, uuid.MustParse(survivor), uuid.MustParse(loser))
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}

	var gotInst string
	var gotLine *string
	if err := p.q.QueryRowContext(ctx, `
		SELECT instrument_id::text, listing_id::text FROM txs WHERE instrument_description = 'L'
	`).Scan(&gotInst, &gotLine); err != nil {
		t.Fatalf("read posting: %v", err)
	}
	if gotInst != survivor {
		t.Errorf("posting names security %s, want the survivor %s", gotInst, survivor)
	}
	if gotLine != nil {
		t.Errorf("posting names line %s, want none: nothing said which of the survivor's lines it is on", *gotLine)
	}
}

// A contract's strike is a price and a price is in a currency, so the underlying
// it delivers is a line and not the security. Every line has a currency, so what
// is left to refuse is a contract naming a line that is not there -- a security
// nobody has named a line for cannot underwrite one. See
// docs/adr/0074-an-options-underlying-is-the-line-its-strike-is-quoted-in.md.
func TestEnsureInstrument_OptionOnALineThatIsNotThere_Rejected(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	underlyingID, line, err := p.EnsureInstrument(ctx, "STOCK", "", "", "U", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "US0000000UN1"}, Canonical: true}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure underlying: %v", err)
	}
	if line != "" {
		t.Fatalf("underlying has line %s, want none: nothing stated a currency", line)
	}
	_ = underlyingID

	_, _, err = p.EnsureInstrument(ctx, "OPTION", "", "USD", "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "OCC", Value: "UNKN  260116C00150500"},
			Canonical: true,
		}}, nil, uuid.New().String(),
		&db.OptionFields{Strike: decimal.RequireFromString("150.5"), Expiry: time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC), PutCall: "C"})
	if err == nil {
		t.Fatal("expected the option to be refused: it names a line that does not exist")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unexpected error: %v", err)
	}
}

// The underlying of an option is one line of a dual-listed security, and which
// one is decided by the currency the contract is struck in.
func TestEnsureInstrument_OptionNamesTheLineItsStrikeIsQuotedIn(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	underlyingID, usdLine, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "DUAL", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "US0000000DU1"}, Canonical: true}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure underlying: %v", err)
	}
	eurLine, err := p.EnsureListing(ctx, underlyingID, "EUR")
	if err != nil {
		t.Fatalf("ensure EUR line: %v", err)
	}

	optionID, _, err := p.EnsureInstrument(ctx, "OPTION", "", "EUR", "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "IBKR", Value: "DUAL 20260116C480"},
			Canonical: true,
		}}, nil, eurLine,
		&db.OptionFields{Strike: decimal.RequireFromString("480"), Expiry: time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC), PutCall: "C"})
	if err != nil {
		t.Fatalf("ensure option: %v", err)
	}

	row, err := p.GetInstrument(ctx, optionID)
	if err != nil {
		t.Fatalf("get option: %v", err)
	}
	if row.UnderlyingListingID == nil || *row.UnderlyingListingID != eurLine {
		t.Errorf("underlying line: got %v, want the EUR line %s (not the USD one %s)",
			row.UnderlyingListingID, eurLine, usdLine)
	}
	// The security above that line is derived rather than stored, for the
	// callers that do not care which line the contract is written on.
	if row.UnderlyingID == nil || *row.UnderlyingID != underlyingID {
		t.Errorf("underlying security: got %v, want %s", row.UnderlyingID, underlyingID)
	}
}

// A split on the underlying security reaches an option written on any of its
// lines: split_factor_at climbs from the line to the security above it.
func TestSplitFactorAt_ReachesTheOptionThroughItsUnderlyingLine(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	underlyingID, _, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "SPLITME", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "US0000000SP1"}, Canonical: true}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure underlying: %v", err)
	}
	eurLine, err := p.EnsureListing(ctx, underlyingID, "EUR")
	if err != nil {
		t.Fatalf("ensure EUR line: %v", err)
	}
	optionID, _, err := p.EnsureInstrument(ctx, "OPTION", "", "EUR", "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "IBKR", Value: "SPLITME 20260116C100"},
			Canonical: true,
		}}, nil, eurLine,
		&db.OptionFields{Strike: decimal.RequireFromString("100"), Expiry: time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC), PutCall: "C"})
	if err != nil {
		t.Fatalf("ensure option: %v", err)
	}
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{{
		InstrumentID: underlyingID, ExDate: time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC),
		SplitFrom: "1", SplitTo: "4", DataProvider: "test",
	}}); err != nil {
		t.Fatalf("upsert split: %v", err)
	}

	var num, den string
	if err := p.q.QueryRowContext(ctx,
		`SELECT num::text, den::text FROM split_factor_at($1::uuid, $2::date)`,
		optionID, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)).Scan(&num, &den); err != nil {
		t.Fatalf("split_factor_at: %v", err)
	}
	if num != "4" || den != "1" {
		t.Errorf("factor: got %s/%s, want 4/1 -- the split on the security reaches the option through its line", num, den)
	}

	// InstrumentsWithSplits takes the same join.
	with, err := p.InstrumentsWithSplits(ctx, []string{optionID})
	if err != nil {
		t.Fatalf("instruments with splits: %v", err)
	}
	if len(with) != 1 || with[0] != optionID {
		t.Errorf("instruments with splits: got %v, want the option", with)
	}
}

// TestIdentifierJoins_PickPerGrain is what the split is for. A dual-listed
// security carries one ISIN and a ticker on each of its lines: the security join
// answers with the ISIN, and answers the same way twice; the listing join answers
// with each line's own ticker and never with its sibling's.
//
// Before the split one order served both, ranking MIC_TICKER above ISIN, so a
// security-grain export was named by whichever of its lines the planner happened
// to return -- and a price group could be named by the GBP line's ticker and
// stated to be in USD.
// See docs/adr/0069-a-listing-is-named-by-a-security-identifier-and-a-currency.md.
func TestIdentifierJoins_PickPerGrain(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID, gbp, err := p.EnsureInstrument(ctx, "STOCK", "", "GBP", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "IE00TWOLINES"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "ABCG", Domain: "XLON"}, Canonical: true},
	}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	usd, err := p.EnsureListing(ctx, instID, "USD")
	if err != nil {
		t.Fatalf("ensure usd listing: %v", err)
	}
	if _, err := p.q.ExecContext(ctx, `
		INSERT INTO instrument_listing_identifiers (instrument_id, listing_id, identifier_type, domain, value, canonical)
		VALUES ($1, $2, 'MIC_TICKER', 'XLON', 'ABCU', true)
	`, instID, usd); err != nil {
		t.Fatalf("insert usd ticker: %v", err)
	}

	// The security join. Read twice, because an unstable answer is the failure
	// mode that splits one security's rows across two groups in a file.
	securityRef := func() db.InstrumentRef {
		t.Helper()
		var ref db.InstrumentRef
		if err := p.q.QueryRowxContext(ctx, `
			SELECT best_id.identifier_type, best_id.value, COALESCE(best_id.domain, '') AS domain
			FROM instruments i
			`+bestSecurityIdentifierJoin+`
			WHERE i.id = $1
		`, instID).StructScan(&ref); err != nil {
			t.Fatalf("security join: %v", err)
		}
		return ref
	}
	first := securityRef()
	if first.Type != "ISIN" || first.Value != "IE00TWOLINES" {
		t.Fatalf("security join = %s %s %q, want the ISIN: a ticker names a line, not the security",
			first.Type, first.Value, first.Domain)
	}
	if second := securityRef(); second != first {
		t.Fatalf("security join returned %+v then %+v: the answer has to be stable across a security's listings", first, second)
	}

	// The listing join, once per line.
	for _, tc := range []struct {
		name    string
		listing string
		want    string
	}{
		{"the GBP line", gbp, "ABCG"},
		{"the USD line", usd, "ABCU"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ref db.InstrumentRef
			if err := p.q.QueryRowxContext(ctx, `
				SELECT best_id.identifier_type, best_id.value, COALESCE(best_id.domain, '') AS domain
				FROM instrument_listings l
				`+bestListingIdentifierJoinOn("JOIN", "l.id", "best_id")+`
				WHERE l.id = $1
			`, tc.listing).StructScan(&ref); err != nil {
				t.Fatalf("listing join: %v", err)
			}
			if ref.Type != "MIC_TICKER" || ref.Value != tc.want {
				t.Fatalf("listing join = %s %s, want MIC_TICKER %s -- a sibling line's ticker names the wrong line",
					ref.Type, ref.Value, tc.want)
			}
		})
	}

	// A line with no ticker of its own falls back to the security's ISIN, which is
	// a name only because the caller carries the currency alongside it.
	eur, err := p.EnsureListing(ctx, instID, "EUR")
	if err != nil {
		t.Fatalf("ensure eur listing: %v", err)
	}
	var ref db.InstrumentRef
	if err := p.q.QueryRowxContext(ctx, `
		SELECT best_id.identifier_type, best_id.value, COALESCE(best_id.domain, '') AS domain
		FROM instrument_listings l
		`+bestListingIdentifierJoinOn("JOIN", "l.id", "best_id")+`
		WHERE l.id = $1
	`, eur).StructScan(&ref); err != nil {
		t.Fatalf("listing join on the EUR line: %v", err)
	}
	if ref.Type != "ISIN" {
		t.Fatalf("listing join on a line with no ticker = %s %s, want the security's ISIN", ref.Type, ref.Value)
	}
}
