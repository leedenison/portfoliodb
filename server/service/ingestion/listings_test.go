package ingestion

import (
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/proto"
)

func listing(id, code string) *db.Listing {
	l := &db.Listing{ID: id}
	if code != "" {
		l.Currency = proto.String(code)
	}
	return l
}

// The rungs, in order, and what each one refuses.
func TestResolveListings(t *testing.T) {
	cases := []struct {
		name     string
		tx       *apiv1.Tx
		already  string
		listings []*db.Listing
		want     string
	}{
		{
			name:     "the stated trading currency names the line",
			tx:       &apiv1.Tx{TradingCurrency: "USD"},
			listings: []*db.Listing{listing("gbp", "GBP"), listing("usd", "USD")},
			want:     "usd",
		},
		{
			// The family, not the code: a line stored in GBX is the line GBP names.
			name:     "a stated currency matches its family",
			tx:       &apiv1.Tx{TradingCurrency: "GBP"},
			listings: []*db.Listing{listing("gbx", "GBX")},
			want:     "gbx",
		},
		{
			// Nothing mints: a broker states a currency to say what its figures
			// are in, and a line the security does not have is not evidence it
			// trades in one.
			name:     "a stated currency the security has no line in falls through",
			tx:       &apiv1.Tx{TradingCurrency: "EUR"},
			listings: []*db.Listing{listing("gbp", "GBP"), listing("usd", "USD")},
			want:     "",
		},
		{
			name:     "the line identification named wins over the rungs below",
			tx:       &apiv1.Tx{},
			already:  "from-identification",
			listings: []*db.Listing{listing("usd", "USD")},
			want:     "from-identification",
		},
		{
			name:     "a security with one line needs nothing stated",
			tx:       &apiv1.Tx{},
			listings: []*db.Listing{listing("usd", "USD")},
			want:     "usd",
		},
		{
			// The case the whole change exists for: picking one would value the
			// holding at an FX rate nobody stated.
			name:     "a security with two lines and nothing naming one names none",
			tx:       &apiv1.Tx{},
			listings: []*db.Listing{listing("gbp", "GBP"), listing("usd", "USD")},
			want:     "",
		},
		{
			// The currency-unknown listing says how many lines the security has
			// is unknown. That is not a line a posting can be on.
			name:     "a security whose only line has no currency names none",
			tx:       &apiv1.Tx{},
			listings: []*db.Listing{listing("unknown", "")},
			want:     "",
		},
		{
			// It is the account's currency, so on a security quoted in two it
			// says nothing about which.
			name:     "the settlement currency is not a rung",
			tx:       &apiv1.Tx{SettlementCurrency: "USD"},
			listings: []*db.Listing{listing("gbp", "GBP"), listing("usd", "USD")},
			want:     "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resolved := []db.Resolution{{InstrumentID: "inst", ListingID: c.already}}
			insts := map[string]*db.InstrumentRow{"inst": {ID: "inst", Listings: c.listings}}
			resolveListings([]*apiv1.Tx{c.tx}, resolved, insts)
			if resolved[0].ListingID != c.want {
				t.Errorf("named line %q, want %q", resolved[0].ListingID, c.want)
			}
		})
	}
}

// A posting whose security never resolved has no listings to choose from, and
// must not be left holding a line from whatever was in the map.
func TestResolveListings_UnknownSecurityNamesNoLine(t *testing.T) {
	resolved := []db.Resolution{{InstrumentID: "missing"}}
	resolveListings([]*apiv1.Tx{{TradingCurrency: "USD"}}, resolved, map[string]*db.InstrumentRow{})
	if resolved[0].ListingID != "" {
		t.Errorf("named line %q, want none", resolved[0].ListingID)
	}
}
