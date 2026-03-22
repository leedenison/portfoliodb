package identifier

import "testing"

func TestNormalizeSplitTicker(t *testing.T) {
	tests := []struct {
		name         string
		ticker       string
		preferredSep string
		want         string
	}{
		{"dot to slash", "BRK.B", "/", "BRK/B"},
		{"slash to dot", "BRK/B", ".", "BRK.B"},
		{"dash to slash", "BRK-B", "/", "BRK/B"},
		{"space to dot", "BRK B", ".", "BRK.B"},
		{"already preferred dot", "BRK.B", ".", "BRK.B"},
		{"already preferred slash", "BRK/B", "/", "BRK/B"},
		{"no separator", "AAPL", "/", "AAPL"},
		{"no separator dot", "AAPL", ".", "AAPL"},
		{"empty ticker", "", ".", ""},
		{"drop separator", "BRK.B", "", "BRKB"},
		{"multiple seps normalized", "A.B-C/D E", ".", "A.B.C.D.E"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeSplitTicker(tt.ticker, tt.preferredSep)
			if got != tt.want {
				t.Errorf("NormalizeSplitTicker(%q, %q) = %q, want %q", tt.ticker, tt.preferredSep, got, tt.want)
			}
		})
	}
}
