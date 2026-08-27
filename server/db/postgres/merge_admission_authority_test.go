package postgres

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
)

// statedBy is oneClaim with an owner: the same identifiers named together, by a
// source carrying user authority rather than by a plugin.
func statedBy(owner string, idns ...db.IdentifierInput) []db.IdentityClaim {
	c := db.IdentityClaim{
		Identifiers: make([]db.ClaimedIdentifier, 0, len(idns)),
		Owner:       owner,
	}
	for _, idn := range idns {
		c.Identifiers = append(c.Identifiers, db.ClaimedIdentifier{Ref: idn.Ref, Role: db.ClaimRoleStated})
	}
	return []db.IdentityClaim{c}
}

// adr/0079: what a merge does is decided by the authority of the claim asking
// for it. Both stored rows here are facts, so every condition about the rows is
// satisfied and the same claim from a plugin merges outright. The file is the
// only thing that changed, and it is the whole of the difference.
//
// The refusal is not the row-owner one: nothing in reach is owned by anybody.
// It is the claim itself having no standing to settle an association for an
// instance whose instruments are global.
func TestEnsureInstrument_AUserAuthorityClaimOverTwoFactsIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		user  bool
		want  string
		merge bool
	}{
		{"a plugin named both", false, db.TelemetryMerged, true},
		{"a broker file named both", true, db.TelemetryMergeUnauthoritative, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, rec := recording(t)
			ctx := context.Background()
			u := aUser(t, p, "authority-"+tc.name)
			a := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00AUTH0001"}, Canonical: true})
			b := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "CUSIP", Value: "AUTH00001"}, Canonical: true})

			idns := []db.IdentifierInput{
				{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00AUTH0001"}, Canonical: true},
				{Ref: db.InstrumentRef{Type: "CUSIP", Value: "AUTH00001"}, Canonical: true},
			}
			claims := oneClaim(idns...)
			if tc.user {
				claims = statedBy(u, idns...)
			}
			if _, _, err := p.EnsureInstrument(ctx, u, "", "", "", "", "", idns, claims, "", nil, testRun); err != nil {
				t.Fatalf("ensure instrument: %v", err)
			}
			if got := only(t, rec).Outcome; got != tc.want {
				t.Errorf("outcome = %s, want %s", got, tc.want)
			}
			// A refusal leaves both instruments standing, holding the names and
			// the transactions they already had. Nothing on either is rewritten
			// and no row changes owner.
			merged := !alive(t, p, a) || !alive(t, p, b)
			if merged != tc.merge {
				t.Errorf("merged = %v, want %v", merged, tc.merge)
			}
		})
	}
}

// The claim's authority is asked before anything about the two stored rows,
// because it is the only condition that is not about them. A user's claim whose
// endpoints would also have failed a row condition is reported as the refusal
// that is true of the claim rather than as one of the row's.
//
// It matters for what a reader does next. An unmediated pair is working as
// intended and is noise; an unauthoritative one wants a plugin able to
// corroborate what the file said. Reporting the row's reason would file this
// under the one nobody acts on.
func TestEnsureInstrument_TheClaimsAuthorityIsAskedFirst(t *testing.T) {
	p, rec := recording(t)
	ctx := context.Background()
	u := aUser(t, p, "authority-precedence")
	// MIC_TICKER reassigns its values as a matter of course, so a claim naming
	// one mediates nothing however sound its source.
	ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "MIC_TICKER", Domain: "XNAS", Value: "AUTHP"}, Canonical: true})
	ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "CUSIP", Value: "AUTHP0001"}, Canonical: true})

	idns := []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "MIC_TICKER", Domain: "XNAS", Value: "AUTHP"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "CUSIP", Value: "AUTHP0001"}, Canonical: true},
	}
	if _, _, err := p.EnsureInstrument(ctx, u, "", "", "", "", "", idns, statedBy(u, idns...), "", nil, testRun); err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	if got := only(t, rec).Outcome; got != db.TelemetryMergeUnauthoritative {
		t.Errorf("outcome = %s, want %s", got, db.TelemetryMergeUnauthoritative)
	}
}

// One resolution carries both levels: the identifiers a plugin returned, and the
// association the uploaded file asserted for itself. They are decided
// separately, which is why the level is on the claim rather than on the call.
func TestEnsureInstrument_OneResolutionCarriesBothLevels(t *testing.T) {
	p, rec := recording(t)
	ctx := context.Background()
	u := aUser(t, p, "authority-both-levels")
	// Three instruments. The plugin's claim names the first two, the file's
	// claim names the first and the third.
	a := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00AUTH0002"}, Canonical: true})
	b := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "CUSIP", Value: "AUTH00002"}, Canonical: true})
	c := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "SEDOL", Value: "AUTH002"}, Canonical: true})

	isin := db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00AUTH0002"}, Canonical: true}
	cusip := db.IdentifierInput{Ref: db.InstrumentRef{Type: "CUSIP", Value: "AUTH00002"}, Canonical: true}
	sedol := db.IdentifierInput{Ref: db.InstrumentRef{Type: "SEDOL", Value: "AUTH002"}, Canonical: true}

	claims := append(oneClaim(isin, cusip), statedBy(u, isin, sedol)...)
	if _, _, err := p.EnsureInstrument(ctx, u, "", "", "", "", "", []db.IdentifierInput{isin, cusip, sedol}, claims, "", nil, testRun); err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	got := make(map[string]string, len(rec.rows))
	for _, r := range rec.rows {
		got[r.B.Type] = r.Outcome
	}
	if got["CUSIP"] != db.TelemetryMerged {
		t.Errorf("the plugin's pair = %s, want %s", got["CUSIP"], db.TelemetryMerged)
	}
	if got["SEDOL"] != db.TelemetryMergeUnauthoritative {
		t.Errorf("the file's pair = %s, want %s", got["SEDOL"], db.TelemetryMergeUnauthoritative)
	}
	// The plugin's merge happened and the file's did not, in one call.
	if alive(t, p, a) && alive(t, p, b) {
		t.Error("the plugin's corroborated pair was not merged")
	}
	if !alive(t, p, c) {
		t.Error("the instrument the file named was merged away on its word alone")
	}
}

// The telemetry.merge CHECK restates the Go vocabulary, because a CHECK cannot
// read a constant. This holds the two in step in the pattern
// TestAssetClassCheck_matchesProtoVocabulary follows, and it fails in both
// directions: an outcome mergeVerdict can return that the database would reject,
// and a value left in the migration that no branch produces.
//
// Worth pinning because the failure is invisible until it happens. A merge
// decision is recorded after its transaction commits, so a rejected outcome
// loses the record of a decision that has already taken effect rather than
// failing the work that produced it.
func TestMergeOutcomeCheck_matchesGoVocabulary(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	inGo := []string{
		db.TelemetryMerged,
		db.TelemetryMergeUncorroborated,
		db.TelemetryMergeUnmediated,
		db.TelemetryMergeUnsettled,
		db.TelemetryMergeDisjoint,
		db.TelemetryMergeUnauthoritative,
		db.TelemetryMergeCollision,
	}

	var def string
	if err := p.q.QueryRowContext(ctx, `
		SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conrelid = 'telemetry.merge'::regclass AND conname LIKE '%outcome%'
	`).Scan(&def); err != nil {
		t.Fatalf("read the merge outcome CHECK: %v", err)
	}
	var inSQL []string
	for _, m := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(def, -1) {
		inSQL = append(inSQL, m[1])
	}
	want := append([]string(nil), inGo...)
	sort.Strings(inSQL)
	sort.Strings(want)
	if strings.Join(inSQL, ",") != strings.Join(want, ",") {
		t.Errorf("the CHECK and the Go constants disagree:\n  SQL: %s\n  Go:  %s",
			strings.Join(inSQL, ","), strings.Join(want, ","))
	}

	// And the constraint is live rather than merely well spelled. Each value
	// goes in under its own savepoint, since a rejected statement aborts the
	// transaction the next case would run in.
	runID := seedRun(t, p, "tx_import", time.Now())
	for _, outcome := range inGo {
		if _, err := p.q.ExecContext(ctx, `SAVEPOINT vocab`); err != nil {
			t.Fatalf("savepoint: %v", err)
		}
		// A collision row carries the triple both instruments held; every other
		// outcome must leave it null, which chk_telemetry_merge_collision pins.
		var collision any
		if outcome == db.TelemetryMergeCollision {
			collision = "ISIN"
		}
		_, err := p.q.ExecContext(ctx, `
			INSERT INTO telemetry.merge
				(run_id, outcome, a_type, a_value, b_type, b_value,
				 a_instrument_id, b_instrument_id, collision_type, collision_value)
			VALUES ($1::uuid, $2, 'ISIN', 'GB00VOCAB001', 'CUSIP', 'VOCAB0001',
				gen_random_uuid(), gen_random_uuid(), $3, $3)
		`, runID, outcome, collision)
		if err != nil {
			t.Errorf("the database rejected %s, which mergeVerdict can return: %v", outcome, err)
		}
		if _, err := p.q.ExecContext(ctx, `RELEASE SAVEPOINT vocab`); err != nil {
			t.Fatalf("release savepoint: %v", err)
		}
	}
}
