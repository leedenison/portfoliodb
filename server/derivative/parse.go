// Package derivative provides parsing of derivative identifiers (e.g. option tickers)
// to extract underlying symbol, format, and optional fields (expiry, put/call, strike).
// It is separate from identification plugins so that other code (e.g. pricing, display) can reuse it.
package derivative

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Format names returned by ParseOptionTicker.
const (
	FormatOCC     = "OCC"     // Options Clearing Corporation 21-char symbol
	FormatClassic = "Classic" // "SYMBOL MM/DD/YY C/P STRIKE"
	FormatCompact = "Compact" // SYMBOL + YYYYMMDD + C/P + strike (no separators)
)

// ParsedOption holds the result of parsing an option-style ticker.
// Expiry, PutCall, and Strike may be zero/empty when not parsed or not applicable.
type ParsedOption struct {
	Format       string    // OCC, Classic, Compact (or "" if unknown)
	Symbol       string    // Underlying ticker (e.g. "AAPL", "IBM")
	ExchangeHint string    // Optional exchange code hint when inferable; often empty
	Expiry       time.Time // Expiration date; zero when unknown
	PutCall      string    // "C" or "P"; empty when unknown
	Strike       float64   // Strike price; 0 when unknown
}

// OCC (Options Clearing Corporation) 21-character symbol.
// Layout: root(6, space-padded) + expiry(6 YYMMDD) + C/P(1) + strike(8, price*1000 zero-padded).
// Example: "AAPL  250117C00150000" = AAPL, 2025-01-17, Call, $150.
var occRe = regexp.MustCompile(`^([A-Z0-9\s]{6})(\d{6})([CP])(\d{8})$`)

// Classic: "SYMBOL MM/DD/YY C STRIKE" or "SYMBOL MM/DD/YY P STRIKE".
// Example: "IBM 03/20/10 C105".
var classicRe = regexp.MustCompile(`^([A-Z]{1,5})\s+(\d{1,2})/(\d{1,2})/(\d{2})\s+([CP])\s*(\d+(?:\.\d+)?)$`)

// Compact: SYMBOL + YYYYMMDD + C/P + strike digits, no spaces.
// Example: "AAPL20250117C200".
var compactRe = regexp.MustCompile(`^([A-Z]{1,5})(\d{8})([CP])(\d+(?:\.\d+)?)$`)

// ParseOptionTicker parses an option-style ticker into format, underlying symbol, and optional expiry/put-call/strike.
// Tries OCC (21-char), then Classic ("SYMBOL MM/DD/YY C/P STRIKE"), then Compact (SYMBOL+YYYYMMDD+C/P+strike).
// Returns (nil, false) when the ticker cannot be parsed. ExchangeHint is left empty for all current formats.
func ParseOptionTicker(optionTicker string) (*ParsedOption, bool) {
	s := strings.TrimSpace(optionTicker)
	if s == "" {
		return nil, false
	}
	upper := strings.ToUpper(s)

	// OCC: 21-char symbol = root(6) + expiry(6 YYMMDD) + C/P(1) + strike(8)
	if m := occRe.FindStringSubmatch(upper); len(m) == 5 {
		root := strings.TrimSpace(m[1])
		if root == "" {
			return nil, false
		}
		expiry, ok := parseYYMMDD(m[2])
		if !ok {
			return nil, false
		}
		strikeCents, err := strconv.Atoi(m[4])
		if err != nil {
			return nil, false
		}
		strike := float64(strikeCents) / 1000
		return &ParsedOption{
			Format:  FormatOCC,
			Symbol:  root,
			Expiry:  expiry,
			PutCall: m[3],
			Strike:  strike,
		}, true
	}

	// Classic: "SYMBOL MM/DD/YY C/P STRIKE"
	if m := classicRe.FindStringSubmatch(upper); len(m) == 7 {
		month, _ := strconv.Atoi(m[2])
		day, _ := strconv.Atoi(m[3])
		year2 := m[4]
		year := 2000
		if y, err := strconv.Atoi(year2); err == nil && y >= 0 && y <= 99 {
			if y >= 50 {
				year = 1900 + y
			} else {
				year = 2000 + y
			}
		}
		expiry := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
		strike, _ := strconv.ParseFloat(m[6], 64)
		return &ParsedOption{
			Format:  FormatClassic,
			Symbol:  m[1],
			Expiry:  expiry,
			PutCall: m[5],
			Strike:  strike,
		}, true
	}

	// Compact: SYMBOL + YYYYMMDD + C/P + strike
	if m := compactRe.FindStringSubmatch(upper); len(m) == 5 {
		expiry, ok := parseYYYYMMDD(m[2])
		if !ok {
			return nil, false
		}
		strike, _ := strconv.ParseFloat(m[4], 64)
		return &ParsedOption{
			Format:  FormatCompact,
			Symbol:  m[1],
			Expiry:  expiry,
			PutCall: m[3],
			Strike:  strike,
		}, true
	}

	return nil, false
}

func parseYYMMDD(yymmdd string) (time.Time, bool) {
	if len(yymmdd) != 6 {
		return time.Time{}, false
	}
	yy, e1 := strconv.Atoi(yymmdd[0:2])
	mm, e2 := strconv.Atoi(yymmdd[2:4])
	dd, e3 := strconv.Atoi(yymmdd[4:6])
	if e1 != nil || e2 != nil || e3 != nil {
		return time.Time{}, false
	}
	year := 2000 + yy
	if yy >= 50 {
		year = 1900 + yy
	}
	if mm < 1 || mm > 12 || dd < 1 || dd > 31 {
		return time.Time{}, false
	}
	return time.Date(year, time.Month(mm), dd, 0, 0, 0, 0, time.UTC), true
}

func parseYYYYMMDD(yyyymmdd string) (time.Time, bool) {
	if len(yyyymmdd) != 8 {
		return time.Time{}, false
	}
	yyyy, e1 := strconv.Atoi(yyyymmdd[0:4])
	mm, e2 := strconv.Atoi(yyyymmdd[4:6])
	dd, e3 := strconv.Atoi(yyyymmdd[6:8])
	if e1 != nil || e2 != nil || e3 != nil {
		return time.Time{}, false
	}
	if mm < 1 || mm > 12 || dd < 1 || dd > 31 {
		return time.Time{}, false
	}
	return time.Date(yyyy, time.Month(mm), dd, 0, 0, 0, 0, time.UTC), true
}
