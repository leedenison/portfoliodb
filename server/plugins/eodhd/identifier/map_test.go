package identifier

import (
	"testing"

	"github.com/leedenison/portfoliodb/server/plugins/eodhd/client"
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/exchangemap"
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

	inst, ids := stockFromSearch(r, nil)

	if inst == nil {
		t.Fatal("expected instrument")
	}
	if inst.AssetClass != "STOCK" {
		t.Errorf("AssetClass = %q, want STOCK", inst.AssetClass)
	}
	if inst.Listing.Venue.MIC != "" {
		t.Errorf("Exchange = %q, want empty (no exchMap)", inst.Listing.Venue.MIC)
	}
	if inst.Listing.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", inst.Listing.Currency)
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

	_, ids := stockFromSearch(r, nil)

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

	inst, _ := stockFromSearch(r, nil)

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

// BRK.B is listed on NYSE and EODHD reports it under "US" like every other
// American stock, so nothing in the response says which of XNAS, XNYS and OTCM
// it is. Taking the first left XNAS on an NYSE issue, and stored it as the
// domain of a canonical MIC_TICKER; asserting the absence is what stops that
// coming back. The exchange the provider did name survives as EODHD_EXCH_CODE.
func TestStockFromSearch_CompositeExchangeIsNotResolvedToAVenue(t *testing.T) {
	exchMap := exchangemap.New()
	r := &client.SearchResult{
		Code:     "BRK-B",
		Exchange: "US",
		Name:     "Berkshire Hathaway Inc",
		Type:     "Common Stock",
		Currency: "USD",
		ISIN:     "US0846707026",
	}

	inst, ids := stockFromSearch(r, exchMap)

	if inst == nil {
		t.Fatal("expected instrument")
	}
	if inst.Listing.Venue.MIC != "" {
		t.Errorf("Exchange = %q, want empty: US covers XNAS, XNYS and OTCM and the result names none of them", inst.Listing.Venue.MIC)
	}
	for _, id := range ids {
		if id.Type == "MIC_TICKER" && id.Domain != "" {
			t.Errorf("MIC_TICKER Domain = %q, want empty", id.Domain)
		}
	}
	var code string
	for _, pi := range inst.ProviderIdentifiers {
		if pi.Type == "EODHD_EXCH_CODE" {
			code = pi.Value
		}
	}
	if code != "US" {
		t.Errorf("EODHD_EXCH_CODE = %q, want US: what the provider said is still recorded", code)
	}
}

// A code that names one operating MIC still resolves to it.
func TestStockFromSearch_SingleMICExchangeResolves(t *testing.T) {
	exchMap := exchangemap.New()
	r := &client.SearchResult{
		Code:     "VOD",
		Exchange: "LSE",
		Name:     "Vodafone Group Plc",
		Type:     "Common Stock",
		Currency: "GBX",
	}

	inst, ids := stockFromSearch(r, exchMap)

	if inst == nil {
		t.Fatal("expected instrument")
	}
	if inst.Listing.Venue.MIC != "XLON" {
		t.Errorf("Exchange = %q, want XLON", inst.Listing.Venue.MIC)
	}
	for _, id := range ids {
		if id.Type == "MIC_TICKER" && id.Domain != "XLON" {
			t.Errorf("MIC_TICKER Domain = %q, want XLON", id.Domain)
		}
	}
}

func TestResolveExchange_NilMap(t *testing.T) {
	got := resolveVenue("US", "USA", nil).MIC
	if got != "" {
		t.Errorf("resolveVenue with nil map = %q, want empty", got)
	}
}

func TestResolveExchange_EmptyCode(t *testing.T) {
	exchMap := exchangemap.New()
	got := resolveVenue("", "", exchMap).MIC
	if got != "" {
		t.Errorf("resolveVenue with empty code = %q, want empty", got)
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
