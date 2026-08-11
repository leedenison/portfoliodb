package grouping

import (
	"math/rand"
	"testing"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
)

// posting builds a transcribed posting with the fields the engine reads. The
// declared set is the one thing every rule consults, so it is required; everything
// else is set by the helpers below where a test depends on it.
func posting(id string, declared ...typev1.TxType) db.GroupingPosting {
	return db.GroupingPosting{
		ID:       id,
		UserID:   "u",
		Broker:   typev1.Broker_FIDELITY,
		Account:  "A1",
		JobID:    "job1",
		Quantity: decimal.New(1, 0),
		Declared: declared,
	}
}

// correlated stamps a token on a posting, in the shape a converter emits: a series,
// the identifier verbatim, and what may be compared about it.
func correlated(p db.GroupingPosting, label, token, scope string, match ...string) db.GroupingPosting {
	p.Correlations = append(p.Correlations, db.Correlation{
		Label: label,
		Token: token,
		Scope: scope,
		Match: match,
	})
	return p
}

// members reads a partition out as a comparable shape: one sorted id list per group,
// itself sorted, so a test states the partition and not the order it was built in.
func members(gs []Group) [][]string {
	out := make([][]string, 0, len(gs))
	for _, g := range gs {
		ids := make([]string, 0, len(g.Members))
		for _, m := range g.Members {
			ids = append(ids, m.ID)
		}
		out = append(out, ids)
	}
	return out
}

func equal(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

// resolvedOf finds what a posting resolved to, whichever group it landed in.
func resolvedOf(gs []Group, id string) typev1.TxType {
	for _, g := range gs {
		for _, m := range g.Members {
			if m.ID == id {
				return m.Resolved
			}
		}
	}
	return typev1.TxType_TX_TYPE_UNSPECIFIED
}

func TestPartition_ExactToken(t *testing.T) {
	tests := []struct {
		name string
		ps   []db.GroupingPosting
		want [][]string
	}{
		{
			// The OFX case: the parser stamps the containing INVTRAN's FITID on
			// every leg it produces, so the source states the grouping and the
			// engine honours it rather than re-deriving it by inference.
			name: "groups the legs a source named with one token",
			ps: []db.GroupingPosting{
				correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "fit1", db.ScopeAccount, db.MatchExact),
				correlated(posting("b", typev1.TxType_TRADE_CASH), "", "fit1", db.ScopeAccount, db.MatchExact),
			},
			want: [][]string{{"a", "b"}},
		},
		{
			name: "leaves a posting whose token nothing shares on its own",
			ps: []db.GroupingPosting{
				correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "fit1", db.ScopeAccount, db.MatchExact),
				correlated(posting("b", typev1.TxType_TRADE_CASH), "", "fit2", db.ScopeAccount, db.MatchExact),
			},
			want: [][]string{{"a"}, {"b"}},
		},
		{
			// A Fidelity reference declares EXACT as well as ORDINAL, and a trade
			// row and its cash row carry different references. Nothing should be
			// grouped by equality that the source did not give the same number.
			name: "does not group two different references",
			ps: []db.GroupingPosting{
				correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "795832439", db.ScopeFile, db.MatchExact, db.MatchOrdinal),
				correlated(posting("b", typev1.TxType_TRADE_CASH), "", "795832440", db.ScopeFile, db.MatchExact, db.MatchOrdinal),
			},
			want: [][]string{{"a"}, {"b"}},
		},
		{
			// The label is the comparability partition, so a counterparty pointer
			// is never compared against a reference number even where the two
			// strings coincide.
			name: "does not compare across series",
			ps: []db.GroupingPosting{
				correlated(posting("a", typev1.TxType_TRANSFER), "", "AG1", db.ScopeBroker, db.MatchExact),
				correlated(posting("b", typev1.TxType_TRANSFER), "counterparty", "AG1", db.ScopeBroker, db.MatchExact),
			},
			want: [][]string{{"a"}, {"b"}},
		},
		{
			// A pointer names another posting's account rather than a token that
			// posting carries, so equality on it could never fire and declaring
			// only ACCOUNT keeps this rule away from it.
			name: "ignores a correlation that does not declare equality",
			ps: []db.GroupingPosting{
				correlated(posting("a", typev1.TxType_TRANSFER), "counterparty", "AG1", db.ScopeBroker, db.MatchAccount),
				correlated(posting("b", typev1.TxType_TRANSFER), "counterparty", "AG1", db.ScopeBroker, db.MatchAccount),
			},
			want: [][]string{{"a"}, {"b"}},
		},
		{
			// A file has no identity once its postings are rows, so a FILE-scoped
			// token means nothing outside the job that supplied it. Two uploads
			// reusing a reference series must not collide.
			name: "keeps a file-scoped token inside its own job",
			ps: []db.GroupingPosting{
				correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "1", db.ScopeFile, db.MatchExact),
				correlated(func() db.GroupingPosting {
					p := posting("b", typev1.TxType_TRADE_CASH)
					p.JobID = "job2"
					return p
				}(), "", "1", db.ScopeFile, db.MatchExact),
			},
			want: [][]string{{"a"}, {"b"}},
		},
		{
			// An OFX FITID is unique within the account, not within the
			// institution, so reading it across accounts invents a pairing.
			name: "keeps an account-scoped token inside its own account",
			ps: []db.GroupingPosting{
				correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "fit1", db.ScopeAccount, db.MatchExact),
				correlated(func() db.GroupingPosting {
					p := posting("b", typev1.TxType_TRADE_CASH)
					p.Account = "A2"
					return p
				}(), "", "fit1", db.ScopeAccount, db.MatchExact),
			},
			want: [][]string{{"a"}, {"b"}},
		},
		{
			// A broker-scoped token is this user's data for this broker, which is
			// wider than one account and is what a transfer between two of the
			// user's own accounts needs.
			name: "carries a broker-scoped token across accounts",
			ps: []db.GroupingPosting{
				correlated(posting("a", typev1.TxType_TRANSFER), "", "mv1", db.ScopeBroker, db.MatchExact),
				correlated(func() db.GroupingPosting {
					p := posting("b", typev1.TxType_TRANSFER)
					p.Account = "A2"
					return p
				}(), "", "mv1", db.ScopeBroker, db.MatchExact),
			},
			want: [][]string{{"a", "b"}},
		},
		{
			name: "groups three legs a source named together",
			ps: []db.GroupingPosting{
				correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "fit1", db.ScopeAccount, db.MatchExact),
				correlated(posting("b", typev1.TxType_TRADE_CASH), "", "fit1", db.ScopeAccount, db.MatchExact),
				correlated(posting("c", typev1.TxType_TRANSACTION_COST), "", "fit1", db.ScopeAccount, db.MatchExact),
			},
			want: [][]string{{"a", "b", "c"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := members(Partition(tc.ps, DefaultRules(), DefaultOpts()))
			if !equal(got, tc.want) {
				t.Fatalf("partition = %v, want %v", got, tc.want)
			}
		})
	}
}

// A shared identifier says these rows are one event. It says nothing about what kind
// of event, so each member keeps what its own declaration narrows to rather than
// being retyped by a rule that has no view.
func TestPartition_ExactTokenKeepsEachDeclaration(t *testing.T) {
	ps := []db.GroupingPosting{
		correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "fit1", db.ScopeAccount, db.MatchExact),
		correlated(posting("b", typev1.TxType_DIVIDEND, typev1.TxType_INTEREST), "", "fit1", db.ScopeAccount, db.MatchExact),
	}
	gs := Partition(ps, DefaultRules(), DefaultOpts())

	if got := resolvedOf(gs, "a"); got != typev1.TxType_TRADE_ASSET {
		t.Fatalf("a resolved to %v, want TRADE_ASSET", got)
	}
	// Siblings under one branch collapse to the branch, which keeps what was known
	// rather than discarding it.
	if got := resolvedOf(gs, "b"); got != typev1.TxType_INCOME {
		t.Fatalf("b resolved to %v, want INCOME", got)
	}
}

// A posting no rule claims is a group of one, resolved to the common ancestor of
// its declared set -- AMBIGUOUS where the set spans branches, which is a different
// claim from the field not being set.
func TestPartition_ResolvesWhatNothingClaims(t *testing.T) {
	ps := []db.GroupingPosting{
		posting("a", typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER),
		posting("b", typev1.TxType_HOLDING_COST),
	}
	gs := Partition(ps, DefaultRules(), DefaultOpts())

	if got := members(gs); !equal(got, [][]string{{"a"}, {"b"}}) {
		t.Fatalf("partition = %v, want each on its own", got)
	}
	if got := resolvedOf(gs, "a"); got != typev1.TxType_AMBIGUOUS {
		t.Fatalf("a resolved to %v, want AMBIGUOUS", got)
	}
	if got := resolvedOf(gs, "b"); got != typev1.TxType_HOLDING_COST {
		t.Fatalf("b resolved to %v, want HOLDING_COST", got)
	}
}

// The engine is handed a neighbourhood, not a stream, so the order postings arrive
// in must not reach the answer. This is the property that lets a caller read them in
// whatever order an index returns.
func TestPartition_IsOrderIndependent(t *testing.T) {
	ps := []db.GroupingPosting{
		correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "fit1", db.ScopeAccount, db.MatchExact),
		correlated(posting("b", typev1.TxType_TRADE_CASH), "", "fit1", db.ScopeAccount, db.MatchExact),
		correlated(posting("c", typev1.TxType_TRADE_ASSET), "", "fit2", db.ScopeAccount, db.MatchExact),
		correlated(posting("d", typev1.TxType_TRADE_CASH), "", "fit2", db.ScopeAccount, db.MatchExact),
		posting("e", typev1.TxType_HOLDING_COST),
	}
	want := members(Partition(ps, DefaultRules(), DefaultOpts()))

	r := rand.New(rand.NewSource(1))
	for i := range 20 {
		shuffled := make([]db.GroupingPosting, len(ps))
		copy(shuffled, ps)
		r.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		if got := members(Partition(shuffled, DefaultRules(), DefaultOpts())); !equal(got, want) {
			t.Fatalf("shuffle %d: partition = %v, want %v", i, got, want)
		}
	}
}

// Precedence is a number the rule carries, so the caller may hand the rules over in
// any order -- a per-broker ordering is a table rather than a call sequence.
func TestPartition_RuleOrderDoesNotMatter(t *testing.T) {
	ps := []db.GroupingPosting{
		correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "fit1", db.ScopeAccount, db.MatchExact),
		correlated(posting("b", typev1.TxType_TRADE_CASH), "", "fit1", db.ScopeAccount, db.MatchExact),
	}
	forward := DefaultRules()
	reversed := make([]Rule, len(forward))
	for i, r := range forward {
		reversed[len(forward)-1-i] = r
	}
	if got, want := members(Partition(ps, reversed, DefaultOpts())), members(Partition(ps, forward, DefaultOpts())); !equal(got, want) {
		t.Fatalf("reversed rules = %v, want %v", got, want)
	}
}
