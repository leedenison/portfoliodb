package grouping

import (
	"context"
	"errors"
	"testing"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// store answers the reader's methods from a fixed set of postings, the way the
// database does, and counts what it was asked so a test can state the questions
// rather than only the answers.
//
// No dispatch: each method knows the one shape it answers. That is the point of the
// reader -- a fake is three small functions rather than a switch over a kind.
type store struct {
	all   []db.GroupingPosting
	reads int
	dates [][]db.DateQuery
	err   error
	// echo returns postings the caller already holds, which a correct store never
	// does. It is here to show the closure terminating regardless.
	echo bool
}

func (s *store) filter(held []string, keep func(db.GroupingPosting) bool) ([]db.GroupingPosting, error) {
	s.reads++
	if s.err != nil {
		return nil, s.err
	}
	heldSet := map[string]bool{}
	for _, id := range held {
		heldSet[id] = true
	}
	var out []db.GroupingPosting
	for _, p := range s.all {
		if !s.echo && heldSet[p.ID] {
			continue
		}
		if keep(p) {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *store) PostingsByToken(_ context.Context, _ string, qs []db.TokenQuery, held []string) ([]db.GroupingPosting, error) {
	return s.filter(held, func(p db.GroupingPosting) bool {
		for _, q := range qs {
			for _, c := range p.Correlations {
				if c.Label == q.Label && c.Token == q.Token && (q.AnyAccount || p.Account == q.Account) {
					return true
				}
			}
		}
		return false
	})
}

func (s *store) PostingsByDates(_ context.Context, _ string, qs []db.DateQuery, held []string) ([]db.GroupingPosting, error) {
	s.dates = append(s.dates, qs)
	return s.filter(held, func(p db.GroupingPosting) bool {
		for _, q := range qs {
			if p.Account == q.Account && !p.Timestamp.Before(q.From) && p.Timestamp.Before(q.Before) {
				return true
			}
		}
		return false
	})
}

func (s *store) PostingsByOrdinals(_ context.Context, _ string, qs []db.OrdinalQuery, held []string) ([]db.GroupingPosting, error) {
	return s.filter(held, func(p db.GroupingPosting) bool {
		for _, q := range qs {
			if p.Account != q.Account {
				continue
			}
			for _, c := range p.Correlations {
				if c.Label == q.Label && c.Ordinal != nil && *c.Ordinal >= q.Low && *c.Ordinal <= q.High {
					return true
				}
			}
		}
		return false
	})
}

func heldIDs(ps []db.GroupingPosting) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

func idsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A token links two postings, and pulling in the second asks the second's own
// questions -- which is what makes this a closure rather than one round of lookups.
func TestNeighbourhood_GrowsUntilNothingIsNew(t *testing.T) {
	// a shares a token with b, b shares a different token with c, and c reaches
	// nothing further. One round of lookups from a would stop at b.
	a := correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "t1", db.ScopeAccount, db.MatchExact)
	b := correlated(correlated(posting("b", typev1.TxType_TRADE_CASH), "", "t1", db.ScopeAccount, db.MatchExact),
		"", "t2", db.ScopeAccount, db.MatchExact)
	c := correlated(posting("c", typev1.TxType_TRANSACTION_COST), "", "t2", db.ScopeAccount, db.MatchExact)
	s := &store{all: []db.GroupingPosting{a, b, c}}

	got, err := Neighbourhood(context.Background(), "u", []db.GroupingPosting{a}, []Rule{Exact{}}, s)
	if err != nil {
		t.Fatalf("neighbourhood: %v", err)
	}
	if want := []string{"a", "b", "c"}; !idsEqual(heldIDs(got), want) {
		t.Fatalf("held = %v, want %v", heldIDs(got), want)
	}
	// Three reads: one that finds b, one that finds c, one that finds nothing and
	// ends it.
	if s.reads != 3 {
		t.Fatalf("reads = %d, want 3", s.reads)
	}
}

func TestNeighbourhood_StopsWhenNothingReachesFurther(t *testing.T) {
	a := correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "t1", db.ScopeAccount, db.MatchExact)
	lonely := correlated(posting("z", typev1.TxType_HOLDING_COST), "", "other", db.ScopeAccount, db.MatchExact)
	s := &store{all: []db.GroupingPosting{a, lonely}}

	got, err := Neighbourhood(context.Background(), "u", []db.GroupingPosting{a}, []Rule{Exact{}}, s)
	if err != nil {
		t.Fatalf("neighbourhood: %v", err)
	}
	if want := []string{"a"}; !idsEqual(heldIDs(got), want) {
		t.Fatalf("held = %v, want %v", heldIDs(got), want)
	}
}

// The closure only ever adds, and it counts a round by what the caller did not
// already hold. So a store that returns everything every time still terminates:
// termination is this function's property rather than a promise made elsewhere.
func TestNeighbourhood_TerminatesWhenAStoreEchoesHeldPostings(t *testing.T) {
	a := correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "t1", db.ScopeAccount, db.MatchExact)
	b := correlated(posting("b", typev1.TxType_TRADE_CASH), "", "t1", db.ScopeAccount, db.MatchExact)
	s := &store{all: []db.GroupingPosting{a, b}, echo: true}

	got, err := Neighbourhood(context.Background(), "u", []db.GroupingPosting{a}, []Rule{Exact{}}, s)
	if err != nil {
		t.Fatalf("neighbourhood: %v", err)
	}
	if want := []string{"a", "b"}; !idsEqual(heldIDs(got), want) {
		t.Fatalf("held = %v, want %v", heldIDs(got), want)
	}
}

// A posting is never asked for twice: the exclusion list carries everything held, so
// a round costs one query per distinct question rather than per posting asking it.
func TestNeighbourhood_ExcludesWhatItAlreadyHolds(t *testing.T) {
	a := correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "t1", db.ScopeAccount, db.MatchExact)
	b := correlated(posting("b", typev1.TxType_TRADE_CASH), "", "t1", db.ScopeAccount, db.MatchExact)
	s := &store{all: []db.GroupingPosting{a, b}}

	if _, err := Neighbourhood(context.Background(), "u", []db.GroupingPosting{a}, []Rule{Exact{}}, s); err != nil {
		t.Fatalf("neighbourhood: %v", err)
	}
	if s.reads < 2 {
		t.Fatalf("reads = %d, want at least 2", s.reads)
	}
}

// Every posting of one account on one day asks the same date question, so a frontier
// of several asks it once. Without this the fetch would grow with the frontier
// rather than with what is actually being asked.
func TestNeighbourhood_AsksEachDistinctQuestionOnce(t *testing.T) {
	seed := []db.GroupingPosting{
		security("a", "-100", "10", "1000", 10, 500000100),
		security("b", "-100", "10", "1000", 10, 500000101),
		security("c", "-100", "10", "1000", 10, 500000102),
	}
	s := &store{}

	if _, err := Neighbourhood(context.Background(), "u", seed, []Rule{Disposal()}, s); err != nil {
		t.Fatalf("neighbourhood: %v", err)
	}
	if len(s.dates) == 0 {
		t.Fatal("no date questions asked")
	}
	// One account, one day, three postings: one date query.
	if got := len(s.dates[0]); got != 1 {
		t.Fatalf("first read asked %d date queries, want 1", got)
	}
}

func TestNeighbourhood_SurfacesAFetchError(t *testing.T) {
	a := correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "t1", db.ScopeAccount, db.MatchExact)
	want := errors.New("read failed")
	s := &store{all: []db.GroupingPosting{a}, err: want}

	if _, err := Neighbourhood(context.Background(), "u", []db.GroupingPosting{a}, []Rule{Exact{}}, s); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}
