package currency

import "testing"

func TestFamily(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{"minor unit resolves to its major", "GBX", "GBP"},
		{"a major unit is its own family", "GBP", "GBP"},
		{"an unrelated code is its own family", "USD", "USD"},
		{"an empty code is left alone", "", ""},
		{"the family is case sensitive, as ISO codes are", "gbx", "gbx"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Family(tc.code); got != tc.want {
				t.Errorf("Family(%q) = %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}

// The table grows on evidence, so a new entry has to be looked at rather than
// arriving as a side effect: it widens the listing uniqueness index, adds a
// derived FX pair a plugin must be able to fetch, and changes what is sent to
// OpenFIGI.
func TestMinorUnits_holdsOnlyGBX(t *testing.T) {
	if len(MinorUnits) != 1 {
		t.Fatalf("MinorUnits has %d entries, want 1: %+v", len(MinorUnits), MinorUnits)
	}
	want := MinorUnit{Code: "GBX", Major: "GBP", Exponent: -2}
	if MinorUnits[0] != want {
		t.Errorf("MinorUnits[0] = %+v, want %+v", MinorUnits[0], want)
	}
}
