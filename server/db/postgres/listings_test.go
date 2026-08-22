package postgres

import (
	"context"
	"strings"
	"testing"

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

// The unknown listing says how many lines a security has is unknown, so a second
// one would be saying it twice.
func TestInstrumentListings_OneUnknownListing(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := ensureListedInstrument(t, p, "ISIN", "GB00LISTING02", "")

	if err := insertListing(ctx, p, instID, ""); err == nil {
		t.Error("inserting a second unknown listing succeeded, want a uniqueness violation")
	} else if !isUniqueViolation(err) {
		t.Errorf("insert second unknown listing: %v, want a uniqueness violation", err)
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

// A security created without a stated currency gets the unknown listing rather
// than none: how many lines it has is what is unknown, not whether it has any.
func TestEnsureInstrument_MintsAnUnknownListing(t *testing.T) {
	p := testDBTx(t)
	instID := ensureListedInstrument(t, p, "ISIN", "GB00LISTING06", "")

	got := listingCurrencies(t, p, instID)
	if len(got) != 1 || got[0] != "" {
		t.Errorf("listings = %v, want one unknown listing", got)
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

// A currency filling a blank names the line the security was already trading on.
// It moves the unknown listing rather than adding a sibling, which is what keeps
// "how many lines does this security have" answerable.
func TestEnsureInstrument_CurrencyNamesTheUnknownListing(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// A broker-description-only instrument: no canonical identifier, no currency.
	instID, err := p.EnsureInstrument(ctx, "", "", "", "Some Security", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "SOME SECURITY", Domain: "test"}, Canonical: false},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure description-only: %v", err)
	}
	if got := listingCurrencies(t, p, instID); len(got) != 1 || got[0] != "" {
		t.Fatalf("listings before = %v, want one unknown listing", got)
	}

	// Identification completes it, and the currency it learned names the line.
	same, err := p.EnsureInstrument(ctx, "STOCK", "", "GBX", "Some Security", "", "", []db.IdentifierInput{
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
		t.Errorf("listings after = %v, want [GBX]: the unknown listing should have moved, not gained a sibling", got)
	}
}

// The same move, through the archive import path.
func TestMergeInstrumentFromArchive_NamesTheUnknownListing(t *testing.T) {
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
	if len(got[withCurrency]) != 1 || got[withCurrency][0].Currency == nil || *got[withCurrency][0].Currency != "USD" {
		t.Errorf("listings for the USD security = %+v, want one USD listing", got[withCurrency])
	}
	if len(got[unknown]) != 1 || got[unknown][0].Currency != nil {
		t.Errorf("listings for the unknown security = %+v, want one null-currency listing", got[unknown])
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
	if len(row.Listings) != 1 || row.Listings[0].Currency == nil || *row.Listings[0].Currency != "USD" {
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
	id, err := p.EnsureInstrument(context.Background(), "STOCK", "", currency, "", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: idType, Value: value}, Canonical: true},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument %s %s: %v", idType, value, err)
	}
	return id
}

// insertListing writes a listing directly, to exercise the constraints rather
// than the writer. currency "" is the unknown listing.
func insertListing(ctx context.Context, p *Postgres, instrumentID, currency string) error {
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO instrument_listings (instrument_id, currency) VALUES ($1::uuid, $2)
	`, instrumentID, nullStr(currency))
	return err
}

// listingCurrencies returns the instrument's listing currencies, "" for unknown.
func listingCurrencies(t *testing.T, p *Postgres, instrumentID string) []string {
	t.Helper()
	got, err := p.ListingsByInstrument(context.Background(), []string{instrumentID})
	if err != nil {
		t.Fatalf("listings by instrument: %v", err)
	}
	out := make([]string, 0, len(got[instrumentID]))
	for _, l := range got[instrumentID] {
		if l.Currency == nil {
			out = append(out, "")
			continue
		}
		out = append(out, *l.Currency)
	}
	return out
}
