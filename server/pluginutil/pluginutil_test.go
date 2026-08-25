package pluginutil

import (
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
)

func strPtr(s string) *string { return &s }

// lines builds a security's listings from (currency, mics...) pairs, the venues
// being MICs alone because the reference data on them is nothing this reads.
func lines(spec ...[]string) []*db.Listing {
	out := make([]*db.Listing, len(spec))
	for i, s := range spec {
		l := &db.Listing{Currency: s[0]}
		for _, m := range s[1:] {
			l.Venues = append(l.Venues, db.Venue{MIC: m})
		}
		out[i] = l
	}
	return out
}

func TestPluginAccepts(t *testing.T) {
	tests := []struct {
		name string
		ac   map[string]bool
		ex   map[string]bool
		cu   map[string]bool
		inst *db.InstrumentRow
		want bool
	}{
		{
			name: "nil filters accept anything",
			inst: &db.InstrumentRow{AssetClass: strPtr("STOCK"), Listings: lines([]string{"USD", "XNAS"})},
			want: true,
		},
		{
			name: "asset class mismatch",
			ac:   map[string]bool{"STOCK": true},
			inst: &db.InstrumentRow{AssetClass: strPtr("OPTION")},
			want: false,
		},
		{
			name: "asset class match",
			ac:   map[string]bool{"STOCK": true, "ETF": true},
			inst: &db.InstrumentRow{AssetClass: strPtr("ETF")},
			want: true,
		},
		{
			name: "nil asset class passes filter",
			ac:   map[string]bool{"STOCK": true},
			inst: &db.InstrumentRow{},
			want: true,
		},
		{
			name: "empty asset class passes filter",
			ac:   map[string]bool{"STOCK": true},
			inst: &db.InstrumentRow{AssetClass: strPtr("")},
			want: true,
		},
		{
			name: "currency case insensitive",
			cu:   map[string]bool{"USD": true},
			inst: &db.InstrumentRow{Listings: lines([]string{"usd"})},
			want: true,
		},
		{
			name: "currency mismatch",
			cu:   map[string]bool{"USD": true},
			inst: &db.InstrumentRow{Listings: lines([]string{"EUR"})},
			want: false,
		},
		{
			// A split is an action on the security and applies to every line of
			// it, so a plugin carrying one of the currencies the security trades
			// in can answer about it. Refusing because a sibling line is in a
			// currency the plugin does not carry would lose the events for the
			// line it does.
			name: "any line's currency in the plugin's set is enough",
			cu:   map[string]bool{"USD": true},
			inst: &db.InstrumentRow{Listings: lines([]string{"GBP"}, []string{"USD"})},
			want: true,
		},
		{
			name: "no line passes the currency filter",
			cu:   map[string]bool{"USD": true},
			inst: &db.InstrumentRow{},
			want: true,
		},
		{
			name: "exchange mismatch",
			ex:   map[string]bool{"XNAS": true},
			inst: &db.InstrumentRow{Listings: lines([]string{"USD", "XNYS"})},
			want: false,
		},
		{
			name: "any line's venue in the plugin's set is enough",
			ex:   map[string]bool{"XNAS": true},
			inst: &db.InstrumentRow{Listings: lines([]string{"GBP", "XLON"}, []string{"USD", "XNAS"})},
			want: true,
		},
		{
			// The venue set is what we have been told about rather than what
			// exists, so a security no line of which records a venue has nothing
			// to fail on. See adr/0077.
			name: "a security with no venue anywhere passes the filter",
			ex:   map[string]bool{"XNAS": true},
			inst: &db.InstrumentRow{Listings: lines([]string{"USD"})},
			want: true,
		},
		{
			name: "no listings at all passes the filter",
			ex:   map[string]bool{"XNAS": true},
			inst: &db.InstrumentRow{},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PluginAccepts(tc.ac, tc.ex, tc.cu, tc.inst); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFilterIdentifiers(t *testing.T) {
	ids := []db.IdentifierInput{
		{
			Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL"},
		},
		{
			Ref: db.InstrumentRef{Type: "ISIN", Value: "US0378331005"},
		},
		{
			Ref: db.InstrumentRef{Type: "OCC", Value: "AAPL250321C00150000"},
		}}
	got := FilterIdentifiers([]string{"MIC_TICKER", "OCC"}, ids)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].Ref.Type != "MIC_TICKER" || got[1].Ref.Type != "OCC" {
		t.Errorf("unexpected types: %s, %s", got[0].Ref.Type, got[1].Ref.Type)
	}
}

func TestFilterIdentifiers_NoMatch(t *testing.T) {
	ids := []db.IdentifierInput{{
		Ref: db.InstrumentRef{Type: "ISIN", Value: "US0378331005"},
	}}
	got := FilterIdentifiers([]string{"MIC_TICKER"}, ids)
	if len(got) != 0 {
		t.Fatalf("expected 0, got %d", len(got))
	}
}

func TestTimeoutFromConfig(t *testing.T) {
	def := 45 * time.Second
	if TimeoutFromConfig(nil, def) != def {
		t.Error("nil config should return default")
	}
	if TimeoutFromConfig([]byte(`{"timeout_seconds": 30}`), def) != 30*time.Second {
		t.Error("explicit 30s")
	}
	if TimeoutFromConfig([]byte(`{"timeout_seconds": -5}`), def) != def {
		t.Error("negative should return default")
	}
	if TimeoutFromConfig([]byte(`{"timeout_seconds": 0}`), def) != def {
		t.Error("zero should return default")
	}
	if TimeoutFromConfig([]byte(`not json`), def) != def {
		t.Error("invalid json should return default")
	}
	if TimeoutFromConfig([]byte(`{}`), def) != def {
		t.Error("missing key should return default")
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
