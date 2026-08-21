package identifier

import (
	"testing"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
)

// notCanonical are members of the proto enum that are not identifier types.
// UNSPECIFIED is the zero member every proto3 enum carries, and OPENFIGI_GLOBAL
// named the venue-specific FIGI before it moved to
// provider_instrument_identifiers.
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
	}
	for name := range idTypes {
		if !seen[name] {
			t.Errorf("%s: in idTypes but not in the proto vocabulary", name)
		}
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
