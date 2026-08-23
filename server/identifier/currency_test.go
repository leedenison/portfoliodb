package identifier

import "testing"

func TestStrikeCurrency(t *testing.T) {
	cases := []struct {
		name   string
		stated string
		types  []string
		want   string
	}{
		{"a stated currency wins over the vocabulary", "EUR", []string{"OCC"}, "EUR"},
		{"an OCC symbol implies USD", "", []string{"ISIN", "OCC"}, "USD"},
		{"an OPRA symbol implies USD", "", []string{"OPRA"}, "USD"},
		{"a futures option symbol implies nothing", "", []string{"FUT_OPT"}, ""},
		{"nothing states or implies a currency", "", []string{"ISIN", "MIC_TICKER"}, ""},
		{"no identifiers at all", "", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := StrikeCurrency(c.stated, c.types); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
