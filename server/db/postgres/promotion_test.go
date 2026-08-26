package postgres

import (
	"context"
	"testing"

	"github.com/leedenison/portfoliodb/server/db"
)

// The promotion sweep. A mapping is a triple and the instrument it names; users
// agree when they hold the same one and conflict when they do not, and only
// agreement is promoted. See
// docs/adr/0063-identity-claims-are-owned-until-users-corroborate-them.md.

// claimants gives n users the same description against one instrument, which is
// the shape every case below starts from.
func claimants(t *testing.T, p *Postgres, instrument, prefix string, n int) []string {
	t.Helper()
	ref := db.InstrumentRef{Type: "BROKER_DESCRIPTION", Domain: "test", Value: "ACME CORP"}
	ids := make([]string, n)
	for i := range ids {
		ids[i] = aUser(t, p, prefix+string(rune('a'+i)))
		if err := writeIdentifier(t, p, instrument, ids[i], ref); err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
	}
	return ids
}

// owners returns who holds one triple, "" standing for the instance.
func owners(t *testing.T, p *Postgres, value string) []string {
	t.Helper()
	var out []string
	if err := p.q.SelectContext(context.Background(), &out, `
		SELECT COALESCE(owner::text, '') FROM instrument_identifiers
		WHERE identifier_type = 'BROKER_DESCRIPTION' AND value = $1
		ORDER BY owner NULLS FIRST
	`, value); err != nil {
		t.Fatalf("read owners: %v", err)
	}
	return out
}

// The threshold is a count of users and the sweep promotes at it. One is the
// case that matters most: it is what a single-user instance runs at, and there
// the sweep promotes whatever a lone user's file said without waiting on
// anybody.
func TestPromotion_PromotesAtTheThreshold(t *testing.T) {
	for _, tc := range []struct {
		name      string
		users     int
		threshold int
		want      bool
	}{
		{"one user at a threshold of one", 1, 1, true},
		{"one user at a threshold of two", 1, 2, false},
		{"two users at a threshold of two", 2, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testDBTx(t)
			ctx := context.Background()
			inst := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00PROMO001"}, Canonical: true})
			claimants(t, p, inst, "promo-"+tc.name, tc.users)

			res, err := p.PromoteCorroboratedIdentifiers(ctx, tc.threshold)
			if err != nil {
				t.Fatalf("sweep: %v", err)
			}
			if (res.Promoted == 1) != tc.want {
				t.Fatalf("promoted %d, want %v", res.Promoted, tc.want)
			}
			got := owners(t, p, "ACME CORP")
			if tc.want {
				// The claims are deleted, not left beside the fact: a promoted
				// mapping resolves for everybody through the one row.
				if len(got) != 1 || got[0] != "" {
					t.Errorf("owners = %v, want the instance alone", got)
				}
				if res.ClaimsCleared != tc.users {
					t.Errorf("cleared %d claims, want %d", res.ClaimsCleared, tc.users)
				}
			} else if len(got) != tc.users {
				t.Errorf("owners = %v, want the %d claims left alone", got, tc.users)
			}
		})
	}
}

// A threshold counts users, not rows. One user with two brokers writing one
// description is one user, and counting rows would promote on their word alone.
func TestPromotion_CountsUsersRatherThanRows(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	inst := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00PROMO002"}, Canonical: true})
	u := aUser(t, p, "promo-one-user")
	for _, source := range []string{"broker-a", "broker-b"} {
		if err := writeIdentifier(t, p, inst, u, db.InstrumentRef{Type: "BROKER_DESCRIPTION", Domain: source, Value: "ACME CORP"}); err != nil {
			t.Fatalf("claim under %s: %v", source, err)
		}
	}
	res, err := p.PromoteCorroboratedIdentifiers(ctx, 2)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Promoted != 0 {
		t.Errorf("promoted %d, want nothing: two rows are one user", res.Promoted)
	}
}

// Where users disagree the sweep promotes neither answer and leaves both rows in
// place, each still resolving for its own owner. Deciding between them is a
// person's, on the surface 0168 builds.
func TestPromotion_LeavesADisagreementAlone(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	a := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00PROMO003"}, Canonical: true})
	b := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00PROMO004"}, Canonical: true})
	ua := claimants(t, p, a, "promo-agree-", 2)
	ub := claimants(t, p, b, "promo-differ-", 1)

	res, err := p.PromoteCorroboratedIdentifiers(ctx, 1)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Promoted != 0 || res.ClaimsCleared != 0 {
		t.Errorf("promoted %d and cleared %d, want neither: the users disagree", res.Promoted, res.ClaimsCleared)
	}
	if got := len(owners(t, p, "ACME CORP")); got != len(ua)+len(ub) {
		t.Errorf("%d rows left, want all %d", got, len(ua)+len(ub))
	}
	contested, err := p.CountUnpromotableClaims(ctx)
	if err != nil {
		t.Fatalf("count contested: %v", err)
	}
	if contested != 1 {
		t.Errorf("contested = %d, want the one triple they disagree about", contested)
	}
}

// A fact naming the same instrument agrees with the claims under it, so they are
// deleted and nothing is written: the mapping is already the instance's, and a
// second row for one triple is what the exclusion constraint forbids.
func TestPromotion_ClearsAClaimAFactHasOvertaken(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	inst := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00PROMO005"}, Canonical: true})
	claimants(t, p, inst, "promo-overtaken-", 1)
	if err := writeIdentifier(t, p, inst, "", db.InstrumentRef{Type: "BROKER_DESCRIPTION", Domain: "test", Value: "ACME CORP"}); err != nil {
		t.Fatalf("the instance's fact: %v", err)
	}

	res, err := p.PromoteCorroboratedIdentifiers(ctx, 1)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Promoted != 0 || res.AlreadyHeld != 1 || res.ClaimsCleared != 1 {
		t.Errorf("promoted %d, already held %d, cleared %d; want 0, 1, 1",
			res.Promoted, res.AlreadyHeld, res.ClaimsCleared)
	}
	if got := owners(t, p, "ACME CORP"); len(got) != 1 || got[0] != "" {
		t.Errorf("owners = %v, want the instance alone", got)
	}
}

// A fact naming a different instrument is the instance and one of its users
// disagreeing. Promoting would fail the exclusion constraint besides, but the
// reason it is refused is that nothing here can say which of them is right.
func TestPromotion_DoesNotOverruleTheInstance(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	a := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00PROMO006"}, Canonical: true})
	b := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00PROMO007"}, Canonical: true})
	claimants(t, p, a, "promo-vs-instance-", 2)
	if err := writeIdentifier(t, p, b, "", db.InstrumentRef{Type: "BROKER_DESCRIPTION", Domain: "test", Value: "ACME CORP"}); err != nil {
		t.Fatalf("the instance's fact: %v", err)
	}

	res, err := p.PromoteCorroboratedIdentifiers(ctx, 1)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Promoted != 0 || res.ClaimsCleared != 0 {
		t.Errorf("promoted %d and cleared %d, want neither", res.Promoted, res.ClaimsCleared)
	}
	if got := len(owners(t, p, "ACME CORP")); got != 3 {
		t.Errorf("%d rows left, want the instance's and both claims", got)
	}
}

// What the promoted row is worth after the sweep: everybody resolves through it,
// including a user who never held it, and the owner-scoped lookup that used to
// answer from a claim now answers from the fact.
func TestPromotion_APromotedMappingResolvesForEverybody(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	inst := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00PROMO008"}, Canonical: true})
	holders := claimants(t, p, inst, "promo-shared-", 2)
	stranger := aUser(t, p, "promo-stranger")

	if before, err := p.FindInstrumentByIdentifier(ctx, stranger, "BROKER_DESCRIPTION", "test", "ACME CORP"); err != nil {
		t.Fatalf("lookup before: %v", err)
	} else if before != "" {
		t.Fatalf("a stranger resolved %s before the promotion", before)
	}
	if _, err := p.PromoteCorroboratedIdentifiers(ctx, 2); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	for _, who := range append([]string{"", stranger}, holders...) {
		got, err := p.FindInstrumentByIdentifier(ctx, who, "BROKER_DESCRIPTION", "test", "ACME CORP")
		if err != nil {
			t.Fatalf("lookup as %q: %v", who, err)
		}
		if got != inst {
			t.Errorf("as %q resolved to %q, want %s", who, got, inst)
		}
	}
}

// The promoted row takes the union of the intervals it was promoted from, which
// is contiguous because they overlap -- a triple two owners hold over disjoint
// intervals is two mappings rather than one. A NULL bound on either side is
// unbounded and absorbs the rest.
func TestPromotion_ThePromotedRowSpansWhatItWasPromotedFrom(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	inst := ensureOne(t, p, "", db.IdentifierInput{Ref: db.InstrumentRef{Type: "ISIN", Value: "GB00PROMO009"}, Canonical: true})
	ua, ub := aUser(t, p, "promo-span-a"), aUser(t, p, "promo-span-b")
	for _, w := range []struct {
		owner, from, before string
	}{
		{ua, "2020-01-01", "2024-01-01"},
		{ub, "2022-01-01", "2026-01-01"},
	} {
		if _, err := p.q.ExecContext(ctx, `
			INSERT INTO instrument_identifiers
				(instrument_id, identifier_type, domain, value, canonical, owner, valid_from, valid_before)
			VALUES ($1, 'BROKER_DESCRIPTION', 'test', 'ACME CORP', false, $2, $3::date, $4::date)
		`, inst, w.owner, w.from, w.before); err != nil {
			t.Fatalf("claim by %s: %v", w.owner, err)
		}
	}

	if _, err := p.PromoteCorroboratedIdentifiers(ctx, 2); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	var from, before string
	if err := p.q.QueryRowContext(ctx, `
		SELECT to_char(valid_from, 'YYYY-MM-DD'), to_char(valid_before, 'YYYY-MM-DD')
		FROM instrument_identifiers
		WHERE identifier_type = 'BROKER_DESCRIPTION' AND value = 'ACME CORP' AND owner IS NULL
	`).Scan(&from, &before); err != nil {
		t.Fatalf("read the promoted row: %v", err)
	}
	if from != "2020-01-01" || before != "2026-01-01" {
		t.Errorf("promoted interval [%s, %s), want [2020-01-01, 2026-01-01)", from, before)
	}
}

// A threshold below one is an error rather than a clamp. It would promote a
// mapping nobody holds, and the sweep must not substitute a number the admin did
// not choose.
func TestPromotion_RefusesAThresholdBelowOne(t *testing.T) {
	p := testDBTx(t)
	if _, err := p.PromoteCorroboratedIdentifiers(context.Background(), 0); err == nil {
		t.Error("a threshold of zero was accepted, want an error")
	}
}
