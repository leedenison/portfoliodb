package ingestion

import (
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

func listing(id, code string) *db.Listing {
	return &db.Listing{ID: id, Currency: code}
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
			name:     "a stated currency the security has no line in names none",
			tx:       &apiv1.Tx{TradingCurrency: "EUR"},
			listings: []*db.Listing{listing("gbp", "GBP"), listing("usd", "USD")},
			want:     "",
		},
		{
			// The case above with one line rather than two, which is the one
			// that discriminates: a stated currency that matches nothing does
			// not fall through to the sole-line rung. Reaching it would place a
			// posting stating EUR on a USD line -- an FX rate nobody stated,
			// arrived at because there was only one candidate to guess at.
			name:     "a stated currency matching no line does not fall through to the sole line",
			tx:       &apiv1.Tx{TradingCurrency: "EUR"},
			listings: []*db.Listing{listing("usd", "USD")},
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
			// Nobody has said what it is quoted in, so there is no line to be on.
			name:     "a security with no line at all names none",
			tx:       &apiv1.Tx{},
			listings: nil,
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
