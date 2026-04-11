package vcr

import "testing"

func TestIsRecordingSuite(t *testing.T) {
	tests := []struct {
		name    string
		vcrMode string
		suite   string
		want    bool
	}{
		{name: "empty_mode", vcrMode: "", suite: "eodhd/identifier", want: false},
		{name: "single_match", vcrMode: "eodhd/identifier", suite: "eodhd/identifier", want: true},
		{name: "single_no_match", vcrMode: "eodhd/identifier", suite: "massive/price", want: false},
		{name: "multi_first", vcrMode: "eodhd/identifier,massive/price", suite: "eodhd/identifier", want: true},
		{name: "multi_second", vcrMode: "eodhd/identifier,massive/price", suite: "massive/price", want: true},
		{name: "multi_no_match", vcrMode: "eodhd/identifier,massive/price", suite: "openai/description", want: false},
		{name: "whitespace_trim", vcrMode: " eodhd/identifier , massive/price ", suite: "massive/price", want: true},
		{name: "partial_no_match", vcrMode: "eodhd/identifier", suite: "eodhd", want: false},
		{name: "e2e_cassette", vcrMode: "ingestion-flow,fetch-blocks", suite: "ingestion-flow", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VCR_MODE", tc.vcrMode)
			if got := IsRecordingSuite(tc.suite); got != tc.want {
				t.Errorf("IsRecordingSuite(%q) = %v, want %v (VCR_MODE=%q)", tc.suite, got, tc.want, tc.vcrMode)
			}
		})
	}
}

func TestIsRecording(t *testing.T) {
	tests := []struct {
		name    string
		vcrMode string
		want    bool
	}{
		{name: "empty", vcrMode: "", want: false},
		{name: "suite_list", vcrMode: "eodhd/identifier", want: true},
		{name: "multi", vcrMode: "eodhd/identifier,massive/price", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VCR_MODE", tc.vcrMode)
			if got := IsRecording(); got != tc.want {
				t.Errorf("IsRecording() = %v, want %v (VCR_MODE=%q)", got, tc.want, tc.vcrMode)
			}
		})
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "massive daily bars",
			in:   "https://api.massive.com/v2/aggs/ticker/TSLA/range/1/day/2024-01-29/2026-03-26?adjusted=false&apiKey=REDACTED&sort=asc",
			want: "https://api.massive.com/v2/aggs/ticker/TSLA/range/1/day/DATE/DATE?adjusted=false&apiKey=REDACTED&sort=asc",
		},
		{
			name: "no dates in path or query",
			in:   "https://api.massive.com/v3/reference/tickers/AAPL?apiKey=REDACTED",
			want: "https://api.massive.com/v3/reference/tickers/AAPL?apiKey=REDACTED",
		},
		{
			name: "eodhd search no dates",
			in:   "https://eodhd.com/api/search/US0378331005?api_token=REDACTED&fmt=json&limit=10&type=stock",
			want: "https://eodhd.com/api/search/US0378331005?api_token=REDACTED&fmt=json&limit=10&type=stock",
		},
		{
			name: "different end dates match after normalization",
			in:   "https://api.massive.com/v2/aggs/ticker/AMZN/range/1/day/2024-01-27/2026-03-27?adjusted=false&apiKey=REDACTED&sort=asc",
			want: "https://api.massive.com/v2/aggs/ticker/AMZN/range/1/day/DATE/DATE?adjusted=false&apiKey=REDACTED&sort=asc",
		},
		{
			name: "eodhd splits with date query params",
			in:   "https://eodhd.com/api/splits/AAPL.US?api_token=REDACTED&fmt=json&from=2024-06-15&to=2026-05-11",
			want: "https://eodhd.com/api/splits/AAPL.US?api_token=REDACTED&fmt=json&from=DATE&to=DATE",
		},
		{
			name: "eodhd dividends with date query params",
			in:   "https://eodhd.com/api/div/AAPL.US?api_token=REDACTED&fmt=json&from=2024-06-15&to=2026-05-11",
			want: "https://eodhd.com/api/div/AAPL.US?api_token=REDACTED&fmt=json&from=DATE&to=DATE",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeURL(tc.in)
			if got != tc.want {
				t.Errorf("normalizeURL(%q)\n got  %q\n want %q", tc.in, got, tc.want)
			}
		})
	}
}
