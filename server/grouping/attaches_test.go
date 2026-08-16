package grouping

import (
	"math/rand"
	"testing"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// pointsAt stamps the pointer a converter emits: the token names another posting's
// identifier, and the bearer attaches to whatever group that posting lands in.
func pointsAt(p db.GroupingPosting, label, token, scope string) db.GroupingPosting {
	p.Correlations = append(p.Correlations, db.Correlation{
		Label: label,
		Token: token,
		Scope: scope,
		Match: []string{db.MatchAttaches},
	})
	return p
}

// charge builds a per-trade charge: money out, declared as a transaction cost, with
// a reference of its own in the series the broker numbers charges in.
func charge(id, qty string, d int, ref int64) db.GroupingPosting {
	return money(id, qty, d, ref, typev1.TxType_TRANSACTION_COST)
}

// The whole point of the rule: the charge joins the trade its converter named, and
// the trade still pairs with the cash that settled it.
func TestAttaches_JoinsTheTradeItNames(t *testing.T) {
	asset := security("a", "28", "263.58", "7390.19", 10, 563466632)
	cash := money("b", "-7380.19", 10, 563466631, typev1.TxType_TRADE_CASH)
	fee := pointsAt(charge("c", "-10", 8, 167614596), "", "563466632", db.ScopeFile)

	got := members(Partition([]db.GroupingPosting{asset, cash, fee}, DefaultRules(), DefaultOpts()))
	if want := [][]string{{"a", "b", "c"}}; !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// The reason the rule runs last. Exact claims every posting sharing a token at
// precedence 1000, so a pointer expressed as a shared token would take the asset leg
// before the trade rules could pair it, and the cash row would be stranded. A
// MATCH_ATTACHES correlation is invisible to Exact, which is what keeps that from
// happening -- assert the cash leg is still in the group.
func TestAttaches_DoesNotStrandTheCashLeg(t *testing.T) {
	asset := security("a", "28", "263.58", "7390.19", 10, 563466632)
	cash := money("b", "-7380.19", 10, 563466631, typev1.TxType_TRADE_CASH)
	fee := pointsAt(charge("c", "-10", 8, 167614596), "", "563466632", db.ScopeFile)

	for _, g := range Partition([]db.GroupingPosting{asset, cash, fee}, DefaultRules(), DefaultOpts()) {
		var hasAsset, hasCash bool
		for _, m := range g.Members {
			hasAsset = hasAsset || m.ID == "a"
			hasCash = hasCash || m.ID == "b"
		}
		if hasAsset && !hasCash {
			t.Fatalf("asset leg grouped without its cash leg: %v", members([]Group{g}))
		}
	}
}

// A pointer says where a posting belongs, not what it is, so the bearer keeps
// whatever its own declaration narrows to.
func TestAttaches_LeavesTheBearersOwnTypeAlone(t *testing.T) {
	asset := security("a", "28", "263.58", "7390.19", 10, 563466632)
	cash := money("b", "-7380.19", 10, 563466631, typev1.TxType_TRADE_CASH)
	fee := pointsAt(charge("c", "-10", 8, 167614596), "", "563466632", db.ScopeFile)

	gs := Partition([]db.GroupingPosting{asset, cash, fee}, DefaultRules(), DefaultOpts())
	if got := resolvedOf(gs, "c"); got != typev1.TxType_TRANSACTION_COST {
		t.Fatalf("charge resolved to %v, want TRANSACTION_COST", got)
	}
}

// A token nobody carries is a pointer at a posting outside the neighbourhood, or at
// one the source got wrong. Either way there is nothing to attach to, and inventing
// a group for it would assert something the evidence does not.
func TestAttaches_LeavesAPointerAtNothingAlone(t *testing.T) {
	asset := security("a", "28", "263.58", "7390.19", 10, 563466632)
	cash := money("b", "-7380.19", 10, 563466631, typev1.TxType_TRADE_CASH)
	fee := pointsAt(charge("c", "-10", 8, 167614596), "", "999999999", db.ScopeFile)

	got := members(Partition([]db.GroupingPosting{asset, cash, fee}, DefaultRules(), DefaultOpts()))
	if want := [][]string{{"a", "b"}, {"c"}}; !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// Several postings carry one token whenever a source stamps a record identifier on
// every leg it produced. That is not an ambiguity while they are together: the
// pointer names the event, and any member of it names the same group.
func TestAttaches_JoinsAGroupWhoseMembersAllCarryTheToken(t *testing.T) {
	a := correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "fit1", db.ScopeAccount, db.MatchExact)
	b := correlated(posting("b", typev1.TxType_TRADE_CASH), "", "fit1", db.ScopeAccount, db.MatchExact)
	c := pointsAt(posting("c", typev1.TxType_TRANSACTION_COST), "", "fit1", db.ScopeAccount)

	got := members(Partition([]db.GroupingPosting{a, b, c}, DefaultRules(), DefaultOpts()))
	if want := [][]string{{"a", "b", "c"}}; !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// A pointer only ever finds a carrier that declared its token under the same scope,
// because the comparability key holds the scope and Exact keys it the same way. Two
// sources can issue one string with different reach, and collapsing those would let a
// file-scoped reference match an account-scoped one silently.
func TestAttaches_DoesNotMatchACarrierOfAnotherScope(t *testing.T) {
	a := correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "shared", db.ScopeAccount, db.MatchExact)
	c := pointsAt(posting("c", typev1.TxType_TRANSACTION_COST), "", "shared", db.ScopeBroker)

	got := members(Partition([]db.GroupingPosting{a, c}, DefaultRules(), DefaultOpts()))
	if want := [][]string{{"a"}, {"c"}}; !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// It is an ambiguity when the carriers of one token are not together: the pointer
// would be choosing between two events, and a wrong attachment is worse than none.
//
// Reaching that state takes a rule list without Exact, which is the point rather than
// a contrivance. Exact runs at the highest precedence and claims every posting
// sharing a token into one group, so with the default list a token's carriers are
// always together and this branch never fires. Precedence is expected to become
// per-broker (adr/0047), and a list that puts another rule above Exact can leave them
// apart -- so the guard is what stops a reordering turning into a wrong grouping.
func TestAttaches_DeclinesWhenTheTokenNamesTwoGroups(t *testing.T) {
	a := correlated(security("a", "28", "263.58", "7380.24", 10, 0), "", "shared", db.ScopeFile, db.MatchExact)
	b := correlated(money("b", "-7380.19", 10, 0, typev1.TxType_TRADE_CASH), "", "shared", db.ScopeFile, db.MatchExact)
	d := correlated(security("d", "70", "454.66", "31830.00", 10, 0), "", "shared", db.ScopeFile, db.MatchExact)
	e := correlated(money("e", "-31826.24", 10, 0, typev1.TxType_TRADE_CASH), "", "shared", db.ScopeFile, db.MatchExact)
	c := pointsAt(charge("c", "-10", 8, 167614596), "", "shared", db.ScopeFile)

	// Without Exact, the trade rules pair a-b and d-e into two groups, and all
	// four carry the token the charge points at.
	rules := []Rule{Disposal(), Acquisition(), CashTrade(), Deposit(), Attaches{}}
	gs := Partition([]db.GroupingPosting{a, b, c, d, e}, rules, DefaultOpts())
	if got, want := members(gs), [][]string{{"a", "b"}, {"c"}, {"d", "e"}}; !equal(got, want) {
		t.Fatalf("partition = %v, want the charge left alone", got)
	}
}

// A pointer at a token the bearer itself carries names nothing: a posting saying it
// belongs with itself is not a statement about where it belongs.
func TestAttaches_IgnoresAPointerAtItself(t *testing.T) {
	a := posting("a", typev1.TxType_TRANSACTION_COST)
	a = correlated(a, "", "self", db.ScopeFile, db.MatchExact)
	a = pointsAt(a, "", "self", db.ScopeFile)

	got := members(Partition([]db.GroupingPosting{a}, DefaultRules(), DefaultOpts()))
	if want := [][]string{{"a"}}; !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// Two charges on one trade, which is the ordinary case for a UK purchase: a dealing
// fee and a stamp duty both name the same trade and both join it.
func TestAttaches_TakesEveryPointerAtOneTrade(t *testing.T) {
	asset := security("a", "180", "360.72", "64945.44", 10, 563466632)
	cash := money("b", "-64930.44", 10, 563466631, typev1.TxType_TRADE_CASH)
	fee := pointsAt(charge("c", "-7.50", 8, 167614596), "", "563466632", db.ScopeFile)
	duty := pointsAt(charge("d", "-7.50", 8, 167614597), "", "563466632", db.ScopeFile)

	got := members(Partition([]db.GroupingPosting{asset, cash, fee, duty}, DefaultRules(), DefaultOpts()))
	if want := [][]string{{"a", "b", "c", "d"}}; !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// A file-scoped token means nothing across files, so a pointer must not reach a
// carrier from another upload that happens to have numbered a row the same.
func TestAttaches_DoesNotReachAcrossJobs(t *testing.T) {
	asset := security("a", "28", "263.58", "7390.19", 10, 563466632)
	cash := money("b", "-7380.19", 10, 563466631, typev1.TxType_TRADE_CASH)
	fee := pointsAt(charge("c", "-10", 8, 167614596), "", "563466632", db.ScopeFile)
	fee.JobID = "job2"

	got := members(Partition([]db.GroupingPosting{asset, cash, fee}, DefaultRules(), DefaultOpts()))
	if want := [][]string{{"a", "b"}, {"c"}}; !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// Attaching never moves a posting a rule already placed. A charge pointing at a
// trade that is already grouped joins it; one pointing at a posting some other rule
// claimed leaves that claim exactly as it was.
func TestAttach_DoesNotDisturbTheAnchor(t *testing.T) {
	ps := []db.GroupingPosting{posting("a"), posting("b"), posting("c")}
	st := newState(ps)
	if !st.Claim(Member{ID: "a", Resolved: typev1.TxType_TRADE_ASSET}, Member{ID: "b", Resolved: typev1.TxType_TRADE_CASH}) {
		t.Fatal("claim refused")
	}
	if !st.Attach("a", Member{ID: "c", Resolved: typev1.TxType_TRANSACTION_COST}) {
		t.Fatal("attach refused")
	}
	gs := st.groups(ps)
	if got, want := members(gs), [][]string{{"a", "b", "c"}}; !equal(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	if got := resolvedOf(gs, "a"); got != typev1.TxType_TRADE_ASSET {
		t.Fatalf("anchor resolved to %v, want the value its own rule set", got)
	}
}

// A contributor another rule already placed is refused rather than moved, which is
// what keeps a claim irrevocable.
func TestAttach_RefusesAClaimedContributor(t *testing.T) {
	ps := []db.GroupingPosting{posting("a"), posting("b"), posting("c")}
	st := newState(ps)
	st.Claim(Member{ID: "b", Resolved: typev1.TxType_TRADE_CASH}, Member{ID: "c", Resolved: typev1.TxType_TRADE_ASSET})
	if st.Attach("a", Member{ID: "c", Resolved: typev1.TxType_TRANSACTION_COST}) {
		t.Fatal("attach moved a posting another rule had claimed")
	}
	if got, want := members(st.groups(ps)), [][]string{{"a"}, {"b", "c"}}; !equal(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
}

// All or nothing, like Claim: an attachment naming one free posting and one taken
// one leaves both where they were.
func TestAttach_RefusesWholeRatherThanPartially(t *testing.T) {
	ps := []db.GroupingPosting{posting("a"), posting("b"), posting("c"), posting("d")}
	st := newState(ps)
	st.Claim(Member{ID: "c", Resolved: typev1.TxType_TRADE_CASH}, Member{ID: "d", Resolved: typev1.TxType_TRADE_ASSET})
	if st.Attach("a", Member{ID: "b", Resolved: typev1.TxType_TRANSACTION_COST}, Member{ID: "c", Resolved: typev1.TxType_TRANSACTION_COST}) {
		t.Fatal("partial attachment taken")
	}
	if got, want := members(st.groups(ps)), [][]string{{"a"}, {"b"}, {"c", "d"}}; !equal(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
}

// The engine is handed a neighbourhood rather than a stream, and attaching must not
// break that: the same postings in any order yield the same groups.
func TestAttaches_IsOrderIndependent(t *testing.T) {
	ps := []db.GroupingPosting{
		security("a", "28", "263.58", "7390.19", 10, 563466632),
		money("b", "-7380.19", 10, 563466631, typev1.TxType_TRADE_CASH),
		pointsAt(charge("c", "-10", 8, 167614596), "", "563466632", db.ScopeFile),
		pointsAt(charge("d", "-1", 8, 167614597), "", "563466632", db.ScopeFile),
		security("e", "70", "454.66", "31826.24", 10, 563466700),
		money("f", "31826.24", 10, 563466701, typev1.TxType_TRADE_CASH),
	}
	want := members(Partition(ps, DefaultRules(), DefaultOpts()))

	r := rand.New(rand.NewSource(7))
	for i := range 20 {
		shuffled := make([]db.GroupingPosting, len(ps))
		copy(shuffled, ps)
		r.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		if got := members(Partition(shuffled, DefaultRules(), DefaultOpts())); !equal(got, want) {
			t.Fatalf("shuffle %d: partition = %v, want %v", i, got, want)
		}
	}
}
