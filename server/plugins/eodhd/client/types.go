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
	Warning  string  `json:"warning,omitempty"`
}

// SplitRow holds one stock split from the EODHD splits API.
// The Split field is formatted as "{split_to}/{split_from}", e.g. "4.000000/1.000000"
// for a 4:1 split.
type SplitRow struct {
	Date  string `json:"date"`  // YYYY-MM-DD ex-date
	Split string `json:"split"` // "to/from"
}

// DividendRow holds one cash dividend from the EODHD dividends API.
// Value is the split-adjusted dividend (changes when later splits occur);
// UnadjustedValue is the actual cash paid on PaymentDate. PortfolioDB stores
// UnadjustedValue so the recorded amount does not drift.
type DividendRow struct {
	Date            string  `json:"date"`            // YYYY-MM-DD ex-date
	DeclarationDate string  `json:"declarationDate"` // optional
	RecordDate      string  `json:"recordDate"`      // optional
	PaymentDate     string  `json:"paymentDate"`     // optional
	Period          string  `json:"period"`          // e.g. "Quarterly", "Monthly"; optional
	Value           float64 `json:"value"`
	UnadjustedValue float64 `json:"unadjustedValue"`
	Currency        string  `json:"currency"`
}
