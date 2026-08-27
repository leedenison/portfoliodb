package identification

import (
	"testing"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// What a source states for itself is a claim when the source had the standing to
// make it, and the level travels on the claim because one resolution carries
// both: the plugin's answers, and the association the uploaded file asserted.
func TestStatedClaim(t *testing.T) {
	isin := identifier.Identifier{Type: "ISIN", Value: "GB00STATED01"}
	cusip := identifier.Identifier{Type: "CUSIP", Value: "STATED001"}

	for _, tc := range []struct {
		name  string
		ident identifier.Identity
		want  bool
	}{
		{
			// A broker file naming two identifiers on one row asserts they
			// denote one security, and it is the only channel that assertion
			// travels through.
			name:  "a user's file names two identifiers together",
			ident: identifier.Identity{Stated: []identifier.Identifier{isin, cusip}, StatedBy: "user-1"},
			want:  true,
		},
		{
			// The underlying of a derivative a plugin resolved. The plugin's
			// own claim already covers what it returned, and widening what the
			// system merges on is not this question.
			name:  "a plugin stated them",
			ident: identifier.Identity{Stated: []identifier.Identifier{isin, cusip}},
			want:  false,
		},
		{
			// One name associates nothing with anything.
			name:  "a user stated one identifier",
			ident: identifier.Identity{Stated: []identifier.Identifier{isin}, StatedBy: "user-1"},
			want:  false,
		},
		{
			// A proposal is tested by the plugins, never trusted ahead of them,
			// and a claim is where that distinction would be irrecoverable. See
			// adr/0057.
			name:  "a candidate plugin proposed them",
			ident: identifier.Identity{Proposed: []identifier.Identifier{isin, cusip}, StatedBy: "user-1"},
			want:  false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, ok := statedClaim(tc.ident)
			if ok != tc.want {
				t.Fatalf("statedClaim ok = %v, want %v", ok, tc.want)
			}
			if !ok {
				return
			}
			if c.Owner != tc.ident.StatedBy {
				t.Errorf("owner = %q, want %q", c.Owner, tc.ident.StatedBy)
			}
			for _, ci := range c.Identifiers {
				// Never returned. The value reaches the store so the merge site
				// can answer for the association, and flattenClaims writes
				// returned values alone -- a stated ISIN is stored when a plugin
				// gives it back, not because a file mentioned it.
				if ci.Role != db.ClaimRoleStated {
					t.Errorf("role = %q, want %q", ci.Role, db.ClaimRoleStated)
				}
			}
		})
	}
}

// The claim reaches the store and contributes nothing to what is written. That
// is the whole of what keeps adr/0060's "a stated hint is never written" true
// once the hint is also a claim.
func TestFlattenClaims_AStatedClaimWritesNothing(t *testing.T) {
	stated, ok := statedClaim(identifier.Identity{
		Stated: []identifier.Identifier{
			{Type: "ISIN", Value: "GB00STATED02"},
			{Type: "CUSIP", Value: "STATED002"},
		},
		StatedBy: "user-1",
	})
	if !ok {
		t.Fatal("statedClaim declined a user's pair")
	}
	got := flattenClaims(t.Context(), nil, []db.IdentityClaim{stated})
	if len(got) != 0 {
		t.Errorf("flattened %+v, want nothing written", got)
	}
}
