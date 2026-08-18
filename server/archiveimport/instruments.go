package archiveimport

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
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
func InstrumentPart(ctx context.Context, database db.DB, part *archivev1.InstrumentPart, rep *PartReporter) (int32, error) {
	instruments := part.GetInstruments()
	rep.Total(ctx, len(instruments))
	if len(instruments) == 0 {
		return 0, nil
	}

	byRef := make(map[importInstKey]int, len(instruments))
	for i, inst := range instruments {
		for _, idf := range inst.GetIdentifiers() {
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
		id := ensureArchiveInstrument(ctx, database, inst, "", i, seenKeys, rep)
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
		found, err := database.FindInstrumentByIdentifier(ctx, typev1.IdentifierType_name[int32(ref.GetType())], ref.GetDomain(), ref.GetValue())
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

	// Pass 3: the derivatives, whose underlyings now exist.
	for i, inst := range instruments {
		if !isDerivative(inst.GetAssetClass()) {
			continue
		}
		underlyingID, ok := underlyingIDByIndex[i]
		if !ok {
			continue // already reported and counted in pass 2
		}
		id := ensureArchiveInstrument(ctx, database, inst, underlyingID, i, seenKeys, rep)
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

// importInstKey names an instrument the way a file names it, for matching an
// underlying reference against the identifiers stated elsewhere in the part.
type importInstKey struct{ typ, value, domain string }

// ensureArchiveInstrument ensures one archive instrument, restoring the values
// nothing recomputes: the option terms, the deliverable multiplier, the interval
// each name was correct over, and the provider identifiers the lookups
// produced. It returns "" when
// it reported a problem rather than storing anything.
func ensureArchiveInstrument(ctx context.Context, database db.DB, inst *archivev1.Instrument,
	underlyingID string, i int, seenKeys map[string]struct{}, rep *PartReporter) string {
	fail := func(msg string) string {
		rep.Errf(i, "instruments", msg)
		return ""
	}
	if len(inst.GetIdentifiers()) == 0 {
		return fail("at least one identifier required")
	}
	idns := make([]db.IdentifierInput, 0, len(inst.GetIdentifiers()))
	for _, idf := range inst.GetIdentifiers() {
		typeStr := typev1.IdentifierType_name[int32(idf.GetType())]
		// The interval is part of the key: one instrument may state a name it
		// has given up alongside the one it wears now, and those are two rows,
		// not a duplicate.
		key := typeStr + "\x00" + idf.GetValue() + "\x00" + idf.GetValidFrom() + "\x00" + idf.GetValidBefore()
		if _, ok := seenKeys[key]; ok {
			return fail("duplicate (type, value) in payload")
		}
		seenKeys[key] = struct{}{}
		idns = append(idns, db.IdentifierInput{
			Type: typeStr, Domain: idf.GetDomain(), Value: idf.GetValue(), Canonical: idf.GetCanonical(),
			ValidFrom: archiveDate(idf.ValidFrom), ValidBefore: archiveDate(idf.ValidBefore),
		})
	}
	opts, err := archiveOptionFields(inst)
	if err != nil {
		return fail(err.Error())
	}
	id, err := database.EnsureInstrument(ctx, db.AssetClassToStr(inst.GetAssetClass()), inst.GetExchangeMic(), inst.GetCurrency(),
		inst.GetName(), inst.GetCik(), inst.GetSicCode(), idns, underlyingID,
		archiveDate(inst.ValidFrom), archiveDate(inst.ValidBefore), opts)
	if err != nil {
		return fail(err.Error())
	}
	// EnsureInstrument matched rather than created whenever the instance already
	// knew the instrument, and a match sets the underlying and the option terms
	// and nothing else. A rebuild hits that on every currency and FX pair, which
	// migration 002 seeds. The merge fills the gaps the match left; it does not
	// overwrite, and on a row just created there is nothing to fill.
	if err := database.MergeInstrumentFromArchive(ctx, id, db.InstrumentMerge{
		AssetClass:  db.AssetClassToStr(inst.GetAssetClass()),
		ExchangeMIC: inst.GetExchangeMic(),
		Currency:    inst.GetCurrency(),
		CIK:         inst.GetCik(),
		SICCode:     inst.GetSicCode(),
		ValidFrom:   archiveDate(inst.ValidFrom),
		ValidBefore: archiveDate(inst.ValidBefore),
		Identifiers: idns,
	}); err != nil {
		return fail(err.Error())
	}
	if err := restoreProviderIdentifiers(ctx, database, id, inst.GetProviderIdentifiers()); err != nil {
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

// restoreProviderIdentifiers writes the recorded output of the identifier
// lookups straight onto the imported instrument. This is what the archive exists
// for: the fetchers address an instrument by the provider's own identifier, so
// an instrument restored with them is indistinguishable from a resolved one and
// no plugin is called for it.
//
// SaveProviderIdentifiers ignores conflicts, so importing over an instance that
// has already resolved the instrument adds nothing and loses nothing.
func restoreProviderIdentifiers(ctx context.Context, database db.DB, instrumentID string, pis []*archivev1.ProviderIdentifier) error {
	if len(pis) == 0 {
		return nil
	}
	in := make([]db.ProviderIdentifierInput, 0, len(pis))
	for _, pi := range pis {
		in = append(in, db.ProviderIdentifierInput{
			Provider: pi.GetProvider(),
			Type:     pi.GetIdentifierType(),
			Domain:   pi.GetDomain(),
			Value:    pi.GetValue(),
		})
	}
	return database.SaveProviderIdentifiers(ctx, instrumentID, in)
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
func refKey(t typev1.IdentifierType, value, domain string) importInstKey {
	return importInstKey{typ: typev1.IdentifierType_name[int32(t)], value: value, domain: domain}
}

func isDerivative(ac typev1.AssetClass) bool {
	return ac == typev1.AssetClass_OPTION || ac == typev1.AssetClass_FUTURE
}
