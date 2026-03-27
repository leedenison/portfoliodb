package vcr

import "testing"

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
			name: "no dates in path",
			in:   "https://api.massive.com/v3/reference/tickers/AAPL?apiKey=REDACTED",
			want: "https://api.massive.com/v3/reference/tickers/AAPL?apiKey=REDACTED",
		},
		{
			name: "eodhd search",
			in:   "https://eodhd.com/api/search/US0378331005?api_token=REDACTED&fmt=json&limit=10&type=stock",
			want: "https://eodhd.com/api/search/US0378331005?api_token=REDACTED&fmt=json&limit=10&type=stock",
		},
		{
			name: "different end dates match after normalization",
			in:   "https://api.massive.com/v2/aggs/ticker/AMZN/range/1/day/2024-01-27/2026-03-27?adjusted=false&apiKey=REDACTED&sort=asc",
			want: "https://api.massive.com/v2/aggs/ticker/AMZN/range/1/day/DATE/DATE?adjusted=false&apiKey=REDACTED&sort=asc",
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
