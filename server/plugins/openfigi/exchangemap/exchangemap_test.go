package exchangemap

import "testing"

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
