package grouping

import (
	"testing"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// stored puts a posting in a group, which is what a shadow run measures against.
func stored(p db.GroupingPosting, groupID string) db.GroupingPosting {
	p.GroupID = groupID
	return p
}

// derived builds a partition by hand, so a test states the two sides separately
// rather than deriving one from the other.
func derived(groups ...[]string) []Group {
	out := make([]Group, 0, len(groups))
	for _, ids := range groups {
		g := Group{}
		for _, id := range ids {
			g.Members = append(g.Members, Member{ID: id, Resolved: typev1.TxType_TRADE_CASH})
		}
		out = append(out, g)
	}
	return out
}

func TestCompare(t *testing.T) {
	ps := []db.GroupingPosting{
		stored(posting("a", typev1.TxType_TRADE_ASSET), "g1"),
		stored(posting("b", typev1.TxType_TRADE_CASH), "g1"),
		stored(posting("c", typev1.TxType_TRADE_ASSET), "g2"),
		stored(posting("d", typev1.TxType_TRADE_CASH), "g2"),
	}
	tests := []struct {
		name   string
		gs     []Group
		agrees bool
		agreed int
		joined int
		split  int
	}{
		{
			name:   "agrees when the partition is the stored one",
			gs:     derived([]string{"a", "b"}, []string{"c", "d"}),
			agrees: true,
			agreed: 2,
		},
		{
			// The engine drew one group where the converters drew two.
			name:   "counts a join",
			gs:     derived([]string{"a", "b", "c", "d"}),
			joined: 1,
			split:  0,
		},
		{
			// And the other way: what the converters held together, the engine
			// left apart. Both stored groups are split.
			name:  "counts a split",
			gs:    derived([]string{"a"}, []string{"b"}, []string{"c"}, []string{"d"}),
			split: 2,
		},
		{
			// A regroup is usually both at once, which is why the two do not sum
			// to the number of disagreements.
			name:   "counts a move as both",
			gs:     derived([]string{"a", "b", "c"}, []string{"d"}),
			joined: 1,
			split:  1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := Compare(ps, tc.gs)
			if d.Agrees() != tc.agrees {
				t.Fatalf("Agrees() = %v, want %v (%+v)", d.Agrees(), tc.agrees, d)
			}
			if d.Agreed != tc.agreed {
				t.Fatalf("agreed = %d, want %d", d.Agreed, tc.agreed)
			}
			if d.Joined != tc.joined {
				t.Fatalf("joined = %d, want %d", d.Joined, tc.joined)
			}
			if d.Split != tc.split {
				t.Fatalf("split = %d, want %d", d.Split, tc.split)
			}
		})
	}
}

// A first run over a corpus the engine has never seen can disagree about
// everything, and a report of that size is not a report.
func TestCompare_BoundsItsExamples(t *testing.T) {
	var ps []db.GroupingPosting
	var groups [][]string
	for i := range 40 {
		id := string(rune('a' + i%26))
		id += string(rune('0' + i/26))
		ps = append(ps,
			stored(posting(id+"x", typev1.TxType_TRADE_ASSET), "g"+id),
			stored(posting(id+"y", typev1.TxType_TRADE_CASH), "g"+id),
		)
		// Each derived group takes one leg out of a stored pair, so every one of
		// them disagrees.
		groups = append(groups, []string{id + "x"})
	}
	d := Compare(ps, derived(groups...))
	if d.Agrees() {
		t.Fatal("Agrees() = true, want a disagreement")
	}
	if len(d.Examples) != maxExamples {
		t.Fatalf("examples = %d, want %d", len(d.Examples), maxExamples)
	}
}

// The count is of groups rather than postings, so a partition and the stored one are
// compared as sets of sets.
func TestCompare_CountsGroupsNotPostings(t *testing.T) {
	ps := []db.GroupingPosting{
		stored(posting("a", typev1.TxType_TRADE_ASSET), "g1"),
		stored(posting("b", typev1.TxType_TRADE_CASH), "g1"),
		stored(posting("c", typev1.TxType_HOLDING_COST), "g2"),
	}
	d := Compare(ps, derived([]string{"a", "b"}, []string{"c"}))
	if d.Groups != 2 || d.Stored != 2 {
		t.Fatalf("groups = %d, stored = %d, want 2 and 2", d.Groups, d.Stored)
	}
	if !d.Agrees() {
		t.Fatalf("Agrees() = false, want true (%+v)", d)
	}
}
