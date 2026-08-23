package ingestion

import (
	"strings"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/currency"
	"github.com/leedenison/portfoliodb/server/db"
)

// Naming the currency line a posting is on.
//
// A posting names its security always and the line within it when something said
// which. Where nothing did, it names no line, and the holding is reported unpriced
// rather than valued against a currency nobody stated. See
// docs/adr/0072-a-posting-names-a-security-and-a-line.md.
//
// Every rung reads what the security already has; none of them mints a line. A
// broker states a currency to say what its own figures are in, and a security is
// quoted in a currency whether or not anyone has traded it -- so a line comes into
// existence when a provider or a listing-grain identifier asserts one, which is
// identification's business, and never from a posting.

// resolveListings fills in the line each posting is on, in place, for the postings
// identification did not already settle.
//
// instByID is the batch's securities, already loaded for the asset-class check and
// the weight rule, and it carries each one's listings. So the whole pass reads what
// is in hand: no query, and no round trip per posting.
func resolveListings(txs []*apiv1.Tx, resolved []db.Resolution, instByID map[string]*db.InstrumentRow) {
	for i, tx := range txs {
		if resolved[i].ListingID != "" {
			continue
		}
		inst := instByID[resolved[i].InstrumentID]
		if inst == nil {
			continue
		}
		resolved[i].ListingID = listingFor(tx, inst)
	}
}

// listingFor is the rungs, in order: the currency the source stated the security
// trades in, then the security's sole line, then none.
//
// The first rung is quotedIn, so settlement_currency is not a rung and the reason
// it is not is stated once, where that choice is made.
func listingFor(tx *apiv1.Tx, inst *db.InstrumentRow) string {
	if code := quotedIn(tx); code != "" {
		if id := listingInFamily(inst, code); id != "" {
			return id
		}
	}
	return soleListing(inst)
}

// listingInFamily is the security's line in a currency's family, or none.
//
// The family rather than the code, so a posting stating GBP names a line stored in
// GBX: the two are one currency under a different unit prefix, and the uniqueness
// index makes at most one of them possible.
func listingInFamily(inst *db.InstrumentRow, code string) string {
	want := currency.Family(code)
	for _, l := range inst.Listings {
		if currency.Family(strings.ToUpper(l.Currency)) == want {
			return l.ID
		}
	}
	return ""
}

// soleListing is the security's only line, when it has exactly one.
//
// A security with several lines and nothing naming one is the case this whole
// change exists for: picking one would value the holding at an FX rate nobody
// stated. A security with no line at all has nothing to name either -- nobody has
// said what it is quoted in, and the posting says so by naming none.
func soleListing(inst *db.InstrumentRow) string {
	if len(inst.Listings) != 1 {
		return ""
	}
	return inst.Listings[0].ID
}
