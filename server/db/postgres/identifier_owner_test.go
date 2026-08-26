package postgres

import (
	"context"
	"testing"

	"github.com/leedenison/portfoliodb/server/db"
)

// Ownership of user-supplied mappings: an identifier row is a fact when nobody
// owns it and that user's claim when somebody does. See
// docs/adr/0063-identity-claims-are-owned-until-users-corroborate-them.md and
// docs/spec/identifiers.md.

// aUser creates a user to own a claim. The tests below need an id a foreign key
// accepts and nothing else about the person.
func aUser(t *testing.T, p *Postgres, sub string) string {
	t.Helper()
	return setupUserWithCurrency(t, p, sub, sub, sub+"@example.test", "USD")
}

// writeIdentifier files one row directly, which is the only way to write a name
// on behalf of somebody the resolution path would not have written it for.
func writeIdentifier(t *testing.T, p *Postgres, instrumentID, owner string, ref db.InstrumentRef) error {
	t.Helper()
	_, err := p.q.ExecContext(context.Background(), `
		INSERT INTO instrument_identifiers (instrument_id, identifier_type, domain, value, canonical, owner)
		VALUES ($1, $2, $3, $4, true, $5)
	`, instrumentID, ref.Type, nullStr(ref.Domain), ref.Value, nullStr(owner))
	return err
}

// The invariant the owner weakened. Two users may each hold one triple naming a
// different instrument, which is the disagreement the promotion sweep has to be
// able to see; under the constraint as it stood the second insert was rejected
// and the disagreement never existed to be surfaced.
func TestIdentifierOwner_TwoUsersMayDisagreeAboutOneTriple(t *testing.T) {
	p := testDBTx(t)
	ua, ub := aUser(t, p, "owner-a"), aUser(t, p, "owner-b")
	a := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00OWNER001"}, Canonical: true})
	b := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00OWNER002"}, Canonical: true})

	ref := db.InstrumentRef{Type: "BROKER_DESCRIPTION", Domain: "test", Value: "ACME CORP"}
	if err := writeIdentifier(t, p, a, ua, ref); err != nil {
		t.Fatalf("first user's claim: %v", err)
	}
	if err := writeIdentifier(t, p, b, ub, ref); err != nil {
		t.Fatalf("second user's claim: %v", err)
	}
}

// A user's claim and the instance's fact are not a collision either, which is
// what lets a user hold a mapping contradicting the one everybody else resolves
// through.
func TestIdentifierOwner_AClaimMayContradictAFact(t *testing.T) {
	p := testDBTx(t)
	u := aUser(t, p, "owner-c")
	a := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00OWNER003"}, Canonical: true})
	b := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00OWNER004"}, Canonical: true})

	ref := db.InstrumentRef{Type: "BROKER_DESCRIPTION", Domain: "test", Value: "ACME CORP"}
	if err := writeIdentifier(t, p, a, "", ref); err != nil {
		t.Fatalf("the instance's fact: %v", err)
	}
	if err := writeIdentifier(t, p, b, u, ref); err != nil {
		t.Fatalf("the user's claim: %v", err)
	}
}

// What did not weaken. One owner still names one instrument at a time, so a
// second row for the same owner over an overlapping interval is rejected exactly
// as it always was -- and so is a second system row, which a bare 'owner WITH ='
// in the constraint would have let through.
func TestIdentifierOwner_OneOwnerStillNamesOneInstrument(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owned bool
	}{
		{"a user cannot hold one triple twice", true},
		{"nor can the instance", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testDBTx(t)
			var owner string
			if tc.owned {
				owner = aUser(t, p, "owner-"+tc.name)
			}
			a := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00OWNER005"}, Canonical: true})
			b := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00OWNER006"}, Canonical: true})

			ref := db.InstrumentRef{Type: "BROKER_DESCRIPTION", Domain: "test", Value: "ACME CORP"}
			if err := writeIdentifier(t, p, a, owner, ref); err != nil {
				t.Fatalf("first row: %v", err)
			}
			// Last, because the violation aborts the transaction the test runs in.
			err := writeIdentifier(t, p, b, owner, ref)
			if err == nil {
				t.Fatal("second row was accepted, want the overlap constraint to reject it")
			}
			if !isIdentifierConflict(err) {
				t.Fatalf("second row failed with %v, want an identifier conflict", err)
			}
		})
	}
}

// Owner-scoped with a system fallback, which is the whole of the resolution
// rule. The four cases are one lookup asked by four callers.
func TestFindInstrumentByIdentifier_OwnerScopedWithASystemFallback(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	ua, ub := aUser(t, p, "owner-d"), aUser(t, p, "owner-e")
	fact := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00OWNER007"}, Canonical: true})
	claim := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00OWNER008"}, Canonical: true})

	ref := db.InstrumentRef{Type: "BROKER_DESCRIPTION", Domain: "test", Value: "ACME CORP"}
	if err := writeIdentifier(t, p, fact, "", ref); err != nil {
		t.Fatalf("the instance's fact: %v", err)
	}
	if err := writeIdentifier(t, p, claim, ua, ref); err != nil {
		t.Fatalf("the user's claim: %v", err)
	}

	for _, tc := range []struct {
		name  string
		owner string
		want  string
	}{
		{"the owner follows their own file", ua, claim},
		{"another user falls back to the instance", ub, fact},
		{"a caller with no user sees the fact alone", "", fact},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := p.FindInstrumentByIdentifier(ctx, tc.owner, ref.Type, ref.Domain, ref.Value)
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			if got != tc.want {
				t.Errorf("resolved to %s, want %s", got, tc.want)
			}
		})
	}
}

// A claim nothing else holds is invisible to everybody but its owner, rather
// than answering for the instance because there is no fact to prefer over it.
func TestFindInstrumentByIdentifier_AClaimAnswersForNobodyElse(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	ua, ub := aUser(t, p, "owner-f"), aUser(t, p, "owner-g")
	claim := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00OWNER009"}, Canonical: true})

	ref := db.InstrumentRef{Type: "BROKER_DESCRIPTION", Domain: "test", Value: "ACME CORP"}
	if err := writeIdentifier(t, p, claim, ua, ref); err != nil {
		t.Fatalf("the user's claim: %v", err)
	}
	for _, tc := range []struct{ name, owner, want string }{
		{"its owner", ua, claim},
		{"another user", ub, ""},
		{"a caller with no user", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := p.FindInstrumentByIdentifier(ctx, tc.owner, ref.Type, ref.Domain, ref.Value)
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			if got != tc.want {
				t.Errorf("resolved to %q, want %q", got, tc.want)
			}
		})
	}
}

// One resolution writes facts and claims together: what a plugin returned is the
// instance's, and the broker description appended to it is the uploader's.
func TestEnsureInstrument_EachIdentifierKeepsItsOwnOwner(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	u := aUser(t, p, "owner-h")

	id, _, err := p.EnsureInstrument(ctx, u, "STOCK", "USD", "Acme", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00OWNER010"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "BROKER_DESCRIPTION", Domain: "test", Value: "ACME CORP"}, Canonical: false, Owner: u},
	}, nil, "", nil, "")
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	row, err := p.GetInstrument(ctx, id)
	if err != nil || row == nil {
		t.Fatalf("get instrument: %v", err)
	}
	want := map[string]string{"ISIN": "", "BROKER_DESCRIPTION": u}
	for _, idn := range row.Identifiers {
		w, ok := want[idn.Ref.Type]
		if !ok {
			t.Errorf("unexpected identifier %s", idn.Ref.Type)
			continue
		}
		if idn.Owner != w {
			t.Errorf("%s owner = %q, want %q", idn.Ref.Type, idn.Owner, w)
		}
		delete(want, idn.Ref.Type)
	}
	for typ := range want {
		t.Errorf("%s was not stored", typ)
	}
}

// A merge moves a row, it does not settle it. Promoting the loser's claim on the
// way across would make a merge a second route to a fact nothing corroborated.
func TestMergeInstruments_TheOwnerTravelsWithTheName(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	u := aUser(t, p, "owner-i")
	a := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00OWNER011"}, Canonical: true})
	b := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "CUSIP", Value: "OWNER0011"}, Canonical: true})
	if err := writeIdentifier(t, p, b, u, db.InstrumentRef{Type: "BROKER_DESCRIPTION", Domain: "test", Value: "ACME CORP"}); err != nil {
		t.Fatalf("the user's claim: %v", err)
	}

	idns := []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00OWNER011"}, Canonical: true},
		{Ref: db.InstrumentRef{Type: "CUSIP", Value: "OWNER0011"}, Canonical: true},
	}
	survivor, _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", idns, oneClaim(idns...), "", nil, "")
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	if alive(t, p, a) && alive(t, p, b) {
		t.Fatal("nothing merged, so there is no move to check")
	}
	row, err := p.GetInstrument(ctx, survivor)
	if err != nil || row == nil {
		t.Fatalf("get survivor: %v", err)
	}
	var found bool
	for _, idn := range row.Identifiers {
		if idn.Ref.Type != "BROKER_DESCRIPTION" {
			continue
		}
		found = true
		if idn.Owner != u {
			t.Errorf("the description arrived owned by %q, want %q", idn.Owner, u)
		}
	}
	if !found {
		t.Error("the description did not travel to the survivor")
	}
}

// A file this export writes is imported back carrying system authority, so a
// claim travelling in one would be settled by the round trip.
func TestListInstrumentsForExport_LeavesAClaimBehind(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	u := aUser(t, p, "owner-j")
	known := ensureOne(t, p, "USD", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00OWNER012"}, Canonical: true})
	if err := writeIdentifier(t, p, known, u, db.InstrumentRef{Type: "BROKER_DESCRIPTION", Domain: "test", Value: "ACME CORP"}); err != nil {
		t.Fatalf("the user's claim: %v", err)
	}
	// An instrument no fact names at all: everything it is called is one user's.
	claimed := ensureOne(t, p, "USD", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00OWNER013"}, Canonical: true})
	if _, err := p.q.ExecContext(ctx, `UPDATE instrument_identifiers SET owner = $2 WHERE instrument_id = $1`, claimed, u); err != nil {
		t.Fatalf("claim every name: %v", err)
	}

	rows, err := p.ListInstrumentsForExport(ctx, "", nil)
	if err != nil {
		t.Fatalf("list for export: %v", err)
	}
	for _, r := range rows {
		if r.ID == claimed {
			t.Error("an instrument named only by a claim was exported")
		}
		if r.ID != known {
			continue
		}
		for _, idn := range r.Identifiers {
			if idn.Owner != "" {
				t.Errorf("%s was exported carrying owner %q", idn.Ref.Type, idn.Owner)
			}
		}
	}
}
