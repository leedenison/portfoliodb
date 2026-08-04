package api

import "github.com/shopspring/decimal"

// Decimal values cross the wire as canonical decimal strings, because a double
// cannot carry an exact decimal and a NUMERIC column exposed as one is lossy in
// both directions. See adr/0027-decimal-values-cross-the-wire-as-strings.md.
//
// decimal.String() emits the shortest form that round-trips, with trailing zeros
// trimmed, so a value read back out of a NUMERIC(38, 12) column does not arrive
// on the wire padded to twelve places.

// decStr renders a decimal for the wire.
func decStr(d decimal.Decimal) string { return d.String() }

// decStrPtr renders an optional decimal for the wire, preserving absence.
func decStrPtr(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.String()
	return &s
}
