package ingestion

import (
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/txtype"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
	"time"
)

const (
	aaplID = "inst-aapl"
	usdID  = "inst-usd"
	eurID  = "inst-eur"
	optID  = "inst-opt"
)

// balanceFixtures is the instrument map the weight tests resolve against: two
// currencies and two securities.
func balanceFixtures() map[string]balanceInstrument {
	return balanceInstruments(map[string]*db.InstrumentRow{
		aaplID: {ID: aaplID, AssetClass: strPtr(db.AssetClassStock)},
		optID:  {ID: optID, AssetClass: strPtr(db.AssetClassOption)},
		usdID:  {ID: usdID, AssetClass: strPtr(db.AssetClassCash), Currency: strPtr("USD")},
		eurID:  {ID: eurID, AssetClass: strPtr(db.AssetClassCash), Currency: strPtr("EUR")},
	})
}

// posting builds one leg. price is nil for a source that supplied none, which is
// not the same as a price of zero.
type posting struct {
	desc string
	// typ is the single declared candidate; types overrides it for a fixture
	// declaring an ambiguous set.
	typ     typev1.TxType
	types   []typev1.TxType
	qty     string
	price   *string
	settle  string
	trading string
	instID  string
}

func (p posting) tx(at time.Time) *apiv1.Tx {
	set := p.types
	if set == nil {
		set = []typev1.TxType{p.typ}
	}
	return &apiv1.Tx{
		OrderDate:             timestamppb.New(at),
		TradeDate:             timestamppb.New(at),
		InstrumentDescription: p.desc,
		BrokerTxType:          set,
		// These tests call weights directly, below the pipeline step that
		// derives the resolved value, so the fixture sets it the way
		// ingestBatch would have.
		ResolvedTxType:     txtype.Resolve(set),
		Quantity:           p.qty,
		UnitPrice:          p.price,
		SettlementCurrency: p.settle,
		TradingCurrency:    p.trading,
		Account:            "ACC-1",
	}
}

// The contract size a price is multiplied by to reach the consideration. The 100
// belongs to the asset class -- an OCC symbol exists only for a standardised
// contract -- while contract_multiplier records the deviation a corporate action
// can leave behind. See the column comment in server/migrations/001_initial.sql.
func TestBalanceInstruments_ContractSize(t *testing.T) {
	cases := []struct {
		name string
		row  *db.InstrumentRow
		want string
	}{{
		name: "standard option contract delivers 100 shares",
		row:  &db.InstrumentRow{AssetClass: strPtr(db.AssetClassOption), ContractMultiplier: decimal.RequireFromString("1")},
		want: "100",
	}, {
		name: "a 3:2 deliverable is recorded as 1.5, meaning 150",
		row:  &db.InstrumentRow{AssetClass: strPtr(db.AssetClassOption), ContractMultiplier: decimal.RequireFromString("1.5")},
		want: "150",
	}, {
		// The column is NOT NULL DEFAULT 1 so the database cannot supply this,
		// but a zero would weigh a whole trade to nothing.
		name: "an absent multiplier falls back to the standard",
		row:  &db.InstrumentRow{AssetClass: strPtr(db.AssetClassOption)},
		want: "100",
	}, {
		name: "a share is quoted in the units it trades in",
		row:  &db.InstrumentRow{AssetClass: strPtr(db.AssetClassStock), ContractMultiplier: decimal.RequireFromString("1")},
		want: "1",
	}, {
		name: "so is a currency",
		row:  &db.InstrumentRow{AssetClass: strPtr(db.AssetClassCash), Currency: strPtr("USD")},
		want: "1",
	}, {
		// A future's size varies per contract and nothing stores it, so one
		// weighs as it always has. See docs/issues/0072.
		name: "a future is left as it was",
		row:  &db.InstrumentRow{AssetClass: strPtr(db.AssetClassFuture), ContractMultiplier: decimal.RequireFromString("1")},
		want: "1",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := balanceInstruments(map[string]*db.InstrumentRow{"i": tc.row})["i"].contractSize
			if got.String() != tc.want {
				t.Errorf("contract size = %v, want %v", got, tc.want)
			}
		})
	}
}

// A non-standard deliverable weighs against the cash it actually settled for,
// which is what makes the multiplier worth reading rather than assuming 100.
func TestWeights_NonStandardDeliverable(t *testing.T) {
	at := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	price := func(v string) *string { return &v }
	instruments := balanceInstruments(map[string]*db.InstrumentRow{
		optID: {ID: optID, AssetClass: strPtr(db.AssetClassOption), ContractMultiplier: decimal.RequireFromString("1.5")},
		usdID: {ID: usdID, AssetClass: strPtr(db.AssetClassCash), Currency: strPtr("USD")},
	})

	// 2 contracts of 150 shares at 3.00 is 900, not the 600 a standard one costs.
	txs := []*apiv1.Tx{
		posting{desc: "OPT", typ: typev1.TxType_TRADE_ASSET, qty: "2", price: price("3"), settle: "USD", instID: optID}.tx(at),
		posting{desc: "USD", typ: typev1.TxType_TRADE_CASH, qty: "-900", price: price("1"), settle: "USD", trading: "USD", instID: usdID}.tx(at),
	}

	// The two weigh to nothing between them, so the store has nothing to route
	// for the group. Assuming 100 would leave 300 of imbalance.
	expectWeightsCancel(t, weights(txs, []string{optID, usdID}, instruments))
}

// expectWeightsCancel fails unless every commodity in the batch sums to exactly
// zero, which is what the deferred balance constraint checks over a stored group.
func expectWeightsCancel(t *testing.T, ws []db.Weight) {
	t.Helper()
	sums := map[string]decimal.Decimal{}
	for _, w := range ws {
		sums[w.Commodity] = sums[w.Commodity].Add(w.Amount)
	}
	for commodity, sum := range sums {
		if !sum.IsZero() {
			t.Errorf("weights sum to %v in %s, want exactly 0", sum, commodity)
		}
	}
}

// TestWeights covers the stored form of each branch of the weight rule. The values
// are the ones the store settles a group from, so what is asserted here is what a
// group's balance is checked against.
func TestWeights(t *testing.T) {
	at := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	price := func(v string) *string { return &v }
	instruments := balanceFixtures()

	cases := []struct {
		name      string
		posting   posting
		instID    string
		amount    string
		commodity string
	}{{
		// The converting branch: a security leg reaches its counter-leg's units at
		// its price, so it weighs money rather than shares.
		name:      "a priced buy weighs its consideration in the settlement currency",
		posting:   posting{desc: "AAPL", typ: typev1.TxType_TRADE_ASSET, qty: "10", price: price("185.50"), settle: "USD"},
		instID:    aaplID,
		amount:    "1855",
		commodity: "cur:USD",
	}, {
		// A price is per underlying unit, so the contract size is part of the
		// consideration rather than a correction applied to it.
		name:      "an option leg weighs by its contract size",
		posting:   posting{desc: "OPT", typ: typev1.TxType_TRADE_ASSET, qty: "8", price: price("20.1105585"), settle: "USD"},
		instID:    optID,
		amount:    "16088.4468",
		commodity: "cur:USD",
	}, {
		// The settlement-currency guard: a cash row is already in the units the
		// group balances in, so its price is not a conversion rate whatever the
		// broker typed in the tx type column.
		name:      "a cash row typed as a sale weighs its own quantity",
		posting:   posting{desc: "USD", typ: typev1.TxType_TRADE_ASSET, qty: "-1855", price: price("185.50"), settle: "USD", trading: "USD"},
		instID:    usdID,
		amount:    "-1855",
		commodity: "cur:USD",
	}, {
		// No price is not a price of zero: there is nothing to convert at, so the
		// weight stays in the security and the missing price shows up as a
		// share-denominated residual.
		name:      "an unpriced buy weighs shares in its instrument",
		posting:   posting{desc: "AAPL", typ: typev1.TxType_TRADE_ASSET, qty: "10", settle: "USD"},
		instID:    aaplID,
		amount:    "10",
		commodity: "inst:" + aaplID,
	}, {
		// A movement across currencies does have its counter-leg in other units,
		// and the price is the FX rate.
		name:      "a cross-currency dividend converts at the FX rate",
		posting:   posting{desc: "EUR", typ: typev1.TxType_INCOME, qty: "100", price: price("1.35"), settle: "USD", trading: "EUR"},
		instID:    eurID,
		amount:    "135",
		commodity: "cur:USD",
	}, {
		// A journal moves a commodity without converting it, so the weight holds
		// the shares rather than a frozen cash value.
		name:      "a securities journal weighs shares even with a price",
		posting:   posting{desc: "AAPL", typ: typev1.TxType_TRANSFER, qty: "-10", price: price("185.50"), settle: "USD"},
		instID:    aaplID,
		amount:    "-10",
		commodity: "inst:" + aaplID,
	}, {
		// The fallback that keeps weight_commodity non-empty: a posting whose
		// instrument never resolved still balances against itself.
		name:      "an unresolved posting falls back to its description",
		posting:   posting{desc: "MYSTERY CORP", typ: typev1.TxType_TRADE_ASSET, qty: "5"},
		instID:    "",
		amount:    "5",
		commodity: "desc:MYSTERY CORP",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := weights([]*apiv1.Tx{tc.posting.tx(at)}, []string{tc.instID}, instruments)
			if len(got) != 1 {
				t.Fatalf("weights returned %d entries, want 1", len(got))
			}
			if got[0].Amount.String() != tc.amount {
				t.Errorf("amount = %v, want %v", got[0].Amount, tc.amount)
			}
			if got[0].Commodity != tc.commodity {
				t.Errorf("commodity = %q, want %q", got[0].Commodity, tc.commodity)
			}
		})
	}
}

// TestValidateWeightNeutrality pins the bound on declared ambiguity: a set is
// admissible only if every candidate weighs the posting the same way. Only a
// priced security row can diverge -- a price is what a trade has and a transfer
// does not -- so that is the rejected case, and removing the price or the
// ambiguity is what makes it pass.
func TestValidateWeightNeutrality(t *testing.T) {
	at := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	price := func(v string) *string { return &v }
	instruments := balanceFixtures()

	cases := []struct {
		name     string
		posting  posting
		instID   string
		wantErrs int
	}{{
		// TRADE_ASSET converts at the price, TRANSFER weighs its own shares:
		// the source has already answered which it is by supplying a price.
		name:     "a priced security row spanning trade and transfer is rejected",
		posting:  posting{desc: "AAPL", types: []typev1.TxType{typev1.TxType_TRADE_ASSET, typev1.TxType_TRANSFER}, qty: "10", price: price("185.50"), settle: "USD"},
		instID:   aaplID,
		wantErrs: 1,
	}, {
		// With no price every candidate weighs the shares in the security.
		name:     "the same set unpriced is neutral",
		posting:  posting{desc: "AAPL", types: []typev1.TxType{typev1.TxType_TRADE_ASSET, typev1.TxType_TRANSFER}, qty: "10", settle: "USD"},
		instID:   aaplID,
		wantErrs: 0,
	}, {
		name:     "a singleton set has nothing to disagree about",
		posting:  posting{desc: "AAPL", typ: typev1.TxType_TRADE_ASSET, qty: "10", price: price("185.50"), settle: "USD"},
		instID:   aaplID,
		wantErrs: 0,
	}, {
		// The settlement-currency guard weighs a cash row the same under every
		// candidate, so the ambiguity is harmless even priced.
		name:     "a priced cash row is neutral whatever it declares",
		posting:  posting{desc: "USD", types: []typev1.TxType{typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER}, qty: "-1855", price: price("1"), settle: "USD", trading: "USD"},
		instID:   usdID,
		wantErrs: 0,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validateWeightNeutrality([]*apiv1.Tx{tc.posting.tx(at)}, []int{7}, []string{tc.instID}, instruments)
			if len(got) != tc.wantErrs {
				t.Fatalf("validateWeightNeutrality returned %d errors, want %d: %v", len(got), tc.wantErrs, got)
			}
			if tc.wantErrs == 0 {
				return
			}
			if got[0].GetField() != "broker_tx_type" {
				t.Errorf("field = %q, want broker_tx_type", got[0].GetField())
			}
			if got[0].GetRowIndex() != 7 {
				t.Errorf("row index = %d, want the caller's 7", got[0].GetRowIndex())
			}
		})
	}
}

// What a group leaves over is exactly what the store has to route, so the weights
// have to say it in the units the store reads. Here they say 11.95: the fee the
// broker netted into the trade's total and reported nowhere.
//
// That the group then sums to zero once the store writes the counterparty is
// TestSettle's, in server/db/postgres. This is the half that has to be right before
// the store can be, because the store never sees the postings these came from.
func TestWeights_NameWhatTheGroupIsShort(t *testing.T) {
	at := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	price := func(v string) *string { return &v }
	instruments := balanceFixtures()

	txs := []*apiv1.Tx{
		posting{desc: "AAPL", typ: typev1.TxType_TRADE_ASSET, qty: "10", price: price("185.50"), settle: "USD", instID: aaplID}.tx(at),
		posting{desc: "USD", typ: typev1.TxType_TRADE_ASSET, qty: "-1866.95", settle: "USD", trading: "USD", instID: usdID}.tx(at),
	}

	sums := map[string]decimal.Decimal{}
	for _, w := range weights(txs, []string{aaplID, usdID}, instruments) {
		sums[w.Commodity] = sums[w.Commodity].Add(w.Amount)
	}
	if got := sums["cur:USD"]; got.String() != "-11.95" {
		t.Errorf("weights leave %v in cur:USD, want -11.95", got)
	}
	if len(sums) != 1 {
		t.Errorf("weights span %d commodities, want only the settlement currency", len(sums))
	}
}
