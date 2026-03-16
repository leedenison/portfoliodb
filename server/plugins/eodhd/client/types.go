package client

// SearchResult holds one item from the EODHD Search API response.
type SearchResult struct {
	Code      string `json:"Code"`
	Exchange  string `json:"Exchange"`
	Name      string `json:"Name"`
	Type      string `json:"Type"`      // e.g. "Common Stock", "ETF", "Fund"
	Country   string `json:"Country"`
	Currency  string `json:"Currency"`  // e.g. "USD"
	ISIN      string `json:"ISIN"`
	IsPrimary bool   `json:"isPrimary"` // true when this is the primary exchange listing
}
