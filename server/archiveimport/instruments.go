package archiveimport

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/assetclass"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// InstrumentPart ensures the instruments of an archive's instrument part
// exist, finding or creating each by its identifiers, and reports through rep.
//
// Three passes: non-derivatives, then each derivative's underlying reference,
// then the derivatives themselves. A reference names an instrument by identifier
// rather than by position, so the passes index the part up front and the order
// the file was written in does not matter.
//
// It returns how many instruments were ensured. A problem with one instrument
// is a validation error rather than a hard failure: an instrument the file
// describes badly must not cost the rest of the part.
func InstrumentPart(ctx context.Context, database db.DB, part *archivev1.InstrumentPart, rep *PartReporter, runID string) (int32, error) {
	instruments := part.GetInstruments()
	rep.Total(ctx, len(instruments))
	if len(instruments) == 0 {
		return 0, nil
	}

	// Both grains: a derivative names its underlying by whichever identifier the
	// export picked, and for an equity known only by its ticker that is a name
	// hanging off one of its lines.
	byRef := make(map[identifier.Identifier]int, len(instruments))
	for i, inst := range instruments {
		for _, idf := range inst.GetIdentifiers() {
			byRef[refKey(idf.GetType(), idf.GetValue(), idf.GetDomain())] = i
		}
		for _, l := range inst.GetListings() {
			for _, idf := range l.GetIdentifiers() {
				byRef[refKey(idf.GetType(), idf.GetValue(), idf.GetDomain())] = i
			}
		}
		for _, idf := range inst.GetUnplacedIdentifiers() {
			byRef[refKey(idf.GetType(), idf.GetValue(), idf.GetDomain())] = i
		}
	}

	seenKeys := make(map[string]struct{})
	ensuredIDs := make([]string, len(instruments))
	var ensuredCount int32

	// Pass 1: non-derivatives, which every derivative's underlying is one of.
	for i, inst := range instruments {
		if isDerivative(inst.GetAssetClass()) {
			continue
		}
		id := ensureArchiveInstrument(ctx, database, inst, "", i, seenKeys, rep, runID)
		rep.Advance(ctx, 1)
		if id == "" {
			continue
		}
		ensuredIDs[i] = id
		ensuredCount++
	}

	// Pass 2: resolve each derivative's underlying to an instrument id. The
	// archive says the underlying appears in the same part, so that is where the
	// reference is looked up first; falling back to the database lets a partial
	// file import against an instance that already knows the underlying.
	underlyingIDByIndex := make(map[int]string, len(instruments))
	for i, inst := range instruments {
		if !isDerivative(inst.GetAssetClass()) {
			continue
		}
		ref := inst.GetUnderlying()
		if ref == nil {
			rep.Errf(i, "underlying", "OPTION/FUTURE requires an underlying reference")
			rep.Advance(ctx, 1)
			continue
		}
		key := refKey(ref.GetType(), ref.GetValue(), ref.GetDomain())
		if j, ok := byRef[key]; ok && ensuredIDs[j] != "" {
			underlyingIDByIndex[i] = ensuredIDs[j]
			continue
		}
		found, err := database.FindInstrumentByIdentifier(ctx, "", typev1.IdentifierType_name[int32(ref.GetType())], ref.GetDomain(), ref.GetValue())
		if err != nil {
			rep.Errf(i, "underlying", err.Error())
			rep.Advance(ctx, 1)
			continue
		}
		if found == "" {
			rep.Errf(i, "underlying", "underlying "+ref.GetValue()+" is in neither this archive nor this instance")
			rep.Advance(ctx, 1)
			continue
		}
		underlyingIDByIndex[i] = found
	}

	// Pass 2b: narrow each underlying security to the line the derivative
	// delivers -- the one its strike is quoted in, which the file names outright
	// as the ref's currency.
	//
	// Ensure rather than find. A contract asserts its own strike currency, and a
	// strike is quoted against shares, so it asserts that the underlying has a
	// line there -- and a system archive is the one source allowed to state
	// instrument identity, so nothing outranks it here. A ref that states no
	// currency names no line and is reported rather than guessed at. See
	// docs/adr/0074-an-options-underlying-is-the-line-its-strike-is-quoted-in.md.
	underlyingLineByIndex := make(map[int]string, len(underlyingIDByIndex))
	for i, inst := range instruments {
		underlyingID, ok := underlyingIDByIndex[i]
		if !ok {
			continue // not a derivative, or already reported in pass 2
		}
		cur := inst.GetUnderlying().GetCurrency()
		if cur == "" {
			rep.Errf(i, "underlying", "the underlying reference states no currency, "+
				"so the line of "+inst.GetUnderlying().GetValue()+" this contract delivers is not named")
			rep.Advance(ctx, 1)
			continue
		}
		line, err := database.EnsureListing(ctx, underlyingID, cur)
		if err != nil {
			rep.Errf(i, "underlying", err.Error())
			rep.Advance(ctx, 1)
			continue
		}
		underlyingLineByIndex[i] = line
	}

	// Pass 3: the derivatives, whose underlyings now exist.
	for i, inst := range instruments {
		if !isDerivative(inst.GetAssetClass()) {
			continue
		}
		underlyingLine, ok := underlyingLineByIndex[i]
		if !ok {
			continue // already reported and counted in pass 2
		}
		id := ensureArchiveInstrument(ctx, database, inst, underlyingLine, i, seenKeys, rep, runID)
		rep.Advance(ctx, 1)
		if id == "" {
			continue
		}
		ensuredIDs[i] = id
		ensuredCount++
	}
	// No fetcher triggers here. Imported instruments have no holdings yet, so
	// there are no price gaps to fill. Corporate events (splits, dividends)
	// are picked up by the daily corporate event fetch cycle.
	return ensuredCount, nil
}

// ensureArchiveInstrument ensures one archive instrument, restoring the values
// nothing recomputes: the option terms, the deliverable multiplier, the interval
// each name was correct over, and the provider identifiers the lookups
// produced. It returns "" when
// it reported a problem rather than storing anything.
func ensureArchiveInstrument(ctx context.Context, database db.DB, inst *archivev1.Instrument,
	underlyingListingID string, i int, seenKeys map[string]struct{}, rep *PartReporter, runID string) string {
	fail := func(msg string) string {
		rep.Errf(i, "instruments", msg)
		return ""
	}
	idns, err := archiveIdentifiers(inst.GetIdentifiers(), seenKeys)
	if err != nil {
		return fail(err.Error())
	}
	set := db.ListingSet{
		Listings:         make([]db.ListingMerge, 0, len(inst.GetListings())),
		UnplacedProvider: archiveProviderIdentifiers(inst.GetUnplacedProviderIdentifiers()),
	}
	claimed := make([]db.IdentifierInput, 0, len(idns))
	claimed = append(claimed, idns...)
	for _, l := range inst.GetListings() {
		lidns, err := archiveIdentifiers(l.GetIdentifiers(), seenKeys)
		if err != nil {
			return fail(err.Error())
		}
		claimed = append(claimed, lidns...)
		set.Listings = append(set.Listings, db.ListingMerge{
			Currency:            l.GetCurrency(),
			ValidFrom:           archiveDate(l.ValidFrom),
			ValidBefore:         archiveDate(l.ValidBefore),
			Identifiers:         lidns,
			ProviderIdentifiers: archiveProviderIdentifiers(l.GetProviderIdentifiers()),
		})
	}
	set.Unplaced, err = archiveIdentifiers(inst.GetUnplacedIdentifiers(), seenKeys)
	if err != nil {
		return fail(err.Error())
	}
	claimed = append(claimed, set.Unplaced...)
	if len(claimed) == 0 {
		return fail("at least one identifier required, on the security, on one of its listings, or on none of them")
	}
	opts, err := archiveOptionFields(inst)
	if err != nil {
		return fail(err.Error())
	}
	// The instrument block names its identifiers together, so it is one claim
	// rather than a set assembled from several answers. An archive carrying
	// instrument data is admin-only and authoritative at every level, which is
	// what makes that claim admissible (adr/0063).
	//
	// No currency is passed: set carries every line the file names, each with its
	// own, and there is nothing above them for one to be the currency of.
	id, listingID, err := database.EnsureArchiveInstrument(ctx, db.AssetClassToStr(inst.GetAssetClass()),
		inst.GetName(), inst.GetCik(), inst.GetSicCode(), idns, set,
		[]db.IdentityClaim{archiveClaim(claimed)}, underlyingListingID, opts, runID)
	if err != nil {
		return fail(err.Error())
	}
	// EnsureArchiveInstrument matched rather than created whenever the instance
	// already knew the instrument, and a match fills the underlying and the
	// option terms where the row has none, and touches nothing else. A rebuild
	// hits that on every currency and FX pair, which migration 002 seeds. The
	// merge fills the gaps the match left; it does not overwrite, and on a row
	// just created there is nothing to fill.
	//
	// The lines are not merged again here: placement already ensured every one of
	// them and filed its names, which is the same fill-the-gaps rule at the grain
	// below.
	if err := database.MergeInstrumentFromArchive(ctx, id, db.InstrumentMerge{
		AssetClass:  db.AssetClassToStr(inst.GetAssetClass()),
		CIK:         inst.GetCik(),
		SICCode:     inst.GetSicCode(),
		Identifiers: idns,
	}); err != nil {
		return fail(err.Error())
	}
	// The security-grain provider identifiers. Each line's travel with the line,
	// which is where every provider type that exists today belongs.
	if err := restoreProviderIdentifiers(ctx, database, id, listingID, inst.GetProviderIdentifiers()); err != nil {
		return fail("provider_identifiers: " + err.Error())
	}
	if inst.ContractMultiplier != nil {
		m, err := decimal.NewFromString(inst.GetContractMultiplier())
		if err != nil {
			return fail("contract_multiplier: " + err.Error())
		}
		if err := database.SetContractMultiplier(ctx, id, m); err != nil {
			return fail("contract_multiplier: " + err.Error())
		}
	}
	return id
}

// archiveIdentifiers converts one grain's identifiers, refusing a set that names
// the same thing twice.
//
// The interval is part of the key: one instrument may state a name it has given
// up alongside the one it wears now, and those are two rows rather than a
// duplicate. seenKeys spans the whole part, so a duplicate across two of a
// security's lines is caught as one within a single line is.
func archiveIdentifiers(in []*archivev1.Identifier, seenKeys map[string]struct{}) ([]db.IdentifierInput, error) {
	out := make([]db.IdentifierInput, 0, len(in))
	for _, idf := range in {
		typeStr := typev1.IdentifierType_name[int32(idf.GetType())]
		key := typeStr + "\x00" + idf.GetValue() + "\x00" + idf.GetValidFrom() + "\x00" + idf.GetValidBefore()
		if _, ok := seenKeys[key]; ok {
			return nil, errors.New("duplicate (type, value) in payload")
		}
		seenKeys[key] = struct{}{}
		out = append(out, db.IdentifierInput{
			Ref:         db.InstrumentRef{Type: typeStr, Value: idf.GetValue(), Domain: idf.GetDomain()},
			Canonical:   idf.GetCanonical(),
			ValidFrom:   archiveDate(idf.ValidFrom),
			ValidBefore: archiveDate(idf.ValidBefore),
		})
	}
	return out, nil
}

// archiveProviderIdentifiers converts one grain's provider identifiers.
func archiveProviderIdentifiers(in []*archivev1.ProviderIdentifier) []db.ProviderIdentifierInput {
	out := make([]db.ProviderIdentifierInput, 0, len(in))
	for _, pi := range in {
		out = append(out, db.ProviderIdentifierInput{
			Provider: pi.GetProvider(),
			Type:     pi.GetIdentifierType(),
			Domain:   pi.GetDomain(),
			Value:    pi.GetValue(),
		})
	}
	return out
}

// restoreProviderIdentifiers writes the recorded output of the identifier
// lookups straight onto the imported instrument. This is what the archive exists
// for: the fetchers address an instrument by the provider's own identifier, so
// an instrument restored with them is indistinguishable from a resolved one and
// no plugin is called for it.
//
// SaveProviderIdentifiers ignores conflicts, so importing over an instance that
// has already resolved the instrument adds nothing and loses nothing.
func restoreProviderIdentifiers(ctx context.Context, database db.DB, instrumentID, listingID string, pis []*archivev1.ProviderIdentifier) error {
	if len(pis) == 0 {
		return nil
	}
	return database.SaveProviderIdentifiers(ctx, instrumentID, listingID, archiveProviderIdentifiers(pis))
}

// archiveOptionFields reads the denormalized OCC components off an archive
// instrument. They travel together or not at all: a strike with no expiry
// describes no real contract, so a part-stated set is an error rather than a
// silent half-restore.
func archiveOptionFields(inst *archivev1.Instrument) (*db.OptionFields, error) {
	n := 0
	for _, set := range []bool{inst.Strike != nil, inst.Expiry != nil, inst.PutCall != nil} {
		if set {
			n++
		}
	}
	if n == 0 {
		return nil, nil
	}
	if n < 3 {
		return nil, errors.New("strike, expiry and put_call are stated together or not at all")
	}
	strike, err := decimal.NewFromString(inst.GetStrike())
	if err != nil {
		return nil, errors.New("strike: " + err.Error())
	}
	expiry, err := time.Parse("2006-01-02", inst.GetExpiry())
	if err != nil {
		return nil, errors.New("expiry: " + err.Error())
	}
	return &db.OptionFields{Strike: strike, Expiry: expiry, PutCall: inst.GetPutCall()}, nil
}

// archiveDate parses an optional "YYYY-MM-DD" archive date. An unparseable
// value is treated as absent; protovalidate has already refused the document if
// it did not match the pattern.
func archiveDate(s *string) *time.Time {
	if s == nil {
		return nil
	}
	t, err := time.Parse("2006-01-02", *s)
	if err != nil {
		return nil
	}
	return &t
}

// refKey names an instrument the way a file names it, for matching an
// underlying reference against the identifiers stated elsewhere in the part.
func refKey(t typev1.IdentifierType, value, domain string) identifier.Identifier {
	return identifier.Identifier{Type: typev1.IdentifierType_name[int32(t)], Domain: domain, Value: value}
}

// isDerivative is db.IsDerivative over the proto enum, which is what an archive
// speaks. The rule is asked of the tree directly rather than routed through the
// stored spelling and back.
func isDerivative(ac typev1.AssetClass) bool {
	return assetclass.Below(ac, typev1.AssetClass_DERIVATIVE)
}

// archiveClaim is the archive's instrument block read as one identity claim.
// Every identifier is returned rather than filtered: the file states them, it
// does not corroborate them by constraining a provider.
//
// No owner. Only a system archive carries instruments and importing one is
// admin-only, which is the system authority an admin archive is meant to have --
// the same argument that has its identifier rows written system-owned. A user
// archive reaches identity through its postings and the ingestion path instead,
// where the statement is the uploader's own. See adr/0063.
func archiveClaim(idns []db.IdentifierInput) db.IdentityClaim {
	c := db.IdentityClaim{Identifiers: make([]db.ClaimedIdentifier, 0, len(idns))}
	for _, i := range idns {
		c.Identifiers = append(c.Identifiers, db.ClaimedIdentifier{
			Ref:  i.Ref,
			Role: db.ClaimRoleReturned,
		})
	}
	return c
}
