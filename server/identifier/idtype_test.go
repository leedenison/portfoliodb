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
