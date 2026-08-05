package testutil

import (
	"github.com/google/go-cmp/cmp"
	"github.com/shopspring/decimal"
)

// DecimalOpts compares decimals by value rather than by representation.
//
// decimal.Decimal is a big.Int and an exponent, so 1.0 and 1.00 are .Equal but
// not reflect.DeepEqual -- the default comparison fails on a difference in scale
// that carries no difference in value, which is exactly what reading a value back
// out of a NUMERIC(38, 12) column produces. Pass this to cmp.Diff wherever the
// compared value contains a decimal.
var DecimalOpts = cmp.Options{
	cmp.Comparer(decimal.Decimal.Equal),
	cmp.Comparer(func(a, b *decimal.Decimal) bool {
		if a == nil || b == nil {
			return a == b
		}
		return a.Equal(*b)
	}),
}
