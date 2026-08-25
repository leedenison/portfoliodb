package db

import "github.com/leedenison/portfoliodb/server/currency"

// LineFor is the line a currency names among the lines a security has, and the
// only statement of that rule. Every caller that has to place something on a
// line asks it: a posting being ingested, a resolution reading back what it just
// minted, a caller naming no currency at all.
//
// Two rungs, and each refuses rather than reaching past itself:
//
//   - A currency stated names the line in its family, and no line at all where
//     the security has none in that family. A broker states a currency to say
//     what its own figures are in, so a currency the security is not quoted in
//     is a disagreement rather than a gap, and the sole line is not the answer
//     to it: placing a posting that stated GBP on a security's only line because
//     that line happens to be the only candidate asserts a currency nobody
//     stated, and values the holding at an FX rate nobody stated.
//   - No currency stated names the security's sole line, and none where it has
//     none or several. Nothing has said which line, and picking one is the same
//     guess by a longer route.
//
// The family rather than the code, so a posting stating GBP names a line stored
// in GBX: the two are one currency under a different unit prefix, and the
// uniqueness index makes at most one of them possible.
//
// Empty means no line, which is an answer rather than a failure: a holding on no
// line reports unpriced. See
// docs/adr/0072-a-posting-names-a-security-and-a-line.md and
// docs/adr/0075-a-name-that-could-not-be-placed-names-no-line.md.
func LineFor(listings []*Listing, code string) string {
	if code == "" {
		if len(listings) != 1 {
			return ""
		}
		return listings[0].ID
	}
	for _, l := range listings {
		if currency.Same(l.Currency, code) {
			return l.ID
		}
	}
	return ""
}
