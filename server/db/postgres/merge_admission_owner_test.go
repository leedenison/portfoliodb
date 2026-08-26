package postgres

import (
	"context"
	"testing"

	"github.com/leedenison/portfoliodb/server/db"
)

// adr/0061's third condition: a chain may be drawn only through an association
// the system holds as settled. The owner is where that lives, and it is a
// question about the row rather than about the type -- the same ISIN is a fact
// from a plugin and a claim from a broker file.
//
// The two cases differ in nothing but who owns the CUSIP. Both resolve as the
// same user, so both see the row: one through the system fallback and one as
// their own claim.
//
// Note this is not about the authority of the caller's own claim. That is a
// separate input the merge does not take yet (0171 and 0172); the claim here
// carries system authority and is still refused, because what it would chain
// through is somebody's.
func TestEnsureInstrument_AClaimDoesNotChainThroughAnUnsettledRow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owned bool
		want  string
	}{
		{"the instance holds both names as facts", false, db.TelemetryMerged},
		{"one of them is the caller's own claim", true, db.TelemetryMergeUnsettled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, rec := recording(t)
			ctx := context.Background()
			u := aUser(t, p, "chain-owner-"+tc.name)
			a := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00CHAIN001"}, Canonical: true})
			b := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "CUSIP", Value: "CHAIN0001"}, Canonical: true})
			if tc.owned {
				if _, err := p.q.ExecContext(ctx, `
					UPDATE instrument_identifiers SET owner = $2
					WHERE instrument_id = $1 AND identifier_type = 'CUSIP'
				`, b, u); err != nil {
					t.Fatalf("make the CUSIP a claim: %v", err)
				}
			}

			idns := []db.IdentifierInput{
				{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00CHAIN001"}, Canonical: true},
				{Ref: db.InstrumentRef{Type: "CUSIP", Value: "CHAIN0001"}, Canonical: true},
			}
			if _, _, err := p.EnsureInstrument(ctx, u, "", "", "", "", "", idns, oneClaim(idns...), "", nil, testRun); err != nil {
				t.Fatalf("ensure instrument: %v", err)
			}
			if got := only(t, rec).Outcome; got != tc.want {
				t.Errorf("outcome = %s, want %s", got, tc.want)
			}
			// A refusal leaves both instruments exactly as they were, holding
			// the names and the transactions they already had.
			merged := !alive(t, p, a) || !alive(t, p, b)
			if merged != (tc.want == db.TelemetryMerged) {
				t.Errorf("merged = %v, want %v", merged, tc.want == db.TelemetryMerged)
			}
		})
	}
}

// Somebody else's claim is refused earlier and more simply than mergeVerdict:
// the lookup never sees it, so the caller's names land on one instrument and
// there is no pair to decide about. The refusal above is what the row's own
// owner gets; this is what everybody else gets.
func TestEnsureInstrument_AnothersClaimNeverReachesTheMerge(t *testing.T) {
	p, rec := recording(t)
	ctx := context.Background()
	ua, ub := aUser(t, p, "chain-owner-a"), aUser(t, p, "chain-owner-b")
	a := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00CHAIN002"}, Canonical: true})
	b := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "CUSIP", Value: "CHAIN0002"}, Canonical: true})
	if _, err := p.q.ExecContext(ctx, `
		UPDATE instrument_identifiers SET owner = $2
		WHERE instrument_id = $1 AND identifier_type = 'CUSIP'
	`, b, ua); err != nil {
		t.Fatalf("make the CUSIP a claim: %v", err)
	}

	idns := []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00CHAIN002"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "CUSIP", Value: "CHAIN0002"}, Canonical: true},
	}
	got, _, err := p.EnsureInstrument(ctx, ub, "", "", "", "", "", idns, oneClaim(idns...), "", nil, testRun)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	if got != a {
		t.Errorf("resolved to %s, want the instrument the ISIN names", a)
	}
	if len(rec.rows) != 0 {
		t.Errorf("recorded %+v, want nothing: the other user's row was never reached", rec.rows)
	}
	if !alive(t, p, b) {
		t.Error("the instrument holding the other user's claim was merged away")
	}
}

// A triple two instruments hold under two different owners is two owners
// agreeing about a security, not a contradiction: once merged they name one
// instrument, which is what the claim said. The collision check compares the
// owner for that reason -- what it asks is whether carrying the loser's names
// across would fail the exclusion constraint, and an answer on any other terms
// would refuse a merge the database would have accepted.
func TestEnsureInstrument_TwoOwnersOfOneTripleAreNotACollision(t *testing.T) {
	p, rec := recording(t)
	ctx := context.Background()
	u := aUser(t, p, "collision-owner")
	a := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00CHAIN003"}, Canonical: true})
	b := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "CUSIP", Value: "CHAIN0003"}, Canonical: true})
	ref := db.InstrumentRef{Type: "BROKER_DESCRIPTION", Domain: "test", Value: "ACME CORP"}
	if err := writeIdentifier(t, p, a, "", ref); err != nil {
		t.Fatalf("the instance's fact: %v", err)
	}
	if err := writeIdentifier(t, p, b, u, ref); err != nil {
		t.Fatalf("the user's claim: %v", err)
	}

	idns := []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00CHAIN003"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "CUSIP", Value: "CHAIN0003"}, Canonical: true},
	}
	survivor, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", idns, oneClaim(idns...), "", nil, testRun)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	if got := only(t, rec).Outcome; got != db.TelemetryMerged {
		t.Fatalf("outcome = %s, want %s", got, db.TelemetryMerged)
	}
	// Both rows are on the survivor, each still owned by whoever wrote it: a
	// merge moves a row, it does not settle it.
	row, err := p.GetInstrument(ctx, survivor)
	if err != nil || row == nil {
		t.Fatalf("get survivor: %v", err)
	}
	owners := map[string]bool{}
	for _, idn := range row.Identifiers {
		if idn.Ref.Type == "BROKER_DESCRIPTION" {
			owners[idn.Owner] = true
		}
	}
	if !owners[""] || !owners[u] {
		t.Errorf("the survivor holds the description under owners %v, want both the instance's and %s's", owners, u)
	}
}
