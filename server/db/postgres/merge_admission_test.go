package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/leedenison/portfoliodb/server/db"
)

// ensureOne creates an instrument holding exactly the identifiers given, with
// nothing asserted about them beyond that they arrived together.
func ensureOne(t *testing.T, p *Postgres, currency string, idns ...db.IdentifierInput) string {
	t.Helper()
	id, _, err := p.EnsureInstrument(context.Background(), "", "", currency, "", "", "", idns, oneClaim(idns...), "", nil, "")
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
	got, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", merging, oneClaim(merging...), "", nil, "")
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
	got, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{isin, cusip}, claims, "", nil, "")
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
			got, _, err := p.EnsureInstrument(ctx, "", "", "USD", "", "", "", merging, oneClaim(merging...), "", nil, "")
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
	got, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", merging, oneClaim(merging...), "", nil, "")
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
	got, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{figi}, []db.IdentityClaim{claim}, "", nil, "")
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
	got, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{isin, cusip, sedol}, claims, "", nil, "")
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
	got, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", merging, oneClaim(merging...), "", nil, "")
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

// --- what the merge decision records ---

// mergeRecorder captures the merge rows the store writes. It embeds NopTelemetry
// so that the rest of the interface stays out of the test: what a merge records
// is the only thing under examination here.
type mergeRecorder struct {
	db.NopTelemetry
	rows []db.TelemetryMerge
}

func (m *mergeRecorder) WriteMerge(_ context.Context, r db.TelemetryMerge) {
	m.rows = append(m.rows, r)
}

// mustUUID parses an instrument id, which the fixtures hand back as text.
func mustUUID(t *testing.T, id string) uuid.UUID {
	t.Helper()
	u, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("parse instrument id %q: %v", id, err)
	}
	return u
}

// testRun is a run id for a store under test. Any non-empty value will do: the
// writer here is a recorder rather than the telemetry schema, so nothing joins
// it to a run row.
const testRun = "3f4b1d4e-0000-4000-8000-000000000001"

// recording returns a store that captures what it decides about a merge.
func recording(t *testing.T) (*Postgres, *mergeRecorder) {
	t.Helper()
	rec := &mergeRecorder{}
	return testDBTx(t).WithTelemetry(rec), rec
}

// only returns the single row recorded, failing the test where there is not
// exactly one. Every case below decides about one pair, so a second row is a
// refusal counted twice rather than a detail to look past.
func only(t *testing.T, rec *mergeRecorder) db.TelemetryMerge {
	t.Helper()
	if len(rec.rows) != 1 {
		t.Fatalf("recorded %d merge decisions, want 1: %+v", len(rec.rows), rec.rows)
	}
	return rec.rows[0]
}

// The ordinary resolution decides nothing. Every identifier names the security
// already holding it, so no pair is in question and the table does not grow with
// traffic.
func TestEnsureInstrument_RecordsNothingWhereOneInstrumentHoldsEverything(t *testing.T) {
	p, rec := recording(t)
	ctx := context.Background()
	idns := []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00QUIET001"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "CUSIP", Value: "QUIET0001"}, Canonical: true},
	}
	if _, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", idns, oneClaim(idns...), "", nil, testRun); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Again, now that one instrument holds both.
	if _, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", idns, oneClaim(idns...), "", nil, testRun); err != nil {
		t.Fatalf("re-ensure: %v", err)
	}
	if len(rec.rows) != 0 {
		t.Errorf("recorded %+v, want nothing", rec.rows)
	}
}

// A merge that happened says so, and says which two names it acted on. Nothing
// recorded a merge at all before this: the decision is taken inside the write,
// where there was no run to hang a row off and no logger to say it out loud.
func TestEnsureInstrument_RecordsAnAdmittedMerge(t *testing.T) {
	p, rec := recording(t)
	ctx := context.Background()
	isin := db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00REC00001"}, Canonical: true}
	cusip := db.IdentifierInput{Ref: db.InstrumentRef{Type: "CUSIP", Value: "REC000001"}, Canonical: true}
	a := ensureOne(t, p, "", isin)
	b := ensureOne(t, p, "", cusip)

	merging := []db.IdentifierInput{isin, cusip}
	if _, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", merging, oneClaim(merging...), "", nil, testRun); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	got := only(t, rec)
	if got.Outcome != db.TelemetryMerged {
		t.Errorf("outcome = %q, want %q", got.Outcome, db.TelemetryMerged)
	}
	if got.RunID != testRun {
		t.Errorf("run = %q, want %q", got.RunID, testRun)
	}
	if got.A.Value != isin.Ref.Value || got.B.Value != cusip.Ref.Value {
		t.Errorf("endpoints = %+v / %+v, want %s / %s", got.A, got.B, isin.Ref.Value, cusip.Ref.Value)
	}
	if got.AInstrument != a || got.BInstrument != b {
		t.Errorf("instruments = %s / %s, want %s / %s", got.AInstrument, got.BInstrument, a, b)
	}
	if got.Collision != nil {
		t.Errorf("collision = %+v, want none", got.Collision)
	}
}

// A refusal records the reason, and the two reasons are kept apart because they
// need different fixes: a type that reassigns its values is working as intended,
// where two names that were never correct at one time may be a vintage recorded
// wrongly.
func TestEnsureInstrument_RecordsWhyAMergeWasRefused(t *testing.T) {
	t.Run("a routinely reassigned name cannot carry the chain", func(t *testing.T) {
		p, rec := recording(t)
		ctx := context.Background()
		ticker := db.IdentifierInput{Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "RECREUSE", Domain: "XNAS"}, Canonical: true}
		isin := db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00RECREUS1"}, Canonical: true}
		ensureOne(t, p, "USD", ticker)
		ensureOne(t, p, "USD", isin)

		merging := []db.IdentifierInput{ticker, isin}
		if _, _, err := p.EnsureInstrument(ctx, "", "", "USD", "", "", "", merging, oneClaim(merging...), "", nil, testRun); err != nil {
			t.Fatalf("ensure: %v", err)
		}
		if got := only(t, rec).Outcome; got != db.TelemetryMergeUnmediated {
			t.Errorf("outcome = %q, want %q", got, db.TelemetryMergeUnmediated)
		}
	})

	t.Run("two names never correct at one time", func(t *testing.T) {
		p, rec := recording(t)
		ctx := context.Background()
		early := db.IdentifierInput{
			Ref:         db.InstrumentRef{Type: "ISIN", Value: "GB00RECSPL01"},
			Canonical:   true,
			ValidBefore: day(2024, 6, 10),
		}
		late := db.IdentifierInput{
			Ref:       db.InstrumentRef{Type: "CUSIP", Value: "RECSPL001"},
			Canonical: true,
			ValidFrom: day(2024, 6, 10),
		}
		ensureOne(t, p, "", early)
		ensureOne(t, p, "", late)

		merging := []db.IdentifierInput{early, late}
		if _, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", merging, oneClaim(merging...), "", nil, testRun); err != nil {
			t.Fatalf("ensure: %v", err)
		}
		if got := only(t, rec).Outcome; got != db.TelemetryMergeDisjoint {
			t.Errorf("outcome = %q, want %q", got, db.TelemetryMergeDisjoint)
		}
	})
}

// Two identifiers the resolver gathered from separate answers assert nothing
// about each other, so the instruments are left apart. That refusal has no pair a
// claim named to hang off, and it is the commonest of the four: it is what a
// plugin set whose vocabularies do not overlap produces all day.
func TestEnsureInstrument_RecordsAnUncorroboratedRefusal(t *testing.T) {
	p, rec := recording(t)
	ctx := context.Background()
	isin := db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00RECUNC01"}, Canonical: true}
	cusip := db.IdentifierInput{Ref: db.InstrumentRef{Type: "CUSIP", Value: "RECUNC001"}, Canonical: true}
	a := ensureOne(t, p, "", isin)
	b := ensureOne(t, p, "", cusip)

	// Nil claims: the caller assembled the set and nobody asserted the pair.
	got, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{isin, cusip}, nil, "", nil, testRun)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got != a {
		t.Errorf("attached to %s, want the anchor %s", got, a)
	}
	if !alive(t, p, a) || !alive(t, p, b) {
		t.Error("neither instrument should have been merged away")
	}
	row := only(t, rec)
	if row.Outcome != db.TelemetryMergeUncorroborated {
		t.Errorf("outcome = %q, want %q", row.Outcome, db.TelemetryMergeUncorroborated)
	}
	if row.AInstrument != a || row.BInstrument != b {
		t.Errorf("instruments = %s / %s, want %s / %s", row.AInstrument, row.BInstrument, a, b)
	}
}

// The state a collision needs cannot be built, and that is the finding rather
// than a gap in the test: both exclusion constraints are global and say nothing
// about the instrument, so one triple held by two instruments over overlapping
// intervals is already refused at insert. collidingIdentifier therefore finds
// nothing today, and becomes reachable when the owner enters the constraint,
// which is 0142. This pins that, so the day the constraint changes this test is
// what says the collision path has come alive.
//
// The insert goes last because it aborts the transaction the whole test runs in.
func TestCollidingIdentifier_TheStateItLooksForIsRefusedAtInsert(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	shared := db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00COLLIDE1"}, Canonical: true}
	a := ensureOne(t, p, "", shared)
	b := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "CUSIP", Value: "COLLIDE01"}, Canonical: true})

	// Two instruments sharing no name: nothing to find, and the merge would be
	// let through.
	au, bu := mustUUID(t, a), mustUUID(t, b)
	if _, bad, cErr := collidingIdentifier(ctx, p.q, au, bu); cErr != nil || bad {
		t.Fatalf("collidingIdentifier = %v, %v; want false, nil", bad, cErr)
	}

	// Giving the second instrument the first's name over an overlapping interval
	// is the state the pre-check exists for, and it cannot be reached.
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO instrument_identifiers (instrument_id, identifier_type, value, canonical)
		VALUES ($1, $2, $3, true)
	`, bu, shared.Ref.Type, shared.Ref.Value)
	if err == nil {
		t.Fatal("two instruments held one triple over overlapping intervals; the exclusion constraint should have refused it")
	}
	if !isIdentifierConflict(err) {
		t.Fatalf("refused for the wrong reason: %v", err)
	}
}
