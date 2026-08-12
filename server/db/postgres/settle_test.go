package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// What settle writes for a group, and why.
//
// These were the ingest balancer's tests, and they moved with the routing. What
// changed with them is the seam: the store is handed weights and decides what a
// group owes from those alone, so the cases state a weight rather than leaving one
// to be derived from a quantity and a price. The weight rule itself is still tested
// where it lives, in server/service/ingestion.

// settleLeg is one posting the store wrote for a group, reduced to what these
// tests assert on.
type settleLeg struct {
	accountType string
	quantity    string
	commodity   string
	purpose     string
}

// settleCase is a group handed to the store and what it should owe for it.
type settleCase struct {
	name string
	// legs are the postings a source stated, in the order they arrive. Each is a
	// quantity and the weight it contributes, which is what the store reads.
	legs []statedLeg
	want []settleLeg
}

// statedLeg is one posting of the upload, with the weight ingestion computed for it.
type statedLeg struct {
	desc      string
	types     []typev1.TxType
	qty       string
	weight    string
	commodity string // "" reads as cur:USD
	// account distinguishes the legs of one group where a test needs it; empty is
	// the group's own account.
	account string
}

// settled runs one case through the store and returns the legs it wrote.
//
// The whole batch is one group, so the residual rules are about what the group as a
// whole leaves over rather than about how the upload was partitioned.
func settled(t *testing.T, p *Postgres, sub string, legs []statedLeg, instByCommodity map[string]string) []settleLeg {
	t.Helper()
	at := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	txs := make([]*apiv1.Tx, len(legs))
	ids := make([]string, len(legs))
	weights := make([]db.Weight, len(legs))
	for i, l := range legs {
		commodity := l.commodity
		if commodity == "" {
			commodity = "cur:USD"
		}
		account := l.account
		if account == "" {
			account = "ACC-1"
		}
		txs[i] = &apiv1.Tx{
			Timestamp:             timestamppb.New(at),
			InstrumentDescription: l.desc,
			BrokerTxType:          l.types,
			ResolvedTxType:        resolveSet(l.types),
			Quantity:              l.qty,
			Account:               account,
			SettlementCurrency:    "USD",
			TradingCurrency:       "USD",
			GroupRef:              "g1",
		}
		ids[i] = instByCommodity[commodity]
		if ids[i] == "" {
			t.Fatalf("case names commodity %q with no instrument", commodity)
		}
		weights[i] = db.Weight{Amount: decimal.RequireFromString(l.weight), Commodity: commodity}
	}
	userID := instByCommodity["user"]
	if err := p.ReplaceTxsInPeriod(context.Background(), userID, "FIDELITY", "",
		timestamppb.New(at.Add(-time.Hour)), timestamppb.New(at.Add(time.Hour)),
		txs, ids, weights, nil); err != nil {
		t.Fatalf("%s: store: %v", sub, err)
	}
	return derivedLegs(t, p, userID)
}

// derivedLegs reads back everything the store wrote for itself, in a fixed order.
func derivedLegs(t *testing.T, p *Postgres, userID string) []settleLeg {
	t.Helper()
	rows, err := p.q.QueryContext(context.Background(), `
		SELECT account_type, quantity::text AS qty, weight_commodity, synthetic_purpose
		FROM txs
		WHERE user_id = $1 AND synthetic_purpose IS NOT NULL
		-- The table's numeric column, not the text the select casts: a bare
		-- "quantity" would bind to the output alias and sort -23.40 after 2.80
		-- under a collation that skips punctuation.
		ORDER BY synthetic_purpose, weight_commodity, txs.quantity
	`, userID)
	if err != nil {
		t.Fatalf("derived legs: %v", err)
	}
	defer rows.Close()
	var out []settleLeg
	for rows.Next() {
		var l settleLeg
		if err := rows.Scan(&l.accountType, &l.quantity, &l.commodity, &l.purpose); err != nil {
			t.Fatalf("derived legs: %v", err)
		}
		out = append(out, l)
	}
	return out
}

// resolveSet is txtype.Resolve's answer for the sets these cases use, spelled out
// so the test states the resolved value it depends on rather than deriving it.
func resolveSet(set []typev1.TxType) typev1.TxType {
	if len(set) == 1 {
		return set[0]
	}
	return typev1.TxType_AMBIGUOUS
}

func TestSettle(t *testing.T) {
	// One group per case, because what a group owes is the subject: the store would
	// otherwise leave each posting alone in a group of its own, which is what it
	// does when nothing tells it two postings are one event.
	p := testDBTx(t).WithSettler(oneGroupSettler{})
	ctx := context.Background()
	userID, usd := balanceSeed(t, p, "sub|settle")
	aapl, err := p.EnsureInstrument(ctx, "STOCK", "", "AAPL", "USD", "", "",
		[]db.IdentifierInput{{Type: "TICKER", Value: "AAPL", Canonical: true}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure AAPL: %v", err)
	}
	instByCommodity := map[string]string{
		"cur:USD":      usd,
		"inst:" + aapl: aapl,
		"user":         userID,
	}

	trade := []typev1.TxType{typev1.TxType_TRADE_ASSET}
	cash := []typev1.TxType{typev1.TxType_TRADE_CASH}
	transfer := []typev1.TxType{typev1.TxType_TRANSFER}
	income := []typev1.TxType{typev1.TxType_INCOME}
	dividend := []typev1.TxType{typev1.TxType_DIVIDEND}
	charge := []typev1.TxType{typev1.TxType_TRANSACTION_COST}

	cases := []settleCase{{
		// The case ADR 0021 was written about: the source reports its own cash row
		// beside the trade. Nothing is left over, so nothing is written.
		name: "a trade and the cash row that settles it owe nothing",
		legs: []statedLeg{
			{desc: "AAPL", types: trade, qty: "10", weight: "-1855"},
			{desc: "USD", types: cash, qty: "1855", weight: "1855"},
		},
	}, {
		// A journal's other side is in an account the upload does not carry, so the
		// value is in transit rather than missing.
		name: "one side of a journal clears in the currency",
		legs: []statedLeg{{desc: "USD", types: transfer, qty: "1000", weight: "1000"}},
		want: []settleLeg{{
			accountType: "TRANSFER_CLEARING", quantity: "-1000",
			commodity: "cur:USD", purpose: db.RoutedPurpose,
		}},
	}, {
		// Two figures the source rounded differently, not a leg it left out, so this
		// is the source disagreeing with itself. The whole-source rule is what says
		// so, and it applies because the group holds every leg the source stated.
		name: "a sub-cent difference reads as the source's own rounding",
		legs: []statedLeg{
			{desc: "USD", types: transfer, qty: "1000.001", weight: "1000.001"},
			{desc: "USD", types: transfer, qty: "-1000", weight: "-1000"},
		},
		want: []settleLeg{{
			accountType: "SOURCE_ROUNDING", quantity: "-0.001",
			commodity: "cur:USD", purpose: db.RoutedPurpose,
		}},
	}, {
		// Income under every reading, so the account its money came from is named by
		// the posting and the group owes a boundary leg rather than a residual.
		name: "a dividend gets the income it came from",
		legs: []statedLeg{{desc: "USD", types: income, qty: "23.40", weight: "23.40"}},
		want: []settleLeg{{
			accountType: "INCOME", quantity: "-23.40",
			commodity: "cur:USD", purpose: db.BoundaryPurpose,
		}},
	}, {
		// A leg each rather than one for the difference: netting them would post
		// 20.60 to whichever account won, and which account is the point.
		name: "a dividend and a charge in one group get a leg each",
		legs: []statedLeg{
			{desc: "USD", types: dividend, qty: "23.40", weight: "23.40"},
			{desc: "USD", types: charge, qty: "-2.80", weight: "-2.80"},
		},
		want: []settleLeg{{
			accountType: "INCOME", quantity: "-23.40",
			commodity: "cur:USD", purpose: db.BoundaryPurpose,
		}, {
			accountType: "EXPENSE", quantity: "2.80",
			commodity: "cur:USD", purpose: db.BoundaryPurpose,
		}},
	}, {
		// A set spanning branches resolves to AMBIGUOUS, and both rules are must-be
		// over the resolved value: a posting that only may be a transfer is not one,
		// and it names no boundary either. Grouping is what settles it.
		name: "an ambiguous set names no boundary and clears to imbalance",
		legs: []statedLeg{{
			desc: "USD", types: []typev1.TxType{typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER},
			qty: "1000", weight: "1000",
		}},
		want: []settleLeg{{
			accountType: "IMBALANCE", quantity: "-1000",
			commodity: "cur:USD", purpose: db.RoutedPurpose,
		}},
	}, {
		// One transfer leg is enough to classify the group, because the residual is
		// the other side of the movement and that is in another account. Read leg by
		// leg, a Fidelity product-account deposit would route to imbalance.
		name: "one transfer leg sends a mixed group's residual to clearing",
		legs: []statedLeg{
			{desc: "USD", types: transfer, qty: "20000", weight: "20000"},
			{desc: "USD", types: cash, qty: "-20000", weight: "-20000"},
			{desc: "USD", types: transfer, qty: "20000", weight: "20000"},
		},
		want: []settleLeg{{
			accountType: "TRANSFER_CLEARING", quantity: "-20000",
			commodity: "cur:USD", purpose: db.RoutedPurpose,
		}},
	}, {
		// A residual can span commodities, as beancount's residual inventory and
		// ledger's Imbalance:<CUR> both can.
		name: "a residual spanning two commodities gets a leg each",
		legs: []statedLeg{
			{desc: "AAPL", types: trade, qty: "10", weight: "10", commodity: "inst:"},
			{desc: "USD", types: cash, qty: "25", weight: "25"},
		},
		want: []settleLeg{{
			accountType: "IMBALANCE", quantity: "-25",
			commodity: "cur:USD", purpose: db.RoutedPurpose,
		}, {
			accountType: "IMBALANCE", quantity: "-10",
			commodity: "inst:", purpose: db.RoutedPurpose,
		}},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i := range tc.legs {
				if tc.legs[i].commodity == "inst:" {
					tc.legs[i].commodity = "inst:" + aapl
				}
			}
			for i := range tc.want {
				if tc.want[i].commodity == "inst:" {
					tc.want[i].commodity = "inst:" + aapl
				}
			}
			got := settled(t, p, tc.name, tc.legs, instByCommodity)
			if len(got) != len(tc.want) {
				t.Fatalf("wrote %d legs, want %d: %v", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				if got[i].accountType != w.accountType {
					t.Errorf("leg %d account type = %q, want %q", i, got[i].accountType, w.accountType)
				}
				if !decimal.RequireFromString(got[i].quantity).Equal(decimal.RequireFromString(w.quantity)) {
					t.Errorf("leg %d quantity = %q, want %q", i, got[i].quantity, w.quantity)
				}
				if got[i].commodity != w.commodity {
					t.Errorf("leg %d commodity = %q, want %q", i, got[i].commodity, w.commodity)
				}
				if got[i].purpose != w.purpose {
					t.Errorf("leg %d purpose = %q, want %q", i, got[i].purpose, w.purpose)
				}
			}
			assertBalanced(t, p, userID)
		})
	}
}

// A routed counterparty keeps the broker account, date and declared type of the
// group it balances, which is what the per-broker total in the imbalance report
// reads and what makes a residual attributable to the account that produced it.
func TestSettle_KeepsAttribution(t *testing.T) {
	p := testDBTx(t)
	userID, usd := balanceSeed(t, p, "sub|settle-attribution")
	at := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	tx := &apiv1.Tx{
		Timestamp:             timestamppb.New(at),
		InstrumentDescription: "AAPL",
		BrokerTxType:          []typev1.TxType{typev1.TxType_TRADE_ASSET},
		ResolvedTxType:        typev1.TxType_TRADE_ASSET,
		Quantity:              "10",
		Account:               "ACC-9",
		SettlementCurrency:    "USD",
		GroupRef:              "t1",
	}
	weights := []db.Weight{{Amount: decimal.RequireFromString("1855"), Commodity: "cur:USD"}}
	if err := p.ReplaceTxsInPeriod(context.Background(), userID, "FIDELITY", "",
		timestamppb.New(at.Add(-time.Hour)), timestamppb.New(at.Add(time.Hour)),
		[]*apiv1.Tx{tx}, []string{usd}, weights, nil); err != nil {
		t.Fatalf("store: %v", err)
	}

	var account, brokerTypes, resolved, desc, purpose string
	var ts time.Time
	var group, legGroup string
	err := p.q.QueryRowContext(context.Background(), `
		SELECT account, array_to_string(broker_tx_type, ','), resolved_tx_type,
		       instrument_description, synthetic_purpose, timestamp, group_id::text,
		       (SELECT group_id::text FROM txs WHERE user_id = $1 AND synthetic_purpose IS NULL)
		FROM txs WHERE user_id = $1 AND synthetic_purpose IS NOT NULL
	`, userID).Scan(&account, &brokerTypes, &resolved, &desc, &purpose, &ts, &group, &legGroup)
	if err != nil {
		t.Fatalf("read routed posting: %v", err)
	}
	if account != "ACC-9" {
		t.Errorf("account = %q, want ACC-9", account)
	}
	if brokerTypes != "TRADE_ASSET" || resolved != "TRADE_ASSET" {
		t.Errorf("types = %q/%q, want the declared set and resolved value of the event", brokerTypes, resolved)
	}
	if !ts.Equal(at) {
		t.Errorf("timestamp = %v, want %v", ts, at)
	}
	if group != legGroup {
		t.Error("the residual must land in the group it balances")
	}
	// Money, so it is described by its currency and resolves to that instrument
	// rather than to the security it balances.
	if desc != "USD" {
		t.Errorf("description = %q, want the currency code", desc)
	}
	if purpose != db.RoutedPurpose {
		t.Errorf("synthetic_purpose = %q, want %q", purpose, db.RoutedPurpose)
	}
}

// A posting uploaded with no group_ref is its own group, and what it owes is
// written into that group rather than into a group of its own or a neighbour's.
func TestSettle_UngroupedPostingsAreSettledSeparately(t *testing.T) {
	p := testDBTx(t)
	userID, usd := balanceSeed(t, p, "sub|settle-ungrouped")
	at := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	leg := func(qty string) *apiv1.Tx {
		return &apiv1.Tx{
			Timestamp: timestamppb.New(at), InstrumentDescription: "USD",
			BrokerTxType:   []typev1.TxType{typev1.TxType_TRANSFER},
			ResolvedTxType: typev1.TxType_TRANSFER, Quantity: qty, Account: "ACC-1",
			SettlementCurrency: "USD", TradingCurrency: "USD",
		}
	}
	txs := []*apiv1.Tx{leg("1000"), leg("500")}
	weights := []db.Weight{
		{Amount: decimal.RequireFromString("1000"), Commodity: "cur:USD"},
		{Amount: decimal.RequireFromString("500"), Commodity: "cur:USD"},
	}
	if err := p.ReplaceTxsInPeriod(context.Background(), userID, "FIDELITY", "",
		timestamppb.New(at.Add(-time.Hour)), timestamppb.New(at.Add(time.Hour)),
		txs, []string{usd, usd}, weights, nil); err != nil {
		t.Fatalf("store: %v", err)
	}

	// Two groups, each with its own clearing leg. One group with a single leg for
	// 1500 would mean the two events had been merged.
	if got := countGroups(t, p, userID); got != 2 {
		t.Fatalf("tx groups = %d, want one per ungrouped posting", got)
	}
	got := derivedLegs(t, p, userID)
	if len(got) != 2 {
		t.Fatalf("derived legs = %v, want one per group", got)
	}
	for _, l := range got {
		if l.accountType != "TRANSFER_CLEARING" {
			t.Errorf("leg %v: want TRANSFER_CLEARING", l)
		}
	}
	assertBalanced(t, p, userID)
}
