// Acceptance run over whole broker exports.
//
// The fixtures in testdata are the postings the shipped converters produce for
// three sample exports. They are modelled on real broker files with every
// account number and every reference replaced by an invented one. Distinctness,
// ordering and the numeric gaps between references survive the substitution --
// the Deposit rule reads all three -- but nothing left in them names anyone.
// Do not restore any of it from an original.
//
// They are generated rather than written by hand: an extraction harness in
// local/ converts each export in local/masters, applies those substitutions and
// writes one user archive document per export. Nothing in the repository can
// regenerate them, because the exports it reads may not be committed, so a
// converter change does not update them on its own.
//
// What this pins differs from what the deleted shadow run pinned. That compared
// the engine's partition against the one the converter stated in group_ref;
// group_ref is retired and there is no stated partition left to compare with.
// So this asserts properties of the partition instead: how many groups it drew,
// what kind of event it decided each is, and how many of them do not account for
// themselves.
package grouping

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/txtype"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/encoding/protojson"
)

// tolerance is moneyTolerance in server/service/ingestion/balance.go. The weights
// are exact, so a group written to full precision balances to exactly zero; this
// is here because a source written to 2dp that balances to within half a cent is
// balanced, which the server records as SOURCE_ROUNDING rather than as an
// imbalance.
var tolerance = decimal.New(5, -3)

// optionContractSize is the OCC standard deliverable, as in balance.go. The
// resolved instrument is where that reads it from; a converter has resolved
// nothing, so the OCC hint stands in -- the symbology exists only for a
// standardised contract, so the two agree.
var optionContractSize = decimal.NewFromInt(100)

func TestPartition_Exports(t *testing.T) {
	tests := []struct {
		file   string
		broker typev1.Broker
		want   exportCounts
	}{
		{
			file:   "alice-fidelity.json",
			broker: typev1.Broker_FIDELITY,
			want: exportCounts{
				Postings: 739, Groups: 613, Unbalanced: 88,
				Sells: 27, Buys: 59, CashForCash: 10, DepositRuns: 13,
				Charged: 4,
			},
		},
		{
			file:   "bob-fidelity.json",
			broker: typev1.Broker_FIDELITY,
			want: exportCounts{
				Postings: 674, Groups: 574, Unbalanced: 78,
				Sells: 39, Buys: 32, CashForCash: 13, DepositRuns: 8,
			},
		},
		{
			file:   "bob-ibkr.json",
			broker: typev1.Broker_IBKR,
			want: exportCounts{
				Postings: 72, Groups: 28, Unbalanced: 0,
				Sells: 9, Buys: 13,
				// Every one of these was already grouped, by the FITID the OFX
				// parser stamps on each leg it reads out of a record. Groups is
				// unchanged, which is the assertion that matters: a source that
				// identifies its own records needs none of the rules above.
				Charged: 22,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			ps := loadExport(t, tt.file, tt.broker)
			gs := Partition(postings(ps), DefaultRules(), DefaultOpts())
			got := count(ps, gs)
			if got != tt.want {
				t.Errorf("counts:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// exportPosting keeps the archive posting beside the one the engine reads, so a
// group can be weighed after it has been derived. Weighing needs the currencies,
// the price and the identifier hints, none of which grouping itself looks at.
type exportPosting struct {
	src *archivev1.Posting
	gp  db.GroupingPosting
}

func postings(ps []exportPosting) []db.GroupingPosting {
	out := make([]db.GroupingPosting, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.gp)
	}
	return out
}

// loadExport reads one fixture and keeps the postings the source transcribed.
//
// A leg the server derives -- a routed residual, or the other side of an income
// or expense posting -- carries no evidence and is not partitioned; it is
// re-derived from whatever the partition turns out to be. The account type is
// what tells them apart, and an unset one reads as USER.
func loadExport(t *testing.T, file string, broker typev1.Broker) []exportPosting {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var archive archivev1.UserArchive
	if err := protojson.Unmarshal(raw, &archive); err != nil {
		t.Fatalf("decode: %v", err)
	}
	windows := archive.GetTxs().GetWindows()
	if len(windows) != 1 {
		t.Fatalf("windows: got %d want 1", len(windows))
	}

	var out []exportPosting
	for i, p := range windows[0].GetPostings() {
		switch p.GetAccountType() {
		case typev1.AccountType_ACCOUNT_TYPE_UNSPECIFIED, typev1.AccountType_ACCOUNT_TYPE_USER:
		default:
			continue
		}
		gp := db.GroupingPosting{
			// The job is what a FILE-scoped correlation is comparable within, so
			// one export is one job.
			ID:        fmt.Sprintf("p%d", i),
			UserID:    "u",
			JobID:     "job",
			Broker:    broker,
			Account:   p.GetAccount(),
			OrderDate: p.GetOrderDate().AsTime(),
			Quantity:  decimal.RequireFromString(p.GetQuantity()),
			Declared:  p.GetBrokerTxType(),
		}
		if p.UnitPrice != nil {
			d := decimal.RequireFromString(*p.UnitPrice)
			gp.UnitPrice = &d
		}
		if p.SettlementAmount != nil {
			d := decimal.RequireFromString(*p.SettlementAmount)
			gp.SettlementAmount = &d
		}
		for _, c := range p.GetCorrelations() {
			scope, err := db.ScopeToStr(c.GetScope())
			if err != nil {
				t.Fatalf("correlation %q: %v", c.GetToken(), err)
			}
			matches := make([]string, 0, len(c.GetMatch()))
			for _, m := range c.GetMatch() {
				s, err := db.MatchToStr(m)
				if err != nil {
					t.Fatalf("correlation %q: %v", c.GetToken(), err)
				}
				matches = append(matches, s)
			}
			gp.Correlations = append(gp.Correlations, db.Correlation{
				Label:       c.GetLabel(),
				Token:       c.GetToken(),
				Ordinal:     c.Ordinal,
				OrdinalSpan: c.OrdinalSpan,
				Scope:       scope,
				Match:       matches,
			})
		}
		out = append(out, exportPosting{src: p, gp: gp})
	}
	return out
}

// exportCounts is what a run over one export found. It is the regression
// surface: a rule that stops firing merges or splits events, and every such
// change moves one of these.
type exportCounts struct {
	Postings int
	Groups   int
	// Groups of more than one posting that do not account for themselves.
	//
	// Groups of one are left out. Most of them legitimately do not balance: a
	// broker states a fee or a dividend as a single row whose other side is not
	// in the ledger at all, and the server posts that side rather than routing an
	// imbalance. What is left is a question about the partition -- an event the
	// engine assembled out of several postings either has all of them or does
	// not close.
	//
	// It is not pinned at zero, because two things in these exports legitimately
	// do not close. Fidelity states a unit price to 2dp, which does not reproduce
	// the consideration its own cash row states -- 41 units at 242.82 against
	// 9955.57 leaves 5p -- and the server routes that difference. And a deposit
	// run chains from money arriving out of the user's bank, which the export
	// says nothing about, so the run is short by exactly what came in. Neither is
	// a fact about grouping; both are stable for a given export, which is what
	// makes the count worth pinning.
	Unbalanced int
	// Groups of more than one posting, by the kind of event the engine decided
	// each is. A reinvestment counts as a buy: the income leg that would tell it
	// apart is one the server derives, so it is not in what gets partitioned.
	Sells       int
	Buys        int
	CashForCash int
	DepositRuns int
	// Groups holding a charge the engine attached to the event it was levied on.
	// Counted separately from the kinds above, which it does not change: a buy
	// that gained its dealing fee is still a buy.
	//
	// It is low against the number of charges these exports carry, and the reason
	// is the fixtures rather than the rules. They were extracted before a posting
	// carried two dates, so every one of them states its order date and its trade
	// date as the same instant -- which is what a charge and its trade disagree
	// about in a real export, and what the day bucket exists to reconcile. What
	// is pinned here is therefore the cases that survive that flattening, not the
	// yield on a real file.
	Charged int
	// A group none of the above names is the shape of a rules bug, so this is
	// pinned at zero rather than recorded.
	Other int
}

// count classifies each derived group of more than one posting by what its
// members resolved to.
func count(ps []exportPosting, gs []Group) exportCounts {
	byID := make(map[string]exportPosting, len(ps))
	for _, p := range ps {
		byID[p.gp.ID] = p
	}
	out := exportCounts{Postings: len(ps), Groups: len(gs)}
	for _, g := range gs {
		if len(g.Members) < 2 {
			continue
		}
		if !balances(g, byID) {
			out.Unbalanced++
		}
		var transfer, asset, sold, charged bool
		cashLegs := 0
		for _, m := range g.Members {
			switch m.Resolved {
			case typev1.TxType_TRANSFER:
				transfer = true
			case typev1.TxType_TRADE_ASSET:
				asset = true
				sold = byID[m.ID].gp.Quantity.IsNegative()
			case typev1.TxType_TRADE_CASH:
				cashLegs++
			case typev1.TxType_TRANSACTION_COST:
				charged = true
			}
		}
		if charged {
			out.Charged++
		}
		switch {
		case transfer:
			out.DepositRuns++
		case asset && sold:
			out.Sells++
		case asset:
			out.Buys++
		case cashLegs == 2:
			out.CashForCash++
		default:
			out.Other++
		}
	}
	return out
}

// balances reports whether a group accounts for itself in every commodity.
//
// This is the oracle. A group's postings are in different commodities, so a
// plain sum says nothing -- a buy is +10 AAPL and -1855 USD -- and this applies
// the same weight rule the server stores weights under. Asking it per group
// rather than over the whole export is what makes it a question about the
// partition: an engine that split one trade in two would leave both halves
// short by equal and opposite amounts, which a total over the export would hide.
func balances(g Group, byID map[string]exportPosting) bool {
	sums := map[string]decimal.Decimal{}
	for _, m := range g.Members {
		p := byID[m.ID].src
		// A posting whose other side the server posts contributes nothing left
		// over: its weight and that leg's cancel.
		if serverPosts(p) {
			continue
		}
		amount, commodity := weigh(p)
		sums[commodity] = sums[commodity].Add(amount)
	}
	for _, v := range sums {
		if v.Abs().GreaterThanOrEqual(tolerance) {
			return false
		}
	}
	return true
}

// weigh is weightOf in server/service/ingestion/balance.go, reduced to the cases
// a converter can produce: a security leg converts to money at its price times
// its contract size, and everything else weighs its own quantity in the
// settlement currency. An unresolved posting falls back to its description,
// which still balances it against itself.
func weigh(p *archivev1.Posting) (decimal.Decimal, string) {
	settle := strings.ToUpper(strings.TrimSpace(p.GetSettlementCurrency()))
	if settle == "" {
		settle = strings.ToUpper(strings.TrimSpace(p.GetTradingCurrency()))
	}
	qty := decimal.RequireFromString(p.GetQuantity())
	// The asset leg of a trade is the one type whose counter-leg is money rather
	// than the commodity the posting is in, so it converts at its price. Under
	// the every-candidate rule an ambiguous set does not convert.
	if txtype.MustBe(p.GetBrokerTxType(), typev1.TxType_TRADE_ASSET) {
		// With no price there is nothing to convert at, so the residual stays in
		// the security -- which is the signal that the source omitted one.
		if p.UnitPrice == nil || settle == "" {
			return qty, p.GetInstrumentDescription()
		}
		return qty.Mul(decimal.RequireFromString(*p.UnitPrice)).Mul(contractSize(p)), settle
	}
	if settle == "" {
		return qty, p.GetInstrumentDescription()
	}
	return qty, settle
}

// contractSize is what one unit of quantity delivers.
func contractSize(p *archivev1.Posting) decimal.Decimal {
	for _, h := range p.GetIdentifierHints() {
		if h.GetType() == typev1.IdentifierType_OCC {
			return optionContractSize
		}
	}
	return decimal.NewFromInt(1)
}

// serverPosts reports whether the server posts the other side of this posting
// itself, mirroring residual.Boundary and the account-type guard beside it in
// balance.go.
func serverPosts(p *archivev1.Posting) bool {
	switch p.GetAccountType() {
	case typev1.AccountType_ACCOUNT_TYPE_UNSPECIFIED, typev1.AccountType_ACCOUNT_TYPE_USER:
	default:
		return false
	}
	return txtype.MustBe(p.GetBrokerTxType(), typev1.TxType_INCOME) ||
		txtype.MustBe(p.GetBrokerTxType(), typev1.TxType_EXPENSE)
}
