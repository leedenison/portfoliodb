package grouping

import (
	"fmt"
	"sort"
	"strings"

	"github.com/leedenison/portfoliodb/server/db"
)

// Divergence is how a derived partition differs from the one already stored.
//
// Counted in groups rather than postings, because a partition is a set of sets and
// the question a shadow run asks is which of them the engine would have drawn
// differently. A group is Agreed when its membership is exactly a stored group's;
// anything else is a disagreement, whether the engine joined two stored groups, split
// one, or moved a single posting between them.
type Divergence struct {
	// Groups is how many the engine derived, Stored how many it found.
	Groups int
	Stored int
	// Agreed is the number of derived groups whose membership matches a stored
	// group exactly.
	Agreed int
	// Joined is derived groups holding postings from more than one stored group,
	// and Split is stored groups whose postings the engine put in more than one.
	// A regroup is usually both at once, so they overlap and do not sum.
	Joined int
	Split  int
	// Examples names a few disagreements, so a report says which rather than only
	// how many. Bounded, because a first shadow run over a corpus the engine has
	// never seen can disagree about everything.
	Examples []string
}

// Agrees reports whether the engine drew exactly the partition already stored, which
// is the bar a shadow run has to clear before anything depends on it.
func (d Divergence) Agrees() bool {
	return d.Groups == d.Stored && d.Agreed == d.Groups
}

// maxExamples bounds what a divergence carries. Enough to see the shape of a
// disagreement in a log line, few enough that a corpus-wide mismatch does not
// produce a corpus-sized message.
const maxExamples = 5

// Compare measures a derived partition against the one the postings already sit in.
//
// The stored partition is the converters' answer, which is what makes this the
// validation 0097 asks for: the engine has to reproduce it before it is allowed to
// replace it. It is not an oracle, though. The engine may disagree because it is
// right -- a converter's wrong pairing of two similar trades balances and looks
// settled -- so a divergence is something to look at rather than a failure to count.
func Compare(ps []db.GroupingPosting, gs []Group) Divergence {
	storedOf := make(map[string]string, len(ps))
	storedSize := map[string]int{}
	for _, p := range ps {
		storedOf[p.ID] = p.GroupID
		storedSize[p.GroupID]++
	}

	d := Divergence{Groups: len(gs), Stored: len(storedSize)}
	// Which stored groups each derived group drew from, so a derived group that
	// joined two is visible, and a stored group drawn into several is too.
	derivedPerStored := map[string]map[int]bool{}
	for i, g := range gs {
		seen := map[string]bool{}
		for _, m := range g.Members {
			s := storedOf[m.ID]
			seen[s] = true
			if derivedPerStored[s] == nil {
				derivedPerStored[s] = map[int]bool{}
			}
			derivedPerStored[s][i] = true
		}
		switch {
		case len(seen) > 1:
			d.Joined++
			d.note(fmt.Sprintf("joined %d stored groups into %s", len(seen), memberList(g)))
		case len(seen) == 1 && storedSize[only(seen)] == len(g.Members):
			d.Agreed++
		default:
			d.note(fmt.Sprintf("took %s out of a stored group of %d", memberList(g), storedSize[only(seen)]))
		}
	}
	for _, derived := range derivedPerStored {
		if len(derived) > 1 {
			d.Split++
		}
	}
	return d
}

func (d *Divergence) note(s string) {
	if len(d.Examples) < maxExamples {
		d.Examples = append(d.Examples, s)
	}
}

// only returns the single key of a one-element set.
func only(m map[string]bool) string {
	for k := range m {
		return k
	}
	return ""
}

// memberList names a group by its postings, ordered, so two runs describe the same
// disagreement the same way.
func memberList(g Group) string {
	ids := make([]string, 0, len(g.Members))
	for _, m := range g.Members {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return "[" + strings.Join(ids, " ") + "]"
}
