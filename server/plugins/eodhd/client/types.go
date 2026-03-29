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

// EODBar holds one day of OHLCV data from the EODHD End-of-Day API.
type EODBar struct {
	Date     string  `json:"date"`           // YYYY-MM-DD
	Open     float64 `json:"open"`
	High     float64 `json:"high"`
	Low      float64 `json:"low"`
	Close    float64 `json:"close"`
	AdjClose float64 `json:"adjusted_close"`
	Volume   int64   `json:"volume"`
}
