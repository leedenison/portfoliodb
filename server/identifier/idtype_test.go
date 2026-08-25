package identifier

import (
	"testing"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
)

// notCanonical are members of the proto enum that are not identifier types.
// UNSPECIFIED is the zero member every proto3 enum carries, and OPENFIGI_GLOBAL
// named the venue-specific FIGI before it moved to the provider identifiers,
// where providerIDTypes carries it as FIGI.
var notCanonical = map[string]bool{
	"IDENTIFIER_TYPE_UNSPECIFIED": true,
	"OPENFIGI_GLOBAL":             true,
}

// The vocabulary is the proto enum, so a type added there and not to the table
// is a gap rather than a type that happens to be unknown. This is the test that
// says so; without it the miss is silent until something asks a question of the
// new type and gets false.
func TestPropsCoversProtoVocabulary(t *testing.T) {
	seen := make(map[string]bool, len(idTypes))
	for _, name := range typev1.IdentifierType_name {
		if notCanonical[name] {
			continue
		}
		seen[name] = true
		p, ok := Props(name)
		if !ok {
			t.Errorf("%s: no entry in idTypes", name)
			continue
		}
		if p.Scope == ScopeUnknown {
			t.Errorf("%s: scope not declared", name)
		}
		if p.Reassignment == ReassignUnknown {
			t.Errorf("%s: reassignment not declared", name)
		}
		if p.Grain == GrainUnknown {
			t.Errorf("%s: grain not declared", name)
		}
		if p.Domain == DomainUnknown {
			t.Errorf("%s: domain not declared", name)
		}
		if p.Lines == LinesUnknown {
			t.Errorf("%s: lines not declared", name)
		}
		// A security-grain type has no line for a domain to pick out, so its
		// domain names something beside the value however it is spelled.
		if p.Grain == GrainSecurity && p.Domain == DomainScopes {
			t.Errorf("%s: security grain with a domain that scopes the value", name)
		}
		// A listing-grain value names one line by definition. Lines says so per
		// entry and this is what keeps the two from drifting: a listing-grain
		// type declared LinesMany would be the silent false this table exists to
		// prevent, since ReachesOneLine and everything downstream of it read
		// Lines rather than Grain.
		if p.Grain == GrainListing && p.Lines != LinesOne {
			t.Errorf("%s: listing grain but lines is not one", name)
		}
		// NamesTheSecurity reads Lines and ignores the domain, which is only
		// sound while no LinesMany type has one that scopes its value. Asserted
		// here rather than left to that function's comment.
		if p.Lines == LinesMany && p.Domain == DomainScopes {
			t.Errorf("%s: reaches many lines and has a domain that scopes the value", name)
		}
	}
	for name := range idTypes {
		if !seen[name] {
			t.Errorf("%s: in idTypes but not in the proto vocabulary", name)
		}
	}
}

// What a value names, which decides both where its row is stored and whether two
// values under two domains are two things.
func TestNamesAListing(t *testing.T) {
	for _, tt := range []struct {
		typ  string
		want bool
	}{
		{"MIC_TICKER", true},
		{"OPENFIGI_TICKER", true},
		// Listing-grain with no domain to say so. Both are issued per market,
		// and a market's venues share a currency, so each names a line on its
		// own -- which is the case a rule reading grain off the domain gets
		// backwards.
		{"SEDOL", true},
		{"OPENFIGI_COMPOSITE", true},
		{"ISIN", false},
		// The class the lines belong to, not one of them.
		{"OPENFIGI_SHARE_CLASS", false},
		// A contract is its own security and its symbol carries no venue.
		{"OCC", false},
		// The domain is the source that wrote the description, not a venue.
		{"BROKER_DESCRIPTION", false},
		{"NOT_A_TYPE", false},
	} {
		t.Run(tt.typ, func(t *testing.T) {
			if got := NamesAListing(tt.typ); got != tt.want {
				t.Errorf("NamesAListing(%q) = %v, want %v", tt.typ, got, tt.want)
			}
		})
	}
}

// A provider type is a free-form string rather than a vocabulary member, so an
// undeclared one has to fail closed onto the security rather than read as the
// zero value of Grain and land on a listing nothing picked.
func TestProviderNamesAListing(t *testing.T) {
	for _, tt := range []struct {
		typ  string
		want bool
	}{
		{"SEGMENT_MIC_TICKER", true},
		{"EODHD_EXCH_CODE", true},
		{"FIGI", true},
		{"NOT_A_PROVIDER_TYPE", false},
		{"", false},
		// A canonical type is not a provider type, and asking the wrong table
		// must not answer for it.
		{"MIC_TICKER", false},
	} {
		t.Run(tt.typ, func(t *testing.T) {
			if got := ProviderNamesAListing(tt.typ); got != tt.want {
				t.Errorf("ProviderNamesAListing(%q) = %v, want %v", tt.typ, got, tt.want)
			}
		})
	}
}

// What one identifier, with the domain it carries, picks out. Two halves: Lines
// says whether values of the type reach a line, Domain whether this value is one
// of them.
func TestReachesOneLine(t *testing.T) {
	for _, tt := range []struct {
		name string
		id   Identifier
		want bool
	}{
		{"a ticker with its MIC", Identifier{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}, true},
		{"a bare ticker", Identifier{Type: "MIC_TICKER", Value: "AAPL"}, false},
		// A domain of blanks is not a venue, which is the reading the gate this
		// backs has always taken.
		{"a ticker whose MIC is blanks", Identifier{Type: "MIC_TICKER", Domain: "  ", Value: "AAPL"}, false},
		{"a ticker with a composite code", Identifier{Type: "OPENFIGI_TICKER", Domain: "US", Value: "AAPL"}, true},
		{"a bare OpenFIGI ticker", Identifier{Type: "OPENFIGI_TICKER", Value: "AAPL"}, false},
		// Listing-grain and needing no domain to say which line: each is issued
		// per market, and a market's venues share a currency.
		{"a SEDOL", Identifier{Type: "SEDOL", Value: "2046251"}, true},
		{"a composite FIGI", Identifier{Type: "OPENFIGI_COMPOSITE", Value: "BBG000B9XRY4"}, true},
		// Security-grain and still one line: the security each names has exactly
		// one. A contract is cleared in one place; a currency is the cash
		// instrument entire.
		{"a contract symbol", Identifier{Type: "OCC", Value: "AAPL  251219C00200000"}, true},
		{"a currency", Identifier{Type: "CURRENCY", Value: "USD"}, true},
		{"an FX pair", Identifier{Type: "FX_PAIR", Value: "GBPUSD"}, true},
		// Registry keys, which reach every line the security trades in.
		{"an ISIN", Identifier{Type: "ISIN", Value: "US0378331005"}, false},
		{"a CUSIP", Identifier{Type: "CUSIP", Value: "037833100"}, false},
		// The class the lines belong to, not one of them, so it leaves which
		// line exactly as open as an ISIN does.
		{"a share class FIGI", Identifier{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG001S5N8V8"}, false},
		// Its domain is the source that wrote the text, and it names no security
		// to have a line of.
		{"a broker description", Identifier{Type: "BROKER_DESCRIPTION", Domain: "SRC", Value: "APPLE INC"}, false},
		{"a type outside the vocabulary", Identifier{Type: "CONID", Domain: "IBKR", Value: "265598"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReachesOneLine(tt.id); got != tt.want {
				t.Errorf("ReachesOneLine(%v) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}

// Which identifiers a stated currency can complete: the ones that reached the
// security and left only the line open.
func TestNamesTheSecurity(t *testing.T) {
	for _, tt := range []struct {
		name string
		id   Identifier
		want bool
	}{
		{"an ISIN", Identifier{Type: "ISIN", Value: "US0378331005"}, true},
		{"a CUSIP", Identifier{Type: "CUSIP", Value: "037833100"}, true},
		// The class the lines belong to is a security, so a currency says which
		// of its lines.
		{"a share class FIGI", Identifier{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG001S5N8V8"}, true},
		// Already at one line, so there is nothing for a currency to complete.
		{"a SEDOL", Identifier{Type: "SEDOL", Value: "2046251"}, false},
		{"a contract symbol", Identifier{Type: "OCC", Value: "AAPL  251219C00200000"}, false},
		{"a ticker with its MIC", Identifier{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}, false},
		// Reached neither the line nor the security: tickers are reused across
		// venues, so a currency beside this names the line of no particular one.
		{"a bare ticker", Identifier{Type: "MIC_TICKER", Value: "AAPL"}, false},
		// Named no security at all, so it has no line for a currency to pick.
		{"a broker description", Identifier{Type: "BROKER_DESCRIPTION", Domain: "SRC", Value: "APPLE INC"}, false},
		{"a type outside the vocabulary", Identifier{Type: "CONID", Value: "265598"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := NamesTheSecurity(tt.id); got != tt.want {
				t.Errorf("NamesTheSecurity(%v) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}

func TestKnown(t *testing.T) {
	for _, tt := range []struct {
		typ  string
		want bool
	}{
		{"ISIN", true},
		{"BROKER_DESCRIPTION", true},
		{"OPENFIGI_GLOBAL", false},
		{"", false},
		{"isin", false},
	} {
		if got := Known(tt.typ); got != tt.want {
			t.Errorf("Known(%q) = %v, want %v", tt.typ, got, tt.want)
		}
	}
}

func TestMayMediate(t *testing.T) {
	tests := []struct {
		name        string
		typ         string
		systemOwned bool
		want        bool
	}{
		{"rarely reassigned FIGI mediates", "OPENFIGI_SHARE_CLASS", true, true},
		{"rarely reassigned ISIN mediates", "ISIN", true, true},
		{"CUSIP mediates", "CUSIP", true, true},
		{"routinely reused ticker does not", "MIC_TICKER", true, false},
		{"contract symbol does not", "OCC", true, false},
		{"description does not", "BROKER_DESCRIPTION", true, false},
		{"currency does not", "CURRENCY", true, false},
		// Ownership is the condition an implementation reading only the type
		// property would miss, and an ISIN is exactly where it would be missed.
		{"user-owned ISIN does not", "ISIN", false, false},
		{"user-owned FIGI does not", "OPENFIGI_COMPOSITE", false, false},
		// Listing grain is not the question MayMediate asks. Both of these name
		// a line and both are still rarely reassigned, which is the only
		// property that decides this.
		{"listing-grain composite mediates", "OPENFIGI_COMPOSITE", true, true},
		{"listing-grain SEDOL mediates", "SEDOL", true, true},
		// A type outside the vocabulary fails closed rather than reading as the
		// zero value of Reassignment.
		{"unknown type does not", "CONID", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MayMediate(tt.typ, tt.systemOwned); got != tt.want {
				t.Errorf("MayMediate(%q, %v) = %v, want %v", tt.typ, tt.systemOwned, got, tt.want)
			}
		})
	}
}
