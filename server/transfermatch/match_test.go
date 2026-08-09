package transfermatch

import (
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"
)

var day0 = time.Date(2025, 4, 15, 0, 0, 0, 0, time.UTC)

// side builds one unmatched side. amount is signed: positive left the account,
// negative arrived in it.
func side(group, account, amount string, day int, refs ...string) db.TransferSide {
	return db.TransferSide{
		UserID:       "u1",
		GroupID:      group,
		Broker:       typev1.Broker_FIDELITY,
		Account:      account,
		InstrumentID: "gbp",
		Amount:       decimal.RequireFromString(amount),
		Timestamp:    day0.AddDate(0, 0, day),
		BrokerRefs:   refs,
	}
}

// pointing copies a side with the account it names as its other side.
func pointing(s db.TransferSide, counterparty string) db.TransferSide {
	s.CounterpartyAccounts = []string{counterparty}
	return s
}

type link struct{ from, to, method string }

func links(ms []db.TransferMatch) []link {
	out := make([]link, len(ms))
	for i, m := range ms {
		out[i] = link{m.FromGroupID, m.ToGroupID, m.Method}
	}
	return out
}

func match(sides []db.TransferSide) []link {
	return links(Match(sides, DefaultOpts()))
}

func sorted(ls []link) []link {
	out := append([]link(nil), ls...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].from != out[j].from {
			return out[i].from < out[j].from
		}
		return out[i].to < out[j].to
	})
	return out
}

func TestMatch(t *testing.T) {
	cases := []struct {
		name  string
		sides []db.TransferSide
		want  []link
	}{{
		// The 2025-04-15 lump sum from the Fidelity master. The arrival names its
		// source outright, which is the only evidence that needs no proximity.
		name: "the source names the other account",
		sides: []db.TransferSide{
			side("g-fund", "AG10000001", "20000", 0, "971613411"),
			pointing(side("g-cma", "AW10000001", "-20000", 0, "971613414"), "AG10000001"),
		},
		want: []link{{"g-fund", "g-cma", db.TransferMatchPointer}},
	}, {
		// A pointer names the counterparty account, not the occurrence. Beyond the
		// window it names a different transfer between the same two accounts.
		name: "a pointer outside the window matches nothing",
		sides: []db.TransferSide{
			side("g-fund", "AG10000001", "20000", 0, "971613411"),
			pointing(side("g-cma", "AW10000001", "-20000", 30, "1099999999"), "AG10000001"),
		},
		want: nil,
	}, {
		// Two arrivals the pointer cannot tell apart, and whose references are too
		// far away to separate them either. Surfacing both beats guessing.
		name: "a pointer with two identical candidates matches neither",
		sides: []db.TransferSide{
			side("g-fund", "AG10000001", "20000", 0, "1000000000"),
			pointing(side("g-cma-a", "AW10000001", "-20000", 0, "2000000000"), "AG10000001"),
			pointing(side("g-cma-b", "AW10000001", "-20000", 0, "3000000000"), "AG10000001"),
		},
		want: nil,
	}, {
		// The hop the pointer does not cover: the cash management account's
		// departure against the ISA's arrival. Under 0069 the ISA's three rows are
		// one group, so its references are a run and the nearest is 1.
		name: "adjacent references pair the hop no pointer names",
		sides: []db.TransferSide{
			side("g-cma", "AW10000001", "20000", 0, "971613430"),
			side("g-isa", "AS10000001", "-20000", 0, "971613428", "971613429", "971613431"),
		},
		want: []link{{"g-cma", "g-isa", db.TransferMatchReference}},
	}, {
		// The widest real gap in the masters is 59, on a fee transfer whose two
		// statements were written further apart than most. Pairs like this are why
		// the span is not the eight the deposit-run heuristic uses.
		name: "a wide but real reference gap still pairs",
		sides: []db.TransferSide{
			side("g-cma", "AW10000002", "5.31", 0, "922876724"),
			side("g-sipp", "AP10000002", "-5.31", 0, "922876783"),
		},
		want: []link{{"g-cma", "g-sipp", db.TransferMatchReference}},
	}, {
		name: "references far apart are not proximity",
		sides: []db.TransferSide{
			side("g-a", "AG10000001", "20000", 0, "1000000000"),
			side("g-b", "AW10000001", "-20000", 0, "1000000400"),
		},
		want: nil,
	}, {
		// The adversarial case the issue names: the same two amounts between the
		// same account pair every month. Only the date and the reference separate
		// one month from the next.
		name: "monthly fee transfers pair within their own month",
		sides: []db.TransferSide{
			side("jan-isa", "AS10000001", "2.16", 0, "924609745"),
			side("jan-cma", "AW10000001", "-2.16", 0, "924609747"),
			side("feb-isa", "AS10000001", "2.17", 30, "940109337"),
			side("feb-cma", "AW10000001", "-2.17", 30, "940109340"),
			side("mar-isa", "AS10000001", "2.16", 58, "953162890"),
			side("mar-cma", "AW10000001", "-2.16", 58, "953162892"),
		},
		want: []link{
			{"jan-isa", "jan-cma", db.TransferMatchReference},
			{"feb-isa", "feb-cma", db.TransferMatchReference},
			{"mar-isa", "mar-cma", db.TransferMatchReference},
		},
	}, {
		// Equidistant references say nothing about which arrival belongs to the
		// departure, and the amounts and accounts are identical. Surfacing both as
		// unmatched beats pairing arbitrarily: a wrong pair looks authoritative and
		// stops the report finding the side that really is missing.
		name: "equally near candidates match neither",
		sides: []db.TransferSide{
			side("g-a", "AS10000001", "2.16", 0, "924609747"),
			side("g-b1", "AW10000001", "-2.16", 0, "924609745"),
			side("g-b2", "AW10000001", "-2.16", 0, "924609749"),
		},
		want: nil,
	}, {
		// The same shape one reference apart, which is the whole reason the
		// reference is stored: two transfers of the same amount between the same
		// accounts in one window are distinguishable after all.
		name: "references separate two same-amount transfers in one window",
		sides: []db.TransferSide{
			side("g-a1", "AS10000001", "2.16", 0, "924609745"),
			side("g-a2", "AS10000001", "2.16", 0, "924609755"),
			side("g-b1", "AW10000001", "-2.16", 0, "924609747"),
			side("g-b2", "AW10000001", "-2.16", 0, "924609757"),
		},
		want: []link{
			{"g-a1", "g-b1", db.TransferMatchReference},
			{"g-a2", "g-b2", db.TransferMatchReference},
		},
	}, {
		// Money from outside, straight into a product account. There is nothing to
		// pair it with and it must stay unmatched: it is a real external flow.
		name: "a deposit with no counterpart stays unmatched",
		sides: []db.TransferSide{
			side("g-isa", "AS10000001", "-19599", 0, "553113055"),
		},
		want: nil,
	}, {
		// Every transfer in the sample is intra-broker, so the cross-broker case
		// cannot be calibrated. Two brokers' reference sequences are unrelated, and
		// an amount and a date alone are not evidence.
		name: "a cross-broker pair is left unmatched",
		sides: []db.TransferSide{
			side("g-fid", "AG10000001", "20000", 0, "971613411"),
			func() db.TransferSide {
				s := side("g-ibkr", "U1000001", "-20000", 0, "971613414")
				s.Broker = typev1.Broker_IBKR
				return s
			}(),
		},
		want: nil,
	}, {
		// A pointer across brokers is not one either: an account name means nothing
		// outside the broker that issued it.
		name: "a cross-broker pointer is not a pointer",
		sides: []db.TransferSide{
			side("g-fid", "AG10000001", "20000", 0, "971613411"),
			func() db.TransferSide {
				s := pointing(side("g-ibkr", "U1000001", "-20000", 0, "9999999999"), "AG10000001")
				s.Broker = typev1.Broker_IBKR
				return s
			}(),
		},
		want: nil,
	}, {
		name: "money moving within one account is not a transfer",
		sides: []db.TransferSide{
			side("g-a", "AP10000001", "12772.83", 0, "1166853277"),
			side("g-b", "AP10000001", "-12772.83", 0, "1166853278"),
		},
		want: nil,
	}, {
		// The 0048 failure mode: both legs carried the same sign, so the pair added
		// twice its value instead of netting. There is no equal-and-opposite pair to
		// find, and the matcher must not invent one.
		name: "two sides of the same sign are not a pair",
		sides: []db.TransferSide{
			side("g-a", "AG10000001", "20000", 0, "971613411"),
			side("g-b", "AW10000001", "20000", 0, "971613414"),
		},
		want: nil,
	}, {
		name: "a transfer never crosses a commodity",
		sides: []db.TransferSide{
			side("g-a", "AG10000001", "20000", 0, "971613411"),
			func() db.TransferSide {
				s := side("g-b", "AW10000001", "-20000", 0, "971613414")
				s.InstrumentID = "usd"
				return s
			}(),
		},
		want: nil,
	}, {
		name: "a transfer never crosses a user",
		sides: []db.TransferSide{
			side("g-a", "AG10000001", "20000", 0, "971613411"),
			func() db.TransferSide {
				s := side("g-b", "AW10000001", "-20000", 0, "971613414")
				s.UserID = "u2"
				return s
			}(),
		},
		want: nil,
	}, {
		// The two statements are dated independently, so a hop that settles over a
		// weekend still has to pair.
		name: "the two sides need not share a date",
		sides: []db.TransferSide{
			side("g-a", "AG10000001", "20000", 0, "971613411"),
			side("g-b", "AW10000001", "-20000", 2, "971613414"),
		},
		want: []link{{"g-a", "g-b", db.TransferMatchReference}},
	}, {
		// An OFX FITID is opaque and unique within one statement, so two of them are
		// never comparable across accounts. That leaves the pass inapplicable rather
		// than producing a nonsense distance.
		name: "an opaque reference is no proximity signal",
		sides: []db.TransferSide{
			side("g-a", "U1000001", "20000", 0, "20251015U10000018371888432"),
			side("g-b", "U1000002", "-20000", 0, "20251015U10000018371888433"),
		},
		want: nil,
	}, {
		// Nothing else identifies the occurrence, so a side with no reference at all
		// is not matchable however alone it looks.
		name: "a side with no reference is not matchable",
		sides: []db.TransferSide{
			side("g-a", "AG10000001", "20000", 0),
			side("g-b", "AW10000001", "-20000", 0),
		},
		want: nil,
	}, {
		// A named pair and an adjacent one competing for the same arrival. The
		// pointer runs first so the arrival goes to the account that named it, and
		// the merely-adjacent departure is left over.
		name: "a pointer beats mere proximity for the same side",
		sides: []db.TransferSide{
			side("g-near", "AG10000001", "20000", 0, "971613413"),
			side("g-named", "AS10000001", "20000", 0, "1099999999"),
			pointing(side("g-cma", "AW10000001", "-20000", 0, "971613414"), "AS10000001"),
		},
		want: []link{{"g-named", "g-cma", db.TransferMatchPointer}},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Compared as a set. The slice comes out ranked by how good the
			// evidence was, which is deterministic -- TestMatch_IsOrderIndependent
			// pins that -- but is not something a caller should read anything into.
			got := sorted(match(tc.sides))
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, sorted(tc.want)) {
				t.Errorf("Match() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMatch_DirectionFollowsTheSign pins the convention every consumer reads: the
// side whose residual is positive is where the value left. Inverting it would point
// every link the wrong way while still looking like a match.
func TestMatch_DirectionFollowsTheSign(t *testing.T) {
	// Deliberately given arrival-first, so the order of the input cannot be what
	// decides the direction.
	got := Match([]db.TransferSide{
		side("g-arrival", "AW10000001", "-20000", 0, "971613414"),
		side("g-departure", "AG10000001", "20000", 0, "971613411"),
	}, DefaultOpts())
	if len(got) != 1 {
		t.Fatalf("got %d matches, want 1", len(got))
	}
	if got[0].FromGroupID != "g-departure" || got[0].ToGroupID != "g-arrival" {
		t.Errorf("link is %s -> %s, want g-departure -> g-arrival",
			got[0].FromGroupID, got[0].ToGroupID)
	}
}

// TestMatch_IsOrderIndependent verifies the pass is deterministic. The sides arrive
// in whatever order the query returns them, and a matcher whose answer depended on
// that would pair differently between two runs over the same data.
func TestMatch_IsOrderIndependent(t *testing.T) {
	sides := []db.TransferSide{
		side("jan-isa", "AS10000001", "2.16", 0, "924609745"),
		side("jan-cma", "AW10000001", "-2.16", 0, "924609747"),
		side("feb-isa", "AS10000001", "2.17", 30, "940109337"),
		side("feb-cma", "AW10000001", "-2.17", 30, "940109340"),
		side("lone", "AS10000001", "-19599", 5, "553113055"),
		side("g-fund", "AG10000001", "20000", 1, "971613411"),
		pointing(side("g-cma", "AW10000001", "-20000", 1, "971613414"), "AG10000001"),
	}
	want := match(sides)
	if len(want) != 3 {
		t.Fatalf("fixture produced %d matches, want 3", len(want))
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20; i++ {
		shuffled := append([]db.TransferSide(nil), sides...)
		rng.Shuffle(len(shuffled), func(a, b int) {
			shuffled[a], shuffled[b] = shuffled[b], shuffled[a]
		})
		if got := match(shuffled); !reflect.DeepEqual(got, want) {
			t.Fatalf("shuffle %d gave %v, want %v", i, got, want)
		}
	}
}

// TestMatchWindow_FitsInsideTheStaleThreshold guards the coupling the issue does not
// state. defaultStaleTransferDays in server/service/api is 7: if the match window
// were wider, the report would flag transfers the matcher would still go on to pair,
// which is the same "the signal carries no information" failure 0068 exists to fix,
// arriving from the other direction.
func TestMatchWindow_FitsInsideTheStaleThreshold(t *testing.T) {
	const staleTransferDays = 7
	if matchWindow > staleTransferDays*24*time.Hour {
		t.Errorf("matchWindow is %v, wider than the %d-day staleness threshold it must fit inside",
			matchWindow, staleTransferDays)
	}
}
