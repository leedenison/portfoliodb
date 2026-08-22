// Package currency declares the currencies PortfolioDB treats as minor-unit
// forms of another. GBX is pence sterling: the same currency as GBP under a
// different unit prefix, so one converts to the other by moving the decimal
// point rather than by an FX rate.
//
// The fact is declared once here because three things state it -- the listing
// uniqueness index, the derived FX pairs a price plugin has to synthesise, and
// the spelling OpenFIGI expects on the wire -- and three copies of one fact is
// the restatement adr/0066 collapsed once already. Each of those derives from
// this table rather than repeating it.
//
// See docs/adr/0068-a-listing-is-a-currency-of-a-security.md.
package currency

// MinorUnit is one currency that is another under a different unit prefix.
type MinorUnit struct {
	Code     string // ISO code of the minor unit, e.g. "GBX"
	Major    string // the unit it is a prefix of, e.g. "GBP"
	Exponent int32  // one minor unit is 10^Exponent major units
}

// MinorUnits holds exactly one entry and grows on evidence. GBX is the only
// minor-unit currency anything in this system handles: one derived FX pair, one
// OpenFIGI override, one entry in the SQL currency_family. Listing currencies
// here that nothing tests would assert more than we know.
var MinorUnits = []MinorUnit{
	{Code: "GBX", Major: "GBP", Exponent: -2},
}

// Family returns the code that governs listing uniqueness for code: the major
// unit for a minor-unit code, and code itself otherwise. A provider quoting the
// London line in pence and another quoting it in pounds name one listing, so
// without the family one line forks in two.
//
// It governs that index and nothing else, and it never rewrites a stored code
// -- not a CURRENCY identifier, not an FX_PAIR, not trading_currency, not a
// stored price. GBX and GBP are separately seeded CASH instruments and GBX/USD
// and GBP/USD separately seeded FX instruments, and normalising codes would
// collapse instruments that are deliberately distinct.
//
// Held in lockstep with the SQL currency_family in server/migrations by a test:
// an index expression must be IMMUTABLE, so the SQL cannot read this table.
func Family(code string) string {
	for _, m := range MinorUnits {
		if m.Code == code {
			return m.Major
		}
	}
	return code
}
