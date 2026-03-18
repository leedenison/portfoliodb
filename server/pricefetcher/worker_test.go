package pricefetcher

import (
	"context"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
)

func TestPluginAccepts(t *testing.T) {
	tests := []struct {
		name   string
		plugin *filterStub
		inst   *db.InstrumentRow
		want   bool
	}{
		{
			name:   "all nil filters accept anything",
			plugin: &filterStub{},
			inst:   &db.InstrumentRow{AssetClass: "STOCK", Exchange: "XNAS", Currency: "USD"},
			want:   true,
		},
		{
			name:   "asset class mismatch",
			plugin: &filterStub{assetClasses: map[string]bool{"STOCK": true}},
			inst:   &db.InstrumentRow{AssetClass: "OPTION"},
			want:   false,
		},
		{
			name:   "asset class match",
			plugin: &filterStub{assetClasses: map[string]bool{"STOCK": true, "ETF": true}},
			inst:   &db.InstrumentRow{AssetClass: "ETF"},
			want:   true,
		},
		{
			name:   "null asset class passes",
			plugin: &filterStub{assetClasses: map[string]bool{"STOCK": true}},
			inst:   &db.InstrumentRow{},
			want:   true,
		},
		{
			name:   "currency mismatch",
			plugin: &filterStub{currencies: map[string]bool{"USD": true}},
			inst:   &db.InstrumentRow{Currency: "EUR"},
			want:   false,
		},
		{
			name:   "currency match case insensitive",
			plugin: &filterStub{currencies: map[string]bool{"USD": true}},
			inst:   &db.InstrumentRow{Currency: "usd"},
			want:   true,
		},
		{
			name:   "null currency passes",
			plugin: &filterStub{currencies: map[string]bool{"USD": true}},
			inst:   &db.InstrumentRow{},
			want:   true,
		},
		{
			name:   "exchange mismatch",
			plugin: &filterStub{exchanges: map[string]bool{"XNAS": true}},
			inst:   &db.InstrumentRow{Exchange: "XNYS"},
			want:   false,
		},
		{
			name:   "null exchange passes",
			plugin: &filterStub{exchanges: map[string]bool{"XNAS": true}},
			inst:   &db.InstrumentRow{},
			want:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pluginAccepts(tc.plugin, tc.inst)
			if got != tc.want {
				t.Errorf("pluginAccepts = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFilterIdentifiers(t *testing.T) {
	ids := []db.IdentifierInput{
		{Type: "TICKER", Value: "AAPL"},
		{Type: "ISIN", Value: "US0378331005"},
		{Type: "OCC", Value: "AAPL250321C00150000"},
	}
	got := filterIdentifiers([]string{"TICKER", "OCC"}, ids)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].Type != "TICKER" || got[1].Type != "OCC" {
		t.Errorf("unexpected types: %s, %s", got[0].Type, got[1].Type)
	}
}

func TestTrigger(t *testing.T) {
	t.Run("nil channel", func(t *testing.T) {
		Trigger(nil) // should not panic
	})
	t.Run("sends signal", func(t *testing.T) {
		ch := make(chan struct{}, 1)
		Trigger(ch)
		select {
		case <-ch:
		default:
			t.Error("expected signal")
		}
	})
	t.Run("non-blocking when full", func(t *testing.T) {
		ch := make(chan struct{}, 1)
		ch <- struct{}{}
		Trigger(ch) // should not block
	})
}

// filterStub implements Plugin for testing pluginAccepts.
type filterStub struct {
	assetClasses map[string]bool
	exchanges    map[string]bool
	currencies   map[string]bool
}

func (s *filterStub) DisplayName() string                        { return "stub" }
func (s *filterStub) SupportedIdentifierTypes() []string         { return nil }
func (s *filterStub) AcceptableAssetClasses() map[string]bool    { return s.assetClasses }
func (s *filterStub) AcceptableExchanges() map[string]bool       { return s.exchanges }
func (s *filterStub) AcceptableCurrencies() map[string]bool      { return s.currencies }
func (s *filterStub) DefaultConfig() []byte                      { return nil }
func (s *filterStub) FetchPrices(_ context.Context, _ []byte, _ []Identifier, _ string, _, _ time.Time) (*FetchResult, error) {
	return nil, ErrNoData
}
