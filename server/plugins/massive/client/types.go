package client

// APIResponse is the common envelope for Massive REST API responses.
type APIResponse[T any] struct {
	Status    string `json:"status"`
	RequestID string `json:"request_id"`
	Count     int    `json:"count,omitempty"`
	Results   T      `json:"results"`
}

// TickerOverviewResult holds reference data for a single ticker from GET /v3/reference/tickers/{ticker}.
type TickerOverviewResult struct {
	Ticker          string `json:"ticker"`
	Name            string `json:"name"`
	Market          string `json:"market"`           // "stocks", "indices", "crypto", "fx", "otc"
	Type            string `json:"type"`             // e.g. "CS" (common stock), "ETF", "INDEX"
	Active          bool   `json:"active"`           // currently trading
	PrimaryExchange string `json:"primary_exchange"` // ISO MIC code
	CurrencyName    string `json:"currency_name"`    // trading currency (e.g. "usd")
	CompositeFIGI   string `json:"composite_figi"`
	ShareClassFIGI  string `json:"share_class_figi"`
	ListDate        string `json:"list_date"`     // YYYY-MM-DD
	TickerRoot      string `json:"ticker_root"`   // base symbol (e.g. "BRK" for "BRK.A")
	TickerSuffix    string `json:"ticker_suffix"` // class extension (e.g. "A")
	SICCode         string `json:"sic_code"`
	SICDescription  string `json:"sic_description"`
}

// AggBar is one daily OHLCV bar from GET /v2/aggs/ticker/{ticker}/range/1/day/{from}/{to}.
type AggBar struct {
	O  float64 `json:"o"`  // open
	H  float64 `json:"h"`  // high
	L  float64 `json:"l"`  // low
	C  float64 `json:"c"`  // close
	V  int64   `json:"v"`  // volume
	VW float64 `json:"vw"` // volume-weighted average price
	T  int64   `json:"t"`  // Unix millisecond timestamp
	N  int     `json:"n"`  // number of trades
}

// OptionsContractResult holds reference data for a single options contract from GET /v3/reference/options/contracts/{options_ticker}.
type OptionsContractResult struct {
	Ticker            string  `json:"ticker"`
	UnderlyingTicker  string  `json:"underlying_ticker"`
	ContractType      string  `json:"contract_type"`      // "call", "put", "other"
	ExerciseStyle     string  `json:"exercise_style"`     // "american", "european", "bermudan"
	ExpirationDate    string  `json:"expiration_date"`    // YYYY-MM-DD
	StrikePrice       float64 `json:"strike_price"`
	SharesPerContract int     `json:"shares_per_contract"`
	PrimaryExchange   string  `json:"primary_exchange"` // MIC code
	CFI               string  `json:"cfi"`              // ISO 10962 classification
}
