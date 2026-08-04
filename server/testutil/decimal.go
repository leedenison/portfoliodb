package testutil

import (
	"github.com/google/go-cmp/cmp"
	"github.com/shopspring/decimal"
	"go.uber.org/mock/gomock"
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

// DecEq matches a decimal argument by value rather than by representation, for
// gomock expectations.
//
// gomock's default matcher is reflect.DeepEqual, which sees a decimal's big.Int
// and exponent: 60 and 60.00 are .Equal but not DeepEqual, so an expectation
// written as a literal would depend on how the code under test happened to
// arrive at the value. Take the expectation as a decimal string so the test says
// what it means.
func DecEq(want string) gomock.Matcher { return decMatcher{decimal.RequireFromString(want)} }

type decMatcher struct{ want decimal.Decimal }

func (m decMatcher) Matches(x any) bool {
	got, ok := x.(decimal.Decimal)
	return ok && got.Equal(m.want)
}

func (m decMatcher) String() string { return "is numerically equal to " + m.want.String() }
