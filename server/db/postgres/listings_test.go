package postgres

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/leedenison/portfoliodb/server/currency"
	"github.com/leedenison/portfoliodb/server/db"
)

// The SQL currency_family restates currency.MinorUnits, because an index
// expression must be IMMUTABLE and so cannot read the Go table. This is what
// holds the two in step. It fails in both directions: a Go entry the SQL lacks
// shows up in the first loop, and an SQL entry Go lacks shows up in the second,
// which walks every currency the seed migration knows about.
func TestCurrencyFamily_matchesGoTable(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	minor := make(map[string]string, len(currency.MinorUnits))
	for _, u := range currency.MinorUnits {
		minor[u.Code] = u.Major
		var got string
		if err := p.q.QueryRowContext(ctx, `SELECT currency_family($1)`, u.Code).Scan(&got); err != nil {
			t.Fatalf("currency_family(%q): %v", u.Code, err)
		}
		if got != u.Major {
			t.Errorf("currency_family(%q) = %q, want %q (SQL is behind server/currency)", u.Code, got, u.Major)
		}
	}

	rows, err := p.q.QueryContext(ctx, `
		SELECT DISTINCT currency, currency_family(currency)
		FROM instruments WHERE asset_class = 'CASH' AND currency IS NOT NULL
	`)
	if err != nil {
		t.Fatalf("read seeded currencies: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var code, family string
		if err := rows.Scan(&code, &family); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen++
		want, isMinor := minor[code]
		if !isMinor {
			want = code
		}
		if family != want {
			t.Errorf("currency_family(%q) = %q, want %q (SQL claims a family server/currency does not)", code, family, want)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if seen == 0 {
		t.Fatal("no seeded CASH currencies found (migration 002 may not have run)")
	}
}

// Two lines of one security are required to hold different currencies, and GBX
// and GBP are one currency under a different prefix rather than two.
func TestInstrumentListings_OneListingPerCurrencyFamily(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := ensureListedInstrument(t, p, "ISIN", "GB00LISTING01", "GBX")

	if err := insertListing(ctx, p, instID, "GBP"); err == nil {
		t.Error("inserting a GBP listing beside a GBX one succeeded, want a uniqueness violation")
	} else if !isUniqueViolation(err) {
		t.Errorf("insert GBP beside GBX: %v, want a uniqueness violation", err)
	}
}

// The case the index exists to allow: two currencies that are not one currency
// under a different prefix are two lines, and holdings on them are not fungible.
// Separate from the rejection above because the harness runs a test in one
// transaction, and a constraint violation aborts it.
func TestInstrumentListings_TwoFamiliesAreTwoListings(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := ensureListedInstrument(t, p, "ISIN", "GB00LISTING14", "GBX")

	if err := insertListing(ctx, p, instID, "USD"); err != nil {
		t.Fatalf("insert USD beside GBX: %v, want it allowed", err)
	}
	got := listingCurrencies(t, p, instID)
	if len(got) != 2 || got[0] != "GBX" || got[1] != "USD" {
		t.Errorf("listings = %v, want [GBX USD]", got)
	}
}

// A line is a currency, so a row without one is not a line and the column
// refuses it. What used to be said by a null-currency row -- how many lines this
// security has is unknown -- is said by having none. See
// docs/adr/0075-a-name-that-could-not-be-placed-names-no-line.md.
func TestInstrumentListings_ALineHasACurrency(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := ensureListedInstrument(t, p, "ISIN", "GB00LISTING02", "USD")

	if err := insertListing(ctx, p, instID, ""); err == nil {
		t.Error("inserting a listing with no currency succeeded, want a not-null violation")
	}
}

// A listing is a fact about a security and does not outlive it.
func TestInstrumentListings_CascadeOnInstrumentDelete(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := ensureListedInstrument(t, p, "ISIN", "GB00LISTING03", "USD")

	if _, err := p.q.ExecContext(ctx, `DELETE FROM instruments WHERE id = $1::uuid`, instID); err != nil {
		t.Fatalf("delete instrument: %v", err)
	}
	var n int
	if err := p.q.QueryRowContext(ctx, `SELECT count(*) FROM instrument_listings WHERE instrument_id = $1::uuid`, instID).Scan(&n); err != nil {
		t.Fatalf("count listings: %v", err)
	}
	if n != 0 {
		t.Errorf("%d listings survived the instrument, want 0", n)
	}
}

func TestEnsureInstrument_MintsAListing(t *testing.T) {
	p := testDBTx(t)
	instID := ensureListedInstrument(t, p, "ISIN", "GB00LISTING04", "GBX")

	got := listingCurrencies(t, p, instID)
	if len(got) != 1 || got[0] != "GBX" {
		t.Errorf("listings = %v, want [GBX]", got)
	}
}

// The code the line is quoted in is what is stored. The family governs the
// uniqueness index and never rewrites a code, so GBX does not arrive as GBP.
func TestEnsureInstrument_StoresTheCodeNotTheFamily(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := ensureListedInstrument(t, p, "ISIN", "GB00LISTING05", "GBX")

	var stored string
	if err := p.q.QueryRowContext(ctx, `SELECT currency FROM instrument_listings WHERE instrument_id = $1::uuid`, instID).Scan(&stored); err != nil {
		t.Fatalf("read listing currency: %v", err)
	}
	if stored != "GBX" {
		t.Errorf("stored currency = %q, want GBX", stored)
	}
}

// A caller that stated no currency has named no line, and none is minted for it.
// A security is quoted in a currency whether or not anyone traded it, so a line
// comes into existence when something asserts one and never to hold a silence.
func TestEnsureInstrument_MintsNoListingWithoutACurrency(t *testing.T) {
	p := testDBTx(t)
	instID := ensureListedInstrument(t, p, "ISIN", "GB00LISTING06", "")

	if got := listingCurrencies(t, p, instID); len(got) != 0 {
		t.Errorf("listings = %v, want none", got)
	}
}

// Resolving the same security twice must not accumulate listings.
func TestEnsureInstrument_IsIdempotentOnListings(t *testing.T) {
	p := testDBTx(t)
	instID := ensureListedInstrument(t, p, "ISIN", "GB00LISTING07", "USD")
	again := ensureListedInstrument(t, p, "ISIN", "GB00LISTING07", "USD")
	if again != instID {
		t.Fatalf("second EnsureInstrument returned %s, want %s", again, instID)
	}

	got := listingCurrencies(t, p, instID)
	if len(got) != 1 || got[0] != "USD" {
		t.Errorf("listings = %v, want [USD]", got)
	}
}

// A security identified without a currency has no line, and the currency a later
// resolution learns mints the one it names.
func TestEnsureInstrument_ALearnedCurrencyMintsTheLine(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// A broker-description-only instrument: no canonical identifier, no currency.
	instID, _, err := p.EnsureInstrument(ctx, "", "", "", "Some Security", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "SOME SECURITY", Domain: "test"}, Canonical: false},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure description-only: %v", err)
	}
	if got := listingCurrencies(t, p, instID); len(got) != 0 {
		t.Fatalf("listings before = %v, want none", got)
	}

	// Identification completes it, and the currency it learned names the line.
	same, _, err := p.EnsureInstrument(ctx, "STOCK", "", "GBX", "Some Security", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "SOME SECURITY", Domain: "test"}, Canonical: false},
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00LISTING08"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("complete description-only: %v", err)
	}
	if same != instID {
		t.Fatalf("completion returned %s, want %s", same, instID)
	}
	if got := listingCurrencies(t, p, instID); len(got) != 1 || got[0] != "GBX" {
		t.Errorf("listings after = %v, want [GBX]", got)
	}
}

// The same mint, through the archive import path.
func TestMergeInstrumentFromArchive_MintsTheLine(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := ensureListedInstrument(t, p, "ISIN", "GB00LISTING09", "")

	if err := p.MergeInstrumentFromArchive(ctx, instID, db.InstrumentMerge{
		AssetClass: "STOCK",
		Currency:   "USD",
	}); err != nil {
		t.Fatalf("merge from archive: %v", err)
	}
	if got := listingCurrencies(t, p, instID); len(got) != 1 || got[0] != "USD" {
		t.Errorf("listings = %v, want [USD]", got)
	}
}

// A file must not rewrite a line a security already trades on, in the same way
// it must not rewrite the columns: a stored value wins (adr/0004).
func TestMergeInstrumentFromArchive_LeavesAStoredListingAlone(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := ensureListedInstrument(t, p, "ISIN", "GB00LISTING10", "GBX")

	if err := p.MergeInstrumentFromArchive(ctx, instID, db.InstrumentMerge{Currency: "GBP"}); err != nil {
		t.Fatalf("merge from archive: %v", err)
	}
	if got := listingCurrencies(t, p, instID); len(got) != 1 || got[0] != "GBX" {
		t.Errorf("listings = %v, want [GBX]: GBP is the same family and must not fork the line", got)
	}
}

func TestListingsByInstrument(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	withCurrency := ensureListedInstrument(t, p, "ISIN", "GB00LISTING11", "USD")
	unknown := ensureListedInstrument(t, p, "ISIN", "GB00LISTING12", "")

	got, err := p.ListingsByInstrument(ctx, []string{withCurrency, unknown})
	if err != nil {
		t.Fatalf("listings by instrument: %v", err)
	}
	if len(got[withCurrency]) != 1 || got[withCurrency][0].Currency != "USD" {
		t.Errorf("listings for the USD security = %+v, want one USD listing", got[withCurrency])
	}
	if len(got[unknown]) != 0 {
		t.Errorf("listings for the security with no stated currency = %+v, want none", got[unknown])
	}
	if got[withCurrency][0].InstrumentID != withCurrency {
		t.Errorf("listing instrument id = %q, want %q", got[withCurrency][0].InstrumentID, withCurrency)
	}
}

// The read paths attach listings the way they attach identifiers, so anything
// reading an instrument sees its lines.
func TestGetInstrument_CarriesItsListings(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := ensureListedInstrument(t, p, "ISIN", "GB00LISTING13", "USD")

	row, err := p.GetInstrument(ctx, instID)
	if err != nil {
		t.Fatalf("get instrument: %v", err)
	}
	if len(row.Listings) != 1 || row.Listings[0].Currency != "USD" {
		t.Errorf("row.Listings = %+v, want one USD listing", row.Listings)
	}
}

// The invariant the rest of the listing work leans on. It is not a table
// constraint -- a deferrable constraint trigger would reject every raw insert in
// this suite for no benefit -- so it is asserted, and it covers the seeded cash
// and FX rows as well as anything a test created.
func TestEveryInstrumentHasAListing(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	rows, err := p.q.QueryContext(ctx, `
		SELECT i.id::text, coalesce(i.name, ''), coalesce(i.asset_class, '')
		FROM instruments i
		WHERE NOT EXISTS (SELECT 1 FROM instrument_listings l WHERE l.instrument_id = i.id)
	`)
	if err != nil {
		t.Fatalf("find instruments without listings: %v", err)
	}
	defer rows.Close()
	var missing []string
	for rows.Next() {
		var id, name, class string
		if err := rows.Scan(&id, &name, &class); err != nil {
			t.Fatalf("scan: %v", err)
		}
		missing = append(missing, id+" "+class+" "+name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(missing) > 0 {
		t.Errorf("%d instruments have no listing:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

// The seeded cash and FX rows are degenerate listings and have to be coherent
// ones: a cash instrument's line is its own currency, and an FX pair's is the
// currency it is quoted against, which is USD under adr/0006.
func TestSeedCurrencyInstruments_Listings(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	// GBX and GBP cash are different instruments, so the currency family never
	// sees the two together and each keeps its own code.
	for _, code := range []string{"USD", "GBP", "GBX"} {
		id, err := p.FindInstrumentByIdentifier(ctx, "CURRENCY", "", code)
		if err != nil || id == "" {
			t.Fatalf("find CURRENCY %s: %v (migration 002 may not have run)", code, err)
		}
		if got := listingCurrencies(t, p, id); len(got) != 1 || got[0] != code {
			t.Errorf("cash %s listings = %v, want [%s]", code, got, code)
		}
	}

	for _, pair := range []string{"GBPUSD", "GBXUSD"} {
		id, err := p.FindInstrumentByIdentifier(ctx, "FX_PAIR", "", pair)
		if err != nil || id == "" {
			t.Fatalf("find FX_PAIR %s: %v", pair, err)
		}
		if got := listingCurrencies(t, p, id); len(got) != 1 || got[0] != "USD" {
			t.Errorf("fx %s listings = %v, want [USD]", pair, got)
		}
	}
}

// ensureListedInstrument resolves an instrument by one canonical identifier,
// with currency "" meaning the source stated none.
func ensureListedInstrument(t *testing.T, p *Postgres, idType, value, currency string) string {
	t.Helper()
	id, _, err := p.EnsureInstrument(context.Background(), "STOCK", "", currency, "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: idType, Value: value}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument %s %s: %v", idType, value, err)
	}
	return id
}

// insertListing writes a listing directly, to exercise the constraints rather
// than the writer.
func insertListing(ctx context.Context, p *Postgres, instrumentID, currency string) error {
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO instrument_listings (instrument_id, currency) VALUES ($1::uuid, $2)
	`, instrumentID, nullStr(currency))
	return err
}

// listingCurrencies returns the instrument's listing currencies.
func listingCurrencies(t *testing.T, p *Postgres, instrumentID string) []string {
	t.Helper()
	got, err := p.ListingsByInstrument(context.Background(), []string{instrumentID})
	if err != nil {
		t.Fatalf("listings by instrument: %v", err)
	}
	out := make([]string, 0, len(got[instrumentID]))
	for _, l := range got[instrumentID] {
		out = append(out, l.Currency)
	}
	return out
}

// A row is stored against what its type names. This is the whole of 0147 in one
// assertion: the ISIN is a fact about the security and the ticker is a fact
// about one of its lines, and neither is found where the other lives.
func TestEnsureInstrument_StoresIdentifiersAtTheirGrain(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID, listingID, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "US00GRAIN001"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "GRN", Domain: "XNAS"}, Canonical: true},
		// Listing-grain without a domain, which is the case a rule reading the
		// domain instead of the declared grain would file in the wrong table.
		{Ref: db.InstrumentRef{Type: "SEDOL", Value: "BGRAIN1"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if listingID == "" {
		t.Fatal("EnsureInstrument named no listing for a stated currency")
	}

	row, err := p.GetInstrument(ctx, instID)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	if len(row.Identifiers) != 1 || row.Identifiers[0].Ref.Type != "ISIN" {
		t.Errorf("security identifiers = %+v, want just the ISIN", row.Identifiers)
	}
	if len(row.Listings) != 1 {
		t.Fatalf("listings = %+v, want one", row.Listings)
	}
	if row.Listings[0].ID != listingID {
		t.Errorf("listing id = %s, want the one EnsureInstrument returned (%s)", row.Listings[0].ID, listingID)
	}
	var types []string
	for _, idn := range row.Listings[0].Identifiers {
		types = append(types, idn.Ref.Type)
	}
	sort.Strings(types)
	if len(types) != 2 || types[0] != "MIC_TICKER" || types[1] != "SEDOL" {
		t.Errorf("listing identifiers = %v, want the ticker and the SEDOL", types)
	}
	// The flattening the callers that have not yet picked a grain read.
	if got := len(row.AllIdentifiers()); got != 3 {
		t.Errorf("AllIdentifiers = %d, want all three", got)
	}
}

// A ticker names one listing across the whole instance, exactly as an ISIN names
// one instrument. Two listings of one security holding it at once is the
// ambiguity the constraint refuses -- it is no less ambiguous for the two lines
// belonging to the same security.
func TestListingIdentifiers_OverlapExcludedAcrossListings(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID, gbp, err := p.EnsureInstrument(ctx, "STOCK", "", "GBP", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00OVERLAP1"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "DUP", Domain: "XLON"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	// A second line of the same security.
	if err := insertListing(ctx, p, instID, "USD"); err != nil {
		t.Fatalf("insert second listing: %v", err)
	}
	var usd string
	if err := p.q.QueryRowContext(ctx, `
		SELECT id::text FROM instrument_listings WHERE instrument_id = $1::uuid AND currency = 'USD'
	`, instID).Scan(&usd); err != nil {
		t.Fatalf("read USD listing: %v", err)
	}
	if usd == gbp {
		t.Fatal("the two lines are the same row")
	}
	err = p.InsertInstrumentIdentifier(ctx, instID, usd, db.IdentifierInput{
		Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "DUP", Domain: "XLON"}, Canonical: true,
	})
	if err == nil {
		t.Fatal("one ticker accepted on two lines at once, want exclusion violation")
	}
	if !isIdentifierConflict(err) {
		t.Fatalf("error = %v, want an exclusion violation", err)
	}
}

// A listing's venues are derived from its MIC_TICKER identifiers, and the whole
// set is recomputed: closing a name takes its venue away as surely as writing
// one adds it.
func TestListingVenues_DerivedFromTickers(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID, listingID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "VEN", Domain: "XNAS"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got := listingVenues(t, p, instID); len(got) != 1 || got[0] != "XNAS" {
		t.Fatalf("venues = %v, want [XNAS]", got)
	}

	// A second venue quoting the same line is a second member of the set rather
	// than a second listing.
	if err := p.InsertInstrumentIdentifier(ctx, instID, listingID, db.IdentifierInput{
		Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "VEN", Domain: "XLON"}, Canonical: true,
	}); err != nil {
		t.Fatalf("insert second venue: %v", err)
	}
	if got := listingVenues(t, p, instID); len(got) != 2 || got[0] != "XLON" || got[1] != "XNAS" {
		t.Fatalf("venues = %v, want [XLON XNAS]", got)
	}

	// A name no longer in force names no venue.
	if _, err := p.q.ExecContext(ctx, `
		UPDATE instrument_listing_identifiers SET valid_before = DATE '2020-01-01' WHERE domain = 'XLON'
	`); err != nil {
		t.Fatalf("close the XLON name: %v", err)
	}
	if got := listingVenues(t, p, instID); len(got) != 1 || got[0] != "XNAS" {
		t.Fatalf("venues after closing = %v, want [XNAS]", got)
	}
}

// A composite names a market rather than a venue, so a ticker under one records
// no MIC. The listing is still perfectly well identified -- the currency is what
// identifies it -- and the venue is simply not known.
func TestListingVenues_CompositeRecordsNoVenue(t *testing.T) {
	p := testDBTx(t)
	instID := ensureListedInstrument(t, p, "ISIN", "US00COMPOS01", "USD")
	ctx := context.Background()
	listings, err := p.ListingsByInstrument(ctx, []string{instID})
	if err != nil {
		t.Fatalf("listings: %v", err)
	}
	if err := p.InsertInstrumentIdentifier(ctx, instID, listings[instID][0].ID, db.IdentifierInput{
		Ref: db.InstrumentRef{Type: "OPENFIGI_TICKER", Value: "CMP", Domain: "US"}, Canonical: true,
	}); err != nil {
		t.Fatalf("insert composite ticker: %v", err)
	}
	if got := listingVenues(t, p, instID); len(got) != 0 {
		t.Errorf("venues = %v, want none: a composite is not a MIC", got)
	}
}

// A MIC the reference table does not carry loses the venue and keeps the ticker,
// rather than failing the identifier write. It is the same judgement the
// resolver makes about a proposed exchange nothing recognises.
func TestListingVenues_UnknownMICIsDropped(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID, listingID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "US00UNKMIC01"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	// Written straight in, because EnsureInstrument would normalise a MIC it
	// recognised and this one it does not.
	if _, err := p.q.ExecContext(ctx, `
		INSERT INTO instrument_listing_identifiers (instrument_id, listing_id, identifier_type, domain, value, canonical)
		VALUES ($1::uuid, $2::uuid, 'MIC_TICKER', 'ZZZZ', 'UNK', true)
	`, instID, listingID); err != nil {
		t.Fatalf("insert ticker under an unknown MIC: %v", err)
	}
	if got := listingVenues(t, p, instID); len(got) != 0 {
		t.Errorf("venues = %v, want none", got)
	}
	row, err := p.GetInstrument(ctx, instID)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	if tkr, _ := findListingIdentifier(row, "MIC_TICKER", "UNK"); tkr == nil {
		t.Error("the ticker was lost with the venue, want it kept")
	}
}

// MIC_TICKER leads the name priority and now lives on a listing, so the trigger
// has to read both tables or every equity falls through to its description.
func TestRecomputeInstrumentName_ReadsBothGrains(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID, _, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "SOME EQUITY", Domain: "test"}, Canonical: false},
		{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "NAMED", Domain: "XNAS"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	row, err := p.GetInstrument(ctx, instID)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	if row.Name == nil || *row.Name != "NAMED" {
		t.Errorf("name = %v, want the ticker rather than the description", row.Name)
	}
}

// No listing is primary, so the name is stable across a security's lines. What
// breaks the tie is being on a line at all: a name nobody could place is a last
// resort rather than a first choice.
func TestRecomputeInstrumentName_PrefersANameOnALine(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID, _, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "US00PRIMARY1"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "KNOWN", Domain: "XNAS"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := p.q.ExecContext(ctx, `
		INSERT INTO instrument_listing_identifiers (instrument_id, listing_id, identifier_type, domain, value, canonical)
		VALUES ($1::uuid, NULL, 'MIC_TICKER', 'XLON', 'AAAAA', true)
	`, instID); err != nil {
		t.Fatalf("insert an unplaced ticker: %v", err)
	}
	row, err := p.GetInstrument(ctx, instID)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	// AAAAA sorts first on every other key, so only the preference for a name on
	// a line keeps it from taking over.
	if row.Name == nil || *row.Name != "KNOWN" {
		t.Errorf("name = %v, want the ticker that is on a line", row.Name)
	}
	// It still names the security, which is what makes it worth keeping.
	if len(row.UnplacedIdentifiers) != 1 || row.UnplacedIdentifiers[0].Ref.Value != "AAAAA" {
		t.Errorf("unplaced identifiers = %+v, want the XLON ticker", row.UnplacedIdentifiers)
	}
}

// The eager merge moves the loser's names across, and a name on a line has to
// land on the survivor's line of the same currency family. Without this every
// merge would silently drop the ticker it moves today.
func TestMergeInstruments_MovesListingIdentifiersByCurrencyFamily(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// Quoted in pence by one source.
	loser, _, err := p.EnsureInstrument(ctx, "STOCK", "", "GBX", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "FAMILY", Domain: "XLON"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure loser: %v", err)
	}
	// And in pounds by another, with two more names so it wins pickSurvivor.
	survivor, _, err := p.EnsureInstrument(ctx, "STOCK", "", "GBP", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00FAMILY01"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "SEDOL", Value: "BFAMILY"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure survivor: %v", err)
	}
	if loser == survivor {
		t.Fatal("the two securities are the same row")
	}

	// One answer naming both: the eager merge follows.
	merged, _, err := p.EnsureInstrument(ctx, "STOCK", "", "GBP", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00FAMILY01"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "FAMILY", Domain: "XLON"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure merge: %v", err)
	}
	if merged != survivor {
		t.Fatalf("survivor = %s, want %s", merged, survivor)
	}

	row, err := p.GetInstrument(ctx, merged)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	// GBX and GBP are one family, so the two lines are one line and the ticker
	// arrives on it rather than forking a second.
	if got := listingCurrencies(t, p, merged); len(got) != 1 || got[0] != "GBP" {
		t.Fatalf("listings = %v, want the single GBP line", got)
	}
	if tkr, _ := findListingIdentifier(row, "MIC_TICKER", "FAMILY"); tkr == nil {
		t.Errorf("the loser's ticker was dropped by the merge: %+v", row.Listings)
	}
}

// A ticker from a result that stated no currency names a line of the security and
// nothing says which. It is stored saying exactly that, rather than on a line
// invented to hold it. See
// docs/adr/0075-a-name-that-could-not-be-placed-names-no-line.md.
func TestEnsureInstrument_ANameWithNoCurrencyNamesNoLine(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID, listingID, err := p.EnsureInstrument(ctx, "STOCK", "", "", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00UNPLACED1"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "UNPL", Domain: "XLON"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if listingID != "" {
		t.Errorf("listing = %q, want none: nothing stated a currency", listingID)
	}
	if got := listingCurrencies(t, p, instID); len(got) != 0 {
		t.Errorf("listings = %v, want none", got)
	}
	row, err := p.GetInstrument(ctx, instID)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	if len(row.UnplacedIdentifiers) != 1 || row.UnplacedIdentifiers[0].Ref.Value != "UNPL" {
		t.Fatalf("unplaced identifiers = %+v, want the ticker", row.UnplacedIdentifiers)
	}
	// It still names the security, which is the whole reason to keep it.
	found, err := p.FindInstrumentByIdentifier(ctx, "MIC_TICKER", "XLON", "UNPL")
	if err != nil {
		t.Fatalf("find by the unplaced ticker: %v", err)
	}
	if found != instID {
		t.Errorf("lookup by the unplaced ticker = %q, want %q", found, instID)
	}
}

// A name on no line survives a merge: it hangs off the security, so the pairing
// by currency has nothing to say about it, and it would otherwise cascade away
// with the loser.
func TestMergeInstruments_CarriesNamesThatNameNoLine(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	loser, _, err := p.EnsureInstrument(ctx, "STOCK", "", "", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00UNPLACED2"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "UNPM", Domain: "XLON"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure loser: %v", err)
	}
	survivor, _, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "CUSIP", Value: "037833100"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "SEDOL", Value: "BUNPLM2"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure survivor: %v", err)
	}
	if err := p.runInTx(ctx, func(exec queryable) error {
		return mergeInstruments(ctx, exec, uuid.MustParse(survivor), uuid.MustParse(loser))
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}

	row, err := p.GetInstrument(ctx, survivor)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	if len(row.UnplacedIdentifiers) != 1 || row.UnplacedIdentifiers[0].Ref.Value != "UNPM" {
		t.Errorf("unplaced identifiers = %+v, want the loser's ticker", row.UnplacedIdentifiers)
	}
	// The survivor's own line is untouched by a name that named none.
	if got := listingCurrencies(t, p, survivor); len(got) != 1 || got[0] != "USD" {
		t.Errorf("listings = %v, want [USD]", got)
	}
}

// A name on no line is not on a line, so deleting one leaves it alone. It never
// named that line, and losing it with the line would lose what it does say.
func TestListingIdentifiers_UnplacedSurvivesAListingDelete(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID, listingID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00UNPLACED3"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := p.q.ExecContext(ctx, `
		INSERT INTO instrument_listing_identifiers (instrument_id, listing_id, identifier_type, domain, value, canonical)
		VALUES ($1::uuid, NULL, 'MIC_TICKER', 'XLON', 'UNPD', true)
	`, instID); err != nil {
		t.Fatalf("insert an unplaced ticker: %v", err)
	}
	if _, err := p.q.ExecContext(ctx, `DELETE FROM instrument_listings WHERE id = $1::uuid`, listingID); err != nil {
		t.Fatalf("delete the listing: %v", err)
	}
	var n int
	if err := p.q.QueryRowContext(ctx, `
		SELECT count(*) FROM instrument_listing_identifiers WHERE instrument_id = $1::uuid
	`, instID).Scan(&n); err != nil {
		t.Fatalf("count names: %v", err)
	}
	if n != 1 {
		t.Errorf("names after the listing delete = %d, want the unplaced one kept", n)
	}
}

// The union carries the loser's prices and coverage on to the survivor's line
// rather than letting them cascade away with it. A merge that lost them would
// cost a re-fetch of data nobody doubted. See
// docs/adr/0071-listings-merge-by-currency-and-an-unknown-one-splits.md.
func TestMergeInstruments_UnionsListingContents(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	loser, loserUSD, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "UNION", Domain: "XNAS"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure loser: %v", err)
	}
	// A second line the survivor does not hold at all, so the union has to give
	// the survivor one and carry this line's rows on to it.
	loserGBP, err := p.EnsureListing(ctx, loser, "GBP")
	if err != nil {
		t.Fatalf("loser GBP line: %v", err)
	}
	survivor, survivorUSD, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "US00UNION001"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "CUSIP", Value: "00UNION01"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure survivor: %v", err)
	}

	// Prices and coverage on each of the loser's lines, and a span on the
	// survivor's USD line that abuts the loser's so the two have to become one.
	price := func(listingID string, day int, close string) db.EODPrice {
		return db.EODPrice{ListingID: listingID, PriceDate: d(2024, 1, day),
			Close: decimal.RequireFromString(close), DataProvider: "test"}
	}
	if err := p.UpsertPricesForRange(ctx, loserUSD, "test",
		[]db.EODPrice{price(loserUSD, 10, "100")}, d(2024, 1, 8), d(2024, 1, 15), nil); err != nil {
		t.Fatalf("loser usd prices: %v", err)
	}
	if err := p.UpsertPricesForRange(ctx, loserGBP, "test",
		[]db.EODPrice{price(loserGBP, 10, "80")}, d(2024, 1, 8), d(2024, 1, 15), nil); err != nil {
		t.Fatalf("loser gbp prices: %v", err)
	}
	if err := p.UpsertPricesForRange(ctx, survivorUSD, "test",
		nil, d(2024, 1, 15), d(2024, 1, 20), nil); err != nil {
		t.Fatalf("survivor usd coverage: %v", err)
	}

	if err := p.runInTx(ctx, func(exec queryable) error {
		return mergeInstruments(ctx, exec, uuid.MustParse(survivor), uuid.MustParse(loser))
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}

	if got := listingCurrencies(t, p, survivor); len(got) != 2 || got[0] != "GBP" || got[1] != "USD" {
		t.Fatalf("listings = %v, want the union [GBP USD]", got)
	}
	var gbp string
	if err := p.q.QueryRowContext(ctx, `
		SELECT id::text FROM instrument_listings WHERE instrument_id = $1::uuid AND currency = 'GBP'
	`, survivor).Scan(&gbp); err != nil {
		t.Fatalf("read the survivor GBP line: %v", err)
	}

	// Both lines' bars are on the survivor, each still on the line it was quoted
	// for -- the whole point of keying a price on a line.
	for _, c := range []struct {
		listingID, close string
	}{{survivorUSD, "100"}, {gbp, "80"}} {
		var close decimal.Decimal
		if err := p.q.QueryRowContext(ctx, `
			SELECT close FROM eod_prices WHERE listing_id = $1::uuid AND price_date = $2
		`, c.listingID, d(2024, 1, 10)).Scan(&close); err != nil {
			t.Fatalf("read the bar on %s: %v", c.listingID, err)
		}
		if close.String() != c.close {
			t.Errorf("close on %s = %s, want %s", c.listingID, close, c.close)
		}
	}

	// The abutting spans became one, rather than the loser's being dropped or
	// stored beside the survivor's.
	var from, before time.Time
	var n int
	if err := p.q.QueryRowContext(ctx, `
		SELECT count(*), min(covered_from), max(covered_before)
		FROM price_coverage WHERE listing_id = $1::uuid
	`, survivorUSD).Scan(&n, &from, &before); err != nil {
		t.Fatalf("read coverage: %v", err)
	}
	if n != 1 || !from.Equal(d(2024, 1, 8)) || !before.Equal(d(2024, 1, 20)) {
		t.Errorf("coverage = %d span(s) [%s, %s), want one [2024-01-08, 2024-01-20)",
			n, from.Format("2006-01-02"), before.Format("2006-01-02"))
	}
}

// A declaration is a statement about a holding, so it moves with the postings it
// describes. Its instrument_id foreign key has no ON DELETE, so a merge that left
// it behind failed outright rather than quietly cascading -- which is what the
// merge did before the union.
func TestMergeInstruments_CarriesDeclarations(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|merge-decl", "U", "u@merge-decl.com")

	loser, loserLine, err := p.EnsureInstrument(ctx, "STOCK", "", "GBX", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "DECL", Domain: "XLON"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure loser: %v", err)
	}
	survivor, survivorLine, err := p.EnsureInstrument(ctx, "STOCK", "", "GBP", "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00DECL0001"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "SEDOL", Value: "BDECL01"}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure survivor: %v", err)
	}
	asOf := d(2025, 6, 1)
	decl := held(userID, "acct1", loser)
	decl.ListingID = loserLine
	if err := p.UpsertHoldingDeclaration(ctx, decl, "100", asOf, asOf); err != nil {
		t.Fatalf("declare on the loser: %v", err)
	}

	if err := p.runInTx(ctx, func(exec queryable) error {
		return mergeInstruments(ctx, exec, uuid.MustParse(survivor), uuid.MustParse(loser))
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}

	var gotInst string
	var gotLine *string
	if err := p.q.QueryRowContext(ctx, `
		SELECT instrument_id::text, listing_id::text FROM holding_declarations WHERE user_id = $1::uuid
	`, userID).Scan(&gotInst, &gotLine); err != nil {
		t.Fatalf("read declaration: %v", err)
	}
	if gotInst != survivor {
		t.Errorf("declaration names security %s, want the survivor %s", gotInst, survivor)
	}
	// GBX and GBP are one family, so the declaration lands on the survivor's line
	// rather than on no line.
	if gotLine == nil || *gotLine != survivorLine {
		t.Errorf("declaration names line %v, want the survivor's GBP line %s", gotLine, survivorLine)
	}
}

// listingVenues returns every venue across a security's lines, sorted.
func listingVenues(t *testing.T, p *Postgres, instrumentID string) []string {
	t.Helper()
	got, err := p.ListingsByInstrument(context.Background(), []string{instrumentID})
	if err != nil {
		t.Fatalf("listings by instrument: %v", err)
	}
	var out []string
	for _, l := range got[instrumentID] {
		out = append(out, l.Venues...)
	}
	sort.Strings(out)
	return out
}
