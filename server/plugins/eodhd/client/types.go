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

// FundamentalsGeneral holds the General section from the EODHD Fundamentals API.
type FundamentalsGeneral struct {
	Code       string `json:"Code"`
	Name       string `json:"Name"`
	Exchange   string `json:"Exchange"`
	Currency   string `json:"CurrencyCode"`
	CountryISO string `json:"CountryISO"`
	Type       string `json:"Type"`
	Sector     string `json:"Sector"`
	Industry   string `json:"Industry"`
	ISIN       string `json:"ISIN"`
	CUSIP      string `json:"CUSIP"`
}
