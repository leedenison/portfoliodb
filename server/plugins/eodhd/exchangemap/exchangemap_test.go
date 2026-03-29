package exchangemap

import "testing"

func TestMICToEODHDCode(t *testing.T) {
	m := New()
	tests := []struct {
		mic      string
		wantCode string
		wantOK   bool
	}{
		{"XNAS", "US", true},
		{"XNYS", "US", true},
		{"OTCM", "US", true},
		{"XLON", "LSE", true},
		{"XETR", "XETRA", true},
		{"XASX", "AU", true},
		{"ZZZZ", "", false},
	}
	for _, tc := range tests {
		code, ok := m.MICToEODHDCode(tc.mic)
		if code != tc.wantCode || ok != tc.wantOK {
			t.Errorf("MICToEODHDCode(%q) = (%q, %v), want (%q, %v)",
				tc.mic, code, ok, tc.wantCode, tc.wantOK)
		}
	}
}

func TestEODHDCodeToMICs(t *testing.T) {
	m := New()

	mics := m.EODHDCodeToMICs("US")
	if len(mics) != 3 || mics[0] != "XNAS" || mics[1] != "XNYS" || mics[2] != "OTCM" {
		t.Errorf("EODHDCodeToMICs(US) = %v, want [XNAS XNYS OTCM]", mics)
	}

	mics = m.EODHDCodeToMICs("LSE")
	if len(mics) != 1 || mics[0] != "XLON" {
		t.Errorf("EODHDCodeToMICs(LSE) = %v, want [XLON]", mics)
	}

	mics = m.EODHDCodeToMICs("UNKNOWN")
	if mics != nil {
		t.Errorf("EODHDCodeToMICs(UNKNOWN) = %v, want nil", mics)
	}
}
