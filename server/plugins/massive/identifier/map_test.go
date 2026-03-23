package identifier

import (
	"testing"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/plugins/massive/client"
)

func TestStockFromTicker(t *testing.T) {
	r := &client.TickerOverviewResult{
		Ticker:          "AAPL",
		Name:            "Apple Inc.",
		Market:          "stocks",
		PrimaryExchange: "XNAS",
		CurrencyName:    "usd",
		CompositeFIGI:   "BBG000B9XRY4",
		ShareClassFIGI:  "BBG001S5N8V8",
		CIK:             "0000320193",
		SICCode:         "3571",
	}
	inst, ids := stockFromTicker(r)
	if inst == nil {
		t.Fatal("expected instrument")
	}
	if inst.AssetClass != db.AssetClassStock {
		t.Errorf("AssetClass = %q, want STOCK", inst.AssetClass)
	}
	if inst.Exchange != "XNAS" {
		t.Errorf("Exchange = %q, want XNAS", inst.Exchange)
	}
	if inst.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", inst.Currency)
	}
	if inst.Name != "Apple Inc." {
		t.Errorf("Name = %q, want Apple Inc.", inst.Name)
	}
	if inst.CIK != "0000320193" {
		t.Errorf("CIK = %q, want 0000320193", inst.CIK)
	}
	if inst.SICCode != "3571" {
		t.Errorf("SICCode = %q, want 3571", inst.SICCode)
	}
	if len(ids) != 3 {
		t.Fatalf("len(ids) = %d, want 3", len(ids))
	}
	assertID(t, ids[0], "TICKER", "XNAS", "AAPL")
	assertID(t, ids[1], "OPENFIGI_COMPOSITE", "", "BBG000B9XRY4")
	assertID(t, ids[2], "OPENFIGI_SHARE_CLASS", "", "BBG001S5N8V8")
}

func TestStockFromTicker_Index(t *testing.T) {
	r := &client.TickerOverviewResult{
		Ticker: "SPX",
		Market: "indices",
	}
	inst, _ := stockFromTicker(r)
	if inst != nil {
		t.Fatal("expected nil for index ticker")
	}
}

func TestStockFromTicker_NoFIGI(t *testing.T) {
	r := &client.TickerOverviewResult{
		Ticker:          "TEST",
		Name:            "Test Inc.",
		Market:          "stocks",
		PrimaryExchange: "XNYS",
		CurrencyName:    "usd",
	}
	_, ids := stockFromTicker(r)
	if len(ids) != 1 {
		t.Fatalf("len(ids) = %d, want 1 (TICKER only)", len(ids))
	}
	assertID(t, ids[0], "TICKER", "XNYS", "TEST")
}

func TestOptionFromContract(t *testing.T) {
	contract := &client.OptionsContractResult{
		Ticker:            "O:AAPL251219C00230000",
		UnderlyingTicker:  "AAPL",
		ContractType:      "call",
		ExerciseStyle:     "american",
		ExpirationDate:    "2025-12-19",
		StrikePrice:       230.0,
		SharesPerContract: 100,
		PrimaryExchange:   "BATO",
	}
	inst, ids := optionFromContract(contract)
	if inst == nil {
		t.Fatal("expected instrument")
	}
	if inst.AssetClass != db.AssetClassOption {
		t.Errorf("AssetClass = %q, want OPTION", inst.AssetClass)
	}
	if inst.Name != "AAPL251219C00230000" {
		t.Errorf("Name = %q, want AAPL251219C00230000", inst.Name)
	}
	if inst.Exchange != "BATO" {
		t.Errorf("Exchange = %q, want BATO", inst.Exchange)
	}
	if len(inst.UnderlyingIdentifiers) != 1 {
		t.Fatalf("len(UnderlyingIdentifiers) = %d, want 1", len(inst.UnderlyingIdentifiers))
	}
	assertID(t, inst.UnderlyingIdentifiers[0], "TICKER", "", "AAPL")
	if len(ids) != 2 {
		t.Fatalf("len(ids) = %d, want 2", len(ids))
	}
	assertID(t, ids[0], "OCC", "", "AAPL251219C00230000")
	assertID(t, ids[1], "TICKER", "BATO", "AAPL251219C00230000")
}

func TestOptionFromContract_NoUnderlying(t *testing.T) {
	contract := &client.OptionsContractResult{
		Ticker:          "O:AAPL251219C00230000",
		PrimaryExchange: "BATO",
	}
	inst, ids := optionFromContract(contract)
	if inst == nil {
		t.Fatal("expected instrument")
	}
	if len(inst.UnderlyingIdentifiers) != 0 {
		t.Error("expected empty UnderlyingIdentifiers when no underlying ticker")
	}
	if len(ids) != 2 {
		t.Fatalf("len(ids) = %d, want 2", len(ids))
	}
}

func assertID(t *testing.T, got struct {
	Type   string
	Domain string
	Value  string
}, wantType, wantDomain, wantValue string) {
	t.Helper()
	if got.Type != wantType || got.Domain != wantDomain || got.Value != wantValue {
		t.Errorf("id = {%q, %q, %q}, want {%q, %q, %q}",
			got.Type, got.Domain, got.Value, wantType, wantDomain, wantValue)
	}
}
