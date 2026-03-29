package identifier

import (
	"context"
	"testing"

	"github.com/leedenison/portfoliodb/server/plugins/eodhd/client"
)

func TestStockFromSearch(t *testing.T) {
	r := &client.SearchResult{
		Code:     "AAPL",
		Exchange: "US",
		Name:     "Apple Inc",
		Type:     "Common Stock",
		Currency: "USD",
		ISIN:     "US0378331005",
	}

	inst, ids := stockFromSearch(context.Background(), r, nil)

	if inst == nil {
		t.Fatal("expected instrument")
	}
	if inst.AssetClass != "STOCK" {
		t.Errorf("AssetClass = %q, want STOCK", inst.AssetClass)
	}
	if inst.Exchange != "" {
		t.Errorf("Exchange = %q, want empty (EODHD codes are not MICs)", inst.Exchange)
	}
	if inst.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", inst.Currency)
	}
	if inst.Name != "Apple Inc" {
		t.Errorf("Name = %q, want Apple Inc", inst.Name)
	}

	wantIDs := map[string]bool{"MIC_TICKER": false, "ISIN": false}
	for _, id := range ids {
		wantIDs[id.Type] = true
		if id.Type == "MIC_TICKER" {
			if id.Domain != "" {
				t.Errorf("MIC_TICKER Domain = %q, want empty", id.Domain)
			}
			if id.Value != "AAPL" {
				t.Errorf("MIC_TICKER Value = %q, want AAPL", id.Value)
			}
		}
		if id.Type == "ISIN" && id.Value != "US0378331005" {
			t.Errorf("ISIN Value = %q, want US0378331005", id.Value)
		}
	}
	for typ, found := range wantIDs {
		if !found {
			t.Errorf("missing identifier type %q", typ)
		}
	}
}

func TestStockFromSearch_NoISIN(t *testing.T) {
	r := &client.SearchResult{
		Code:     "AAPL",
		Exchange: "US",
		Name:     "Apple Inc",
		Type:     "Common Stock",
		Currency: "USD",
	}

	_, ids := stockFromSearch(context.Background(), r, nil)

	if len(ids) != 1 {
		t.Errorf("got %d identifiers, want 1 (MIC_TICKER only)", len(ids))
	}
	if ids[0].Type != "MIC_TICKER" {
		t.Errorf("identifier type = %q, want MIC_TICKER", ids[0].Type)
	}
}

func TestStockFromSearch_NonStockType(t *testing.T) {
	r := &client.SearchResult{
		Code:     "SPY",
		Exchange: "US",
		Name:     "SPDR S&P 500 ETF",
		Type:     "ETF",
		Currency: "USD",
	}

	inst, _ := stockFromSearch(context.Background(), r, nil)

	if inst != nil {
		t.Error("expected nil instrument for non-stock type")
	}
}

func TestBestMatch_PrefersPrimary(t *testing.T) {
	results := []client.SearchResult{
		{Code: "AAPL", Exchange: "XETRA", Type: "Common Stock", IsPrimary: false},
		{Code: "AAPL", Exchange: "US", Type: "Common Stock", IsPrimary: true},
	}

	got := bestMatch(results, "")

	if got == nil {
		t.Fatal("expected a match")
	}
	if got.Exchange != "US" {
		t.Errorf("Exchange = %q, want US (primary)", got.Exchange)
	}
}

func TestBestMatch_ExchangeFilter(t *testing.T) {
	results := []client.SearchResult{
		{Code: "AAPL", Exchange: "US", Type: "Common Stock", IsPrimary: true},
		{Code: "AAPL", Exchange: "XETRA", Type: "Common Stock", IsPrimary: false},
		{Code: "AAPL", Exchange: "LSE", Type: "Common Stock", IsPrimary: false},
	}

	got := bestMatch(results, "XETRA")

	if got == nil {
		t.Fatal("expected a match")
	}
	if got.Exchange != "XETRA" {
		t.Errorf("Exchange = %q, want XETRA", got.Exchange)
	}
}

func TestBestMatch_NoResults(t *testing.T) {
	got := bestMatch(nil, "")

	if got != nil {
		t.Error("expected nil for empty results")
	}
}

// stubExchDB implements ExchangeCodeDB for testing.
type stubExchDB struct {
	mics map[string][]string
}

func (s *stubExchDB) LookupMICsByEODHDCode(_ context.Context, code string) ([]string, error) {
	return s.mics[code], nil
}

func TestStockFromSearch_WithExchangeDB(t *testing.T) {
	db := &stubExchDB{mics: map[string][]string{"US": {"XNAS", "XNYS", "OTCM"}}}
	r := &client.SearchResult{
		Code:     "AAPL",
		Exchange: "US",
		Name:     "Apple Inc",
		Type:     "Common Stock",
		Currency: "USD",
		ISIN:     "US0378331005",
	}

	inst, ids := stockFromSearch(context.Background(), r, db)

	if inst == nil {
		t.Fatal("expected instrument")
	}
	if inst.Exchange != "XNAS" {
		t.Errorf("Exchange = %q, want XNAS (first MIC for US)", inst.Exchange)
	}
	// MIC_TICKER domain should also be the resolved exchange
	for _, id := range ids {
		if id.Type == "MIC_TICKER" && id.Domain != "XNAS" {
			t.Errorf("MIC_TICKER Domain = %q, want XNAS", id.Domain)
		}
	}
}

func TestResolveExchange_NilDB(t *testing.T) {
	got := resolveExchange(context.Background(), "US", nil)
	if got != "" {
		t.Errorf("resolveExchange with nil DB = %q, want empty", got)
	}
}

func TestResolveExchange_EmptyCode(t *testing.T) {
	db := &stubExchDB{mics: map[string][]string{"US": {"XNAS"}}}
	got := resolveExchange(context.Background(), "", db)
	if got != "" {
		t.Errorf("resolveExchange with empty code = %q, want empty", got)
	}
}

func TestBestMatch_FiltersNonStock(t *testing.T) {
	results := []client.SearchResult{
		{Code: "SPY", Exchange: "US", Type: "ETF", IsPrimary: true},
	}

	got := bestMatch(results, "")

	if got != nil {
		t.Error("expected nil when no stock types")
	}
}
