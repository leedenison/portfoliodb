// Package derivative provides parsing of derivative identifiers (e.g. option tickers)
// to extract underlying symbol, format, and optional fields (expiry, put/call, strike).
// It is separate from identification plugins so that other code (e.g. pricing, display) can reuse it.
package derivative

import (
	"fmt"
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

// occSuffixLen is the fixed length of the OCC suffix: expiry(6) + C/P(1) + strike(8).
const occSuffixLen = 15

var occSuffixRe = regexp.MustCompile(`^\d{6}[CP]\d{8}$`)

// OCCCompact strips spaces from an OCC identifier and returns the compact form
// (e.g. "AAPL251219C00230000"). Returns ("", false) if the input is not a valid OCC.
func OCCCompact(occ string) (string, bool) {
	compact := strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(occ)), " ", "")
	if len(compact) < occSuffixLen+1 || len(compact) > occSuffixLen+6 {
		return "", false
	}
	suffix := compact[len(compact)-occSuffixLen:]
	if !occSuffixRe.MatchString(suffix) {
		return "", false
	}
	return compact, true
}

// OCCExpiry extracts the expiration date from a compact OCC identifier
// (e.g. "AAPL251219C00230000"). Returns (zero, false) if the OCC is invalid.
func OCCExpiry(occ string) (time.Time, bool) {
	compact, ok := OCCCompact(occ)
	if !ok {
		return time.Time{}, false
	}
	suffix := compact[len(compact)-occSuffixLen:]
	return parseYYMMDD(suffix[:6])
}

// OCCPadded normalizes an OCC identifier to the standard 21-character space-padded
// format (e.g. "AAPL  251219C00230000"). Returns ("", false) if the input is not a valid OCC.
func OCCPadded(occ string) (string, bool) {
	compact, ok := OCCCompact(occ)
	if !ok {
		return "", false
	}
	root := compact[:len(compact)-occSuffixLen]
	padded := root + strings.Repeat(" ", 6-len(root)) + compact[len(compact)-occSuffixLen:]
	return padded, true
}

// BuildOCCCompact constructs a compact OCC identifier from its components.
// Symbol must be 1-6 uppercase letters/digits. PutCall must be "C" or "P".
// Strike is encoded as price*1000 zero-padded to 8 digits.
// Returns ("", false) if the inputs are invalid.
func BuildOCCCompact(symbol string, expiry time.Time, putCall string, strike float64) (string, bool) {
	sym := strings.TrimSpace(strings.ToUpper(symbol))
	if len(sym) < 1 || len(sym) > 6 {
		return "", false
	}
	pc := strings.ToUpper(putCall)
	if pc != "C" && pc != "P" {
		return "", false
	}
	if expiry.IsZero() || strike < 0 {
		return "", false
	}
	strikeCents := int(strike*1000 + 0.5)
	if strikeCents > 99999999 {
		return "", false
	}
	yy := expiry.Year() % 100
	return sym + fmt.Sprintf("%02d%02d%02d", yy, expiry.Month(), expiry.Day()) + pc + fmt.Sprintf("%08d", strikeCents), true
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
