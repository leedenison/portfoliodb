package postgres

import (
	"context"
	"testing"

	"github.com/leedenison/portfoliodb/server/db"
)

// ensureOne creates an instrument holding exactly the identifiers given, with
// nothing asserted about them beyond that they arrived together.
func ensureOne(t *testing.T, p *Postgres, currency string, idns ...db.IdentifierInput) string {
	t.Helper()
	id, _, err := p.EnsureInstrument(context.Background(), "", currency, "", "", "", idns, oneClaim(idns...), "", nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	return id
}

// alive reports whether an instrument is still there, which is how a refused
// merge is told from an admitted one: nothing is deleted where nothing was
// corroborated.
func alive(t *testing.T, p *Postgres, id string) bool {
	t.Helper()
	row, err := p.GetInstrument(context.Background(), id)
	if err != nil {
		t.Fatalf("get instrument: %v", err)
	}
	return row != nil
}

// A merge acts on a claim, so two identifiers named together by one result merge
// the instruments holding them.
func TestEnsureInstrument_OneClaimNamingBothMerges(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	a := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00CLAIM001"}, Canonical: true})
	b := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "CUSIP", Value: "CLAIM0001"}, Canonical: true})
	if a == b {
		t.Fatal("fixture did not create two instruments")
	}

	merging := []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00CLAIM001"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "CUSIP", Value: "CLAIM0001"}, Canonical: true},
	}
	got, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", merging, oneClaim(merging...), "", nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got != a && got != b {
		t.Fatalf("survivor = %s, want one of %s or %s", got, a, b)
	}
	loser := a
	if got == a {
		loser = b
	}
	if alive(t, p, loser) {
		t.Errorf("instrument %s should have been merged away", loser)
	}
}

// The union is not a claim. Two results each naming one of the two identifiers
// have asserted nothing about the pair, however complete the set looks by the
// time it reaches the store.
func TestEnsureInstrument_TwoClaimsNamingOneEachDoNotMerge(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	isin := db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00UNION001"}, Canonical: true}
	cusip := db.IdentifierInput{Ref: db.InstrumentRef{Type: "CUSIP", Value: "UNION0001"}, Canonical: true}
	a := ensureOne(t, p, "", isin)
	b := ensureOne(t, p, "", cusip)

	// Two answers, each about one identifier. Flattened they look exactly like
	// the corroborated case above, which is why the partition is the whole of
	// the evidence.
	claims := append(oneClaim(isin), oneClaim(cusip)...)
	got, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", []db.IdentifierInput{isin, cusip}, claims, "", nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got != a {
		t.Errorf("attached to %s, want the instrument the first identifier reached, %s", got, a)
	}
	if !alive(t, p, a) || !alive(t, p, b) {
		t.Error("neither instrument should have been merged away")
	}
}

// A type that reassigns its values as a matter of course carries no chain, so a
// claim naming one of them corroborates nothing about the instrument holding it.
func TestEnsureInstrument_ARoutinelyReassignedNameDoesNotMerge(t *testing.T) {
	for _, tc := range []struct {
		name string
		idn  db.IdentifierInput
	}{
		{"ticker", db.IdentifierInput{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "REUSE", Domain: "XNAS"}, Canonical: true}},
		{"contract symbol", db.IdentifierInput{Ref: db.InstrumentRef{Type: "OCC", Value: "REUSE250117C00100000"}, Canonical: true}},
		{"broker description", db.IdentifierInput{Ref: db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "REUSED TEXT", Domain: "IBKR"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testDBTx(t)
			ctx := context.Background()
			isin := db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00REUSE001"}, Canonical: true}
			// The reassignable name first, so it anchors the group and the
			// refusal is not the anchor's own doing.
			reused := ensureOne(t, p, "USD", tc.idn)
			identified := ensureOne(t, p, "USD", isin)

			merging := []db.IdentifierInput{tc.idn, isin}
			got, _, err := p.EnsureInstrument(ctx, "", "USD", "", "", "", merging, oneClaim(merging...), "", nil)
			if err != nil {
				t.Fatalf("ensure: %v", err)
			}
			if got != reused {
				t.Errorf("attached to %s, want %s", got, reused)
			}
			if !alive(t, p, reused) || !alive(t, p, identified) {
				t.Error("neither instrument should have been merged away")
			}
		})
	}
}

// Two names that were never correct at one time cannot both be reached by a
// claim made at any single moment, whatever their types allow.
func TestEnsureInstrument_DisjointIntervalsDoNotMerge(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	early := db.IdentifierInput{
		Ref:         db.InstrumentRef{Type: "ISIN", Value: "GB00SPLIT001"},
		Canonical:   true,
		ValidBefore: day(2024, 6, 10),
	}
	late := db.IdentifierInput{
		Ref:       db.InstrumentRef{Type: "CUSIP", Value: "SPLIT0001"},
		Canonical: true,
		ValidFrom: day(2024, 6, 10),
	}
	a := ensureOne(t, p, "", early)
	b := ensureOne(t, p, "", late)

	merging := []db.IdentifierInput{early, late}
	got, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", merging, oneClaim(merging...), "", nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got != a {
		t.Errorf("attached to %s, want %s", got, a)
	}
	if !alive(t, p, a) || !alive(t, p, b) {
		t.Error("neither instrument should have been merged away")
	}
}

// A value the result was strictly filtered on is never stored, so it is never
// among the caller's identifiers -- and it corroborates the association as
// loudly as a returned value does. See adr/0060.
func TestEnsureInstrument_AFilteredValueCorroborates(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	isin := db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00FILTER01"}, Canonical: true}
	figi := db.IdentifierInput{Ref: db.InstrumentRef{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG00FILTER01"}, Canonical: true}
	a := ensureOne(t, p, "", isin)
	b := ensureOne(t, p, "", figi)

	// The provider was asked to map the ISIN and answered with the FIGI, so the
	// answer names the FIGI and the call names the ISIN. Only the FIGI is
	// written.
	claim := db.IdentityClaim{Identifiers: []db.ClaimedIdentifier{
		{Ref: figi.Ref, Role: db.ClaimRoleReturned},
		{Ref: isin.Ref, Role: db.ClaimRoleFiltered},
	}}
	got, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", []db.IdentifierInput{figi}, []db.IdentityClaim{claim}, "", nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got != a && got != b {
		t.Fatalf("survivor = %s, want one of %s or %s", got, a, b)
	}
	if alive(t, p, a) && alive(t, p, b) {
		t.Error("the filtered value did not corroborate the merge")
	}
}

// A claim ties A to B and another ties B to C, so all three are one: each link
// is a corroborated association in its own right.
func TestEnsureInstrument_ClaimsChainThroughAThirdInstrument(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	isin := db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00CHAIN001"}, Canonical: true}
	cusip := db.IdentifierInput{Ref: db.InstrumentRef{Type: "CUSIP", Value: "CHAIN0001"}, Canonical: true}
	sedol := db.IdentifierInput{Ref: db.InstrumentRef{Type: "SEDOL", Value: "BCHAIN1"}, Canonical: true}
	a := ensureOne(t, p, "", isin)
	b := ensureOne(t, p, "", cusip)
	c := ensureOne(t, p, "", sedol)

	claims := append(oneClaim(isin, cusip), oneClaim(cusip, sedol)...)
	got, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", []db.IdentifierInput{isin, cusip, sedol}, claims, "", nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	for _, id := range []string{a, b, c} {
		if id != got && alive(t, p, id) {
			t.Errorf("instrument %s should have been merged away into %s", id, got)
		}
	}
}

// A broker description beside a security that is already identified: the
// transaction attaches to the identified one, and the description keeps naming
// the instrument it always named. Binding one user's word to instance-global
// reference data is 0142's, not a merge.
func TestEnsureInstrument_ADescriptionOnlyInstrumentSurvivesBesideAnIdentifiedOne(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	desc := db.IdentifierInput{Ref: db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "SOME STOCK INC", Domain: "IBKR"}}
	isin := db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00DESC0001"}, Canonical: true}
	identified := ensureOne(t, p, "", isin)
	descOnly := ensureOne(t, p, "", desc)

	// The resolution's own order: what the winner returned first, with the
	// description it binds appended last.
	merging := []db.IdentifierInput{isin, desc}
	got, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", merging, oneClaim(merging...), "", nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got != identified {
		t.Errorf("attached to %s, want the identified instrument %s", got, identified)
	}
	if !alive(t, p, descOnly) {
		t.Error("the description-only instrument should have survived")
	}
	// And it is still description-only: nothing was written on to it, and the
	// ISIN is still the other instrument's.
	row, err := p.GetInstrument(ctx, descOnly)
	if err != nil || row == nil {
		t.Fatalf("get description-only instrument: %v", err)
	}
	if db.Identified(row) {
		t.Errorf("the description-only instrument was completed from a refused merge: %+v", row.Identifiers)
	}
}
