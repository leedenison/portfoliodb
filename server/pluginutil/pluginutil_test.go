package pluginutil

import (
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
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

func TestAccepts(t *testing.T) {
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
		{
			// The currency is compared on the family, as every currency
			// comparison is: the line is one line whether it is quoted in
			// pounds or in pence. See adr/0068.
			name: "a plugin declaring GBP carries the line quoted in pence",
			cu:   map[string]bool{"GBP": true},
			inst: &db.InstrumentRow{Listings: lines([]string{"GBX", "XLON"})},
			want: true,
		},
		{
			name: "and a plugin declaring pence carries the line quoted in pounds",
			cu:   map[string]bool{"GBX": true},
			inst: &db.InstrumentRow{Listings: lines([]string{"GBP", "XLON"})},
			want: true,
		},
		{
			name: "a currency in no declared family is refused",
			cu:   map[string]bool{"GBP": true},
			inst: &db.InstrumentRow{Listings: lines([]string{"USD", "XNAS"})},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Accepts(tc.ac, tc.ex, tc.cu, tc.inst); got != tc.want {
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

// The line grain asks the same currency question the security grain does, and
// has to answer it the same way: a rule about what makes two lines cannot hold
// on one path and not another.
func TestAcceptsListing_CurrencyFamily(t *testing.T) {
	tests := []struct {
		name string
		cu   map[string]bool
		lst  *db.Listing
		want bool
	}{
		{"a plugin declaring GBP prices the line quoted in pence",
			map[string]bool{"GBP": true}, &db.Listing{Currency: "GBX"}, true},
		{"and the other way round",
			map[string]bool{"GBX": true}, &db.Listing{Currency: "GBP"}, true},
		{"case is folded, as it is for a code off a provider",
			map[string]bool{"USD": true}, &db.Listing{Currency: "usd"}, true},
		{"a currency in no declared family is refused",
			map[string]bool{"GBP": true}, &db.Listing{Currency: "USD"}, false},
		{"a plugin declaring nothing prices anything",
			nil, &db.Listing{Currency: "GBX"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AcceptsListing(nil, nil, tc.cu, nil, tc.lst); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAcceptsSecurityType(t *testing.T) {
	cashTypes := map[string]bool{identifier.SecurityTypeHintCash: true}
	stockTypes := map[string]bool{identifier.SecurityTypeHintStock: true, identifier.SecurityTypeHintFixedIncome: true}

	tests := []struct {
		name       string
		acceptable map[string]bool
		secType    string
		want       bool
	}{
		// A plugin covering shares is offered what a statement line says, which
		// is the coarse value rather than the leaf below it.
		{"stock plugin accepts EQUITY", stockTypes, identifier.SecurityTypeHintEquity, true},
		{"stock plugin accepts STOCK", stockTypes, identifier.SecurityTypeHintStock, true},
		{"stock plugin accepts one of several declared", stockTypes, identifier.SecurityTypeHintFixedIncome, true},

		// A row whose source said only that it is a security is offered every
		// security plugin, and none of them has to enumerate the vocabulary to
		// be reached by it.
		{"stock plugin accepts SECURITY", stockTypes, identifier.SecurityTypeHintSecurity, true},

		// And the coarse value does not reach across the tree.
		{"cash plugin rejects a security", cashTypes, identifier.SecurityTypeHintSecurity, false},
		{"cash plugin rejects EQUITY", cashTypes, identifier.SecurityTypeHintEquity, false},
		{"stock plugin rejects CASH", stockTypes, identifier.SecurityTypeHintCash, false},
		{"cash plugin accepts CASH", cashTypes, identifier.SecurityTypeHintCash, true},

		// A class the plugin does not cover is refused however specific it is.
		{"stock plugin rejects OPTION", stockTypes, identifier.SecurityTypeHintOption, false},
		{"stock plugin rejects ETF", stockTypes, identifier.SecurityTypeHintETF, false},

		// The root reaches everything: it rules nothing out, so no plugin can
		// be said not to cover it.
		{"cash plugin accepts the root", cashTypes, identifier.SecurityTypeHintUnknown, true},
		{"stock plugin accepts the root", stockTypes, identifier.SecurityTypeHintUnknown, true},

		{"a plugin declaring nothing accepts everything", nil, identifier.SecurityTypeHintOption, true},
		{"a row stating nothing reaches every plugin", cashTypes, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AcceptsSecurityType(tt.acceptable, tt.secType); got != tt.want {
				t.Errorf("AcceptsSecurityType(%v, %q) = %v, want %v", tt.acceptable, tt.secType, got, tt.want)
			}
		})
	}
}

func TestAcceptsCurrency(t *testing.T) {
	gbpEur := []string{"GBP", "EUR"}

	if !AcceptsCurrency(gbpEur, "GBP") {
		t.Error("expected GBP accepted")
	}
	if !AcceptsCurrency(gbpEur, "gbp") {
		t.Error("expected case-insensitive match")
	}
	if AcceptsCurrency(gbpEur, "USD") {
		t.Error("expected USD rejected")
	}
	// On the family: a plugin publishing an index for GBP publishes it for the
	// line quoted in pence. See adr/0068.
	if !AcceptsCurrency(gbpEur, "GBX") {
		t.Error("expected GBX accepted by a plugin declaring GBP")
	}
}
