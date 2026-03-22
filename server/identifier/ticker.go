package identifier

import "strings"

// splitTickerSeps is the set of characters used as separators in multi-class
// tickers (e.g. BRK.B, BRK-B, BRK/B, "BRK B").
const splitTickerSeps = ".-/ "

// NormalizeSplitTicker replaces any split-ticker separator (dot, dash, slash,
// space) with preferredSep. Returns ticker unchanged when it contains none of
// these characters.
func NormalizeSplitTicker(ticker, preferredSep string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(splitTickerSeps, r) {
			if len(preferredSep) > 0 {
				return rune(preferredSep[0])
			}
			return -1 // drop the separator
		}
		return r
	}, ticker)
}
