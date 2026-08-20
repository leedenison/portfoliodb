package exchangemap

import (
	"slices"
	"testing"
)

func TestExchCodeToMICs(t *testing.T) {
	m := New()
	tests := []struct {
		code    string
		wantLen int
		first   string
	}{
		{"UN", 1, "XNYS"},
		{"UW", 1, "XNAS"},
		{"LN", 1, "XLON"},
		{"ZZZZ", 0, ""},
	}
	for _, tt := range tests {
		mics := m.ExchCodeToMICs(tt.code)
		if len(mics) != tt.wantLen {
			t.Errorf("ExchCodeToMICs(%q) len = %d, want %d", tt.code, len(mics), tt.wantLen)
			continue
		}
		if tt.wantLen > 0 && mics[0] != tt.first {
			t.Errorf("ExchCodeToMICs(%q)[0] = %q, want %q", tt.code, mics[0], tt.first)
		}
	}
}

func TestMICToExchCode(t *testing.T) {
	m := New()
	// XLON has a unique mapping; verify exact value.
	code, ok := m.MICToExchCode("XLON")
	if !ok || code != "LN" {
		t.Errorf("MICToExchCode(XLON) = (%q, %v), want (LN, true)", code, ok)
	}
	// XNYS and XNAS have multiple codes mapping to them (UA/UN/UP, UB/UQ/UR/UT/UW/UX).
	// Just verify they resolve to something.
	code, ok = m.MICToExchCode("XNYS")
	if !ok || code == "" {
		t.Errorf("MICToExchCode(XNYS) = (%q, %v), want non-empty", code, ok)
	}
	code, ok = m.MICToExchCode("XNAS")
	if !ok || code == "" {
		t.Errorf("MICToExchCode(XNAS) = (%q, %v), want non-empty", code, ok)
	}
	// Unknown MIC
	_, ok = m.MICToExchCode("ZZZZ")
	if ok {
		t.Error("MICToExchCode(ZZZZ) ok = true, want false")
	}
}

func TestCompositeCountry(t *testing.T) {
	m := New()
	// A composite names a market, and the country is what remains knowable
	// about a listing reported under one.
	if got := m.CompositeCountry("US"); got != "US" {
		t.Errorf("CompositeCountry(US) = %q, want US", got)
	}
	if got := m.CompositeCountry("LN"); got != "GB" {
		t.Errorf("CompositeCountry(LN) = %q, want GB", got)
	}
	// GR is Bloomberg's code for Germany, not Greece; the country column is what
	// says so, and reading the letters would get it wrong.
	if got := m.CompositeCountry("GR"); got != "DE" {
		t.Errorf("CompositeCountry(GR) = %q, want DE", got)
	}
	// A composite whose rows disagree about the country constrains nothing this
	// can express, so it is absent rather than approximated: EO is the
	// pan-European MTF book, and DE covers both Munich and Douala.
	for _, code := range []string{"EO", "DE"} {
		if got := m.CompositeCountry(code); got != "" {
			t.Errorf("CompositeCountry(%s) = %q, want empty", code, got)
		}
	}
	if got := m.CompositeCountry("ZZZZ"); got != "" {
		t.Errorf("CompositeCountry(ZZZZ) = %q, want empty", got)
	}
}

// A market-level answer is only comparable to another provider's venue through
// the country, so the MIC side of that comparison has to be answerable too.
func TestMICCountry(t *testing.T) {
	m := New()
	for mic, want := range map[string]string{"XNYS": "US", "XNAS": "US", "XLON": "GB", "XWBO": "AT"} {
		if got := m.MICCountry(mic); got != want {
			t.Errorf("MICCountry(%s) = %q, want %q", mic, got, want)
		}
	}
	if got := m.MICCountry("ZZZZ"); got != "" {
		t.Errorf("MICCountry(ZZZZ) = %q, want empty", got)
	}
}

// The two namespaces are held apart because a handful of codes mean different
// exchanges in each. Reading one through the other's map is the bug this guards.
func TestCompositeAndVenueNamespacesAreDistinct(t *testing.T) {
	m := New()
	if venue := m.ExchCodeToMICs("US"); len(venue) != 0 {
		t.Errorf("ExchCodeToMICs(US) = %v, want none: US is a composite, not a venue", venue)
	}
	if got := m.CompositeCountry("DU"); got != "AE" {
		t.Errorf("CompositeCountry(DU) = %q, want AE", got)
	}
	if got := m.ExchCodeToMICs("DU"); !slices.Equal(got, []string{"DIFX"}) {
		t.Errorf("ExchCodeToMICs(DU) = %v, want [DIFX]", got)
	}
}

// A MIC maps back to the venue that is it, never to a group it belongs to.
func TestMICToExchCodeExcludesComposites(t *testing.T) {
	m := New()
	code, ok := m.MICToExchCode("XNYS")
	if !ok {
		t.Fatal("MICToExchCode(XNYS) not found")
	}
	if code == "US" {
		t.Error("MICToExchCode(XNYS) = US, want a venue code")
	}
}
