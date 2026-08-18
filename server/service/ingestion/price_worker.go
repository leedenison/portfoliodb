package ingestion

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/archiveimport"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/derivative"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/service/identification"
	"github.com/shopspring/decimal"
)

// resolveEntry caches the result of instrument resolution for a given identifier.
type resolveEntry struct {
	result identification.ResolveResult
	err    error
}

// importPricePart applies one archive price part, reporting through rep.
//
// It reports whether anything was persisted, which is what decides whether the
// price fetcher is nudged: a part that rejected every row must not produce churn.
// The error return is a hard failure -- a database write that did not land --
// as against a row the import could not use, which is a validation error on a
// part that still succeeded.
//
// The resolve cache is passed in rather than made here so that a single archive
// carrying both prices and corporate events for one instrument identifies it
// once rather than once per part.
func importPricePart(ctx context.Context, database db.DB, pluginRegistry *identifier.Registry,
	part *archivev1.PricePart, pricesAsOf *time.Time, resolveCache map[string]*resolveEntry, keys *resolutionKeys, rep *archiveimport.PartReporter) (bool, error) {
	groups := part.GetGroups()
	total := 0
	for _, g := range groups {
		total += len(g.GetRows())
	}
	rep.Total(ctx, total)

	var resolved []resolvedGroup
	for i, g := range groups {
		instID, err := resolveGroupInstrument(ctx, database, pluginRegistry, resolveCache, keys, g, pricesAsOf)
		if err != nil {
			rep.Errf(i, "instrument", err.Error())
			rep.Advance(ctx, len(g.GetRows()))
			continue
		}

		bars, rowErrs := groupBars(g, i, instID, pricesAsOf)
		rep.Errs(rowErrs)
		rep.Advance(ctx, len(g.GetRows()))
		var covErrs []*apiv1.ValidationError
		resolved = append(resolved, resolvedGroup{
			instrumentID: instID,
			coverage:     groupCoverage(g, i, &covErrs),
			bars:         bars,
		})
		rep.Errs(covErrs)
	}

	if len(resolved) == 0 {
		return false, nil
	}
	if err := upsertGroups(ctx, database, resolved); err != nil {
		rep.Errf(-1, "prices", err.Error())
		return false, fmt.Errorf("upsert prices: %w", err)
	}
	for _, r := range resolved {
		if len(r.bars) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// newResolveCache makes the per-import identifier cache. Resolving an
// instrument can mean a paid plugin call, so two groups naming the same one
// must resolve it once -- and one archive's parts share a cache, because a
// price group and a corporate event group naming the same instrument is the
// ordinary case rather than the exception.
func newResolveCache() map[string]*resolveEntry {
	return make(map[string]*resolveEntry)
}

// dateRange is a half-open [from, before) span of dates.
type dateRange struct{ from, before time.Time }

// resolvedGroup is one archive group after its instrument has been resolved.
type resolvedGroup struct {
	instrumentID string
	coverage     []dateRange
	bars         []db.EODPrice
}

// resolveGroupInstrument maps a group's identifier to an instrument id, going to
// the identifier plugins at most once per identifier. The group's asset class
// and currency are the hints that route them, which is why a group carrying no
// rows is still worth writing: it is the only place they travel for an
// instrument that was covered and had nothing.
func resolveGroupInstrument(ctx context.Context, database db.DB, pluginRegistry *identifier.Registry,
	cache map[string]*resolveEntry, keys *resolutionKeys, g *archivev1.PriceGroup, asOf *time.Time) (string, error) {
	ref := g.GetInstrument()
	idType := ref.GetType().String()
	if !identifier.AllowedIdentifierTypes[idType] {
		return "", fmt.Errorf("unknown identifier_type %q", idType)
	}

	r := identifierRef{Type: idType, Domain: ref.GetDomain(), Value: ref.GetValue()}
	entry, cached := cache[r.cacheKey()]
	if !cached {
		acStr := db.AssetClassToStr(g.GetAssetClass())
		result, err := resolveOrIdentifyInstrument(ctx, database, pluginRegistry,
			idType, ref.GetDomain(), ref.GetValue(), acStr, g.GetCurrency(), asOf, keys, r.cacheKey())
		entry = &resolveEntry{result: result, err: err}
		cache[r.cacheKey()] = entry
	}
	if entry.err != nil {
		return "", entry.err
	}
	if len(entry.result.HintDiffs) > 0 {
		return "", fmt.Errorf("resolved instrument differs from import data: %s", hintDiffsSummary(entry.result.HintDiffs))
	}
	return entry.result.InstrumentID, nil
}

// groupBars converts one group's rows into storable bars, reporting a bad row
// against the group it came from and carrying on with the rest. Every row is
// accounted for either as a bar or as an error, so the caller advances the
// progress counter by the whole group and does not count rows itself.
func groupBars(g *archivev1.PriceGroup, groupIndex int, instrumentID string, fetchedAt *time.Time) ([]db.EODPrice, []*apiv1.ValidationError) {
	rows := g.GetRows()
	bars := make([]db.EODPrice, 0, len(rows))
	var errs []*apiv1.ValidationError

	fail := func(i int, field, msg string) {
		errs = append(errs, &apiv1.ValidationError{
			RowIndex: int32(groupIndex),
			Field:    fmt.Sprintf("rows[%d].%s", i, field),
			Message:  msg,
		})
	}

	for i, row := range rows {
		priceDate, err := time.Parse("2006-01-02", row.GetPriceDate())
		if err != nil {
			fail(i, "price_date", fmt.Sprintf("invalid price_date %q: %v", row.GetPriceDate(), err))
			continue
		}
		dec, field, err := parsePriceDecimals(row)
		if err != nil {
			fail(i, field, err.Error())
			continue
		}
		p := db.EODPrice{
			InstrumentID:  instrumentID,
			PriceDate:     priceDate,
			Close:         dec.Close,
			Open:          dec.Open,
			High:          dec.High,
			Low:           dec.Low,
			AdjustedClose: dec.AdjustedClose,
			DataProvider:  "import",
			LastFetchedAt: fetchedAt,
		}
		if row.Volume != nil {
			p.Volume = row.Volume
		}
		bars = append(bars, p)
	}
	return bars, errs
}

// groupCoverage parses a group's declared spans, dropping and reporting one it
// cannot read rather than failing the group: the bars are still worth storing,
// they just cover their own dates.
func groupCoverage(g *archivev1.PriceGroup, groupIndex int, errs *[]*apiv1.ValidationError) []dateRange {
	var out []dateRange
	for i, c := range g.GetCoverage() {
		from, err := time.Parse("2006-01-02", c.GetFrom())
		if err != nil {
			*errs = append(*errs, &apiv1.ValidationError{
				RowIndex: int32(groupIndex),
				Field:    fmt.Sprintf("coverage[%d].from", i),
				Message:  fmt.Sprintf("invalid date %q: want YYYY-MM-DD", c.GetFrom()),
			})
			continue
		}
		before, err := time.Parse("2006-01-02", c.GetBefore())
		if err != nil {
			*errs = append(*errs, &apiv1.ValidationError{
				RowIndex: int32(groupIndex),
				Field:    fmt.Sprintf("coverage[%d].before", i),
				Message:  fmt.Sprintf("invalid date %q: want YYYY-MM-DD", c.GetBefore()),
			})
			continue
		}
		out = append(out, dateRange{from, before})
	}
	return out
}

// priceDecimals is the decimal half of an import row.
type priceDecimals struct {
	Close                          decimal.Decimal
	Open, High, Low, AdjustedClose *decimal.Decimal
}

// parsePriceDecimals converts a row's decimal strings into exact values, and is
// the seam where an imported price stops being text. It returns the offending
// field name alongside the error so the caller can report it against the row.
//
// ImportPrices is unary, so the protovalidate patterns on PriceRow reject a
// malformed value at the interceptor before this runs. Reaching the error here
// means the request came from somewhere that bypassed it, and it is reported per
// row rather than failing the job, like every other row-level problem.
func parsePriceDecimals(row *archivev1.PriceRow) (priceDecimals, string, error) {
	var out priceDecimals
	c, err := decimal.NewFromString(row.GetClose())
	if err != nil {
		return out, "close", fmt.Errorf("invalid decimal %q", row.GetClose())
	}
	out.Close = c
	for _, f := range []struct {
		name string
		src  *string
		dst  **decimal.Decimal
	}{
		{"open", row.Open, &out.Open},
		{"high", row.High, &out.High},
		{"low", row.Low, &out.Low},
		{"adjusted_close", row.AdjustedClose, &out.AdjustedClose},
	} {
		// An unset optional field and an empty one both mean the provider did
		// not supply the value, which is what a nil column records.
		if f.src == nil || *f.src == "" {
			continue
		}
		d, err := decimal.NewFromString(*f.src)
		if err != nil {
			return out, f.name, fmt.Errorf("invalid decimal %q", *f.src)
		}
		*f.dst = &d
	}
	return out, "", nil
}

// upsertGroups stores each group's bars, recording its declared spans as
// coverage so valuation carries prices forward across the non-trading days
// inside them. Bars falling outside every declared span cover only their own
// dates, which keeps the gaps between them gaps.
//
// Groups are folded by instrument first. Coverage is stored per instrument with
// no basis dimension, and two groups naming one instrument would otherwise have
// their spans applied against partial bar sets.
func upsertGroups(ctx context.Context, database db.DB, groups []resolvedGroup) error {
	byInst := make(map[string]*resolvedGroup, len(groups))
	order := make([]string, 0, len(groups))
	for i := range groups {
		g := groups[i]
		cur, ok := byInst[g.instrumentID]
		if !ok {
			c := g
			byInst[g.instrumentID] = &c
			order = append(order, g.instrumentID)
			continue
		}
		cur.coverage = append(cur.coverage, g.coverage...)
		cur.bars = append(cur.bars, g.bars...)
	}

	for _, instID := range order {
		g := byInst[instID]
		if len(g.coverage) == 0 {
			if len(g.bars) > 0 {
				if err := database.UpsertPrices(ctx, g.bars); err != nil {
					return err
				}
			}
			continue
		}
		covered := make(map[int]bool, len(g.bars))
		for _, r := range g.coverage {
			var inRange []db.EODPrice
			for i, p := range g.bars {
				if !p.PriceDate.Before(r.from) && p.PriceDate.Before(r.before) {
					inRange = append(inRange, p)
					covered[i] = true
				}
			}
			var fetchedAt *time.Time
			if len(inRange) > 0 {
				fetchedAt = inRange[0].LastFetchedAt
			}
			// A declared range with no rows in it is not a no-op: it says the
			// caller asked about those dates and there was nothing to report.
			if err := database.UpsertPricesForRange(ctx, instID, db.PriceProviderImport, inRange, r.from, r.before, fetchedAt); err != nil {
				return err
			}
		}
		var uncovered []db.EODPrice
		for i, p := range g.bars {
			if !covered[i] {
				uncovered = append(uncovered, p)
			}
		}
		if len(uncovered) > 0 {
			if err := database.UpsertPrices(ctx, uncovered); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveOrIdentifyInstrument finds an instrument by identifier, or creates one.
// When the resolved instrument's metadata differs from the supplied hints, the
// returned ResolveResult.HintDiffs will be non-empty.
// keys and key stamp the resolution key this identifier was written as. A branch
// that returns an error leaves the key unstamped, because a resolution that could
// not run has no outcome to report -- as against one that ran and found nothing,
// which has several.
func resolveOrIdentifyInstrument(ctx context.Context, database db.DB, pluginRegistry *identifier.Registry, idType, domain, value, assetClass, currency string, hintsValidAt *time.Time, keys *resolutionKeys, key string) (identification.ResolveResult, error) {
	hint := identifier.Identifier{Type: idType, Domain: domain, Value: value}
	hints := identifier.Hints{SecurityTypeHint: assetClass, Currency: currency}

	if assetClass != "" && pluginRegistry != nil {
		fallback := func(ctx context.Context, database db.DB) (string, error) {
			return ensureWithSuppliedIdentifier(ctx, database, assetClass, currency, idType, domain, value, hintsValidAt)
		}
		result, err := identification.ResolveWithPlugins(ctx, database, pluginRegistry,
			"", "", "", hints,
			[]identifier.Identifier{hint},
			false, fallback, keys.attempt(key, db.TelemetryPurposePrimary), nil, 0, hintsValidAt)
		if err != nil {
			return identification.ResolveResult{}, fmt.Errorf("identification error for %s %q: %v", idType, value, err)
		}
		keys.end(ctx, key, resolutionOutcome(result), result.InstrumentID)
		return result, nil
	}

	resolved, err := identification.ResolveByHintsDBOnly(ctx, database, []identifier.Identifier{hint})
	if err != nil {
		return identification.ResolveResult{}, fmt.Errorf("lookup error for %s %q: %v", idType, value, err)
	}
	if len(resolved) > 1 {
		keys.end(ctx, key, db.TelemetryResolutionConflictingHints, "")
		return identification.ResolveResult{}, fmt.Errorf("ambiguous: multiple instruments match %s %q", idType, value)
	}
	if len(resolved) == 1 {
		inst := &identifier.Instrument{
			AssetClass: resolved[0].AssetClass,
			Exchange:   resolved[0].Exchange,
			Currency:   resolved[0].Currency,
		}
		normMIC := identification.NewDBMICNormalizer(database)
		diffs := identification.CompareHints(ctx, hints, []identifier.Identifier{hint}, inst, nil, normMIC)
		keys.end(ctx, key, db.TelemetryResolutionDBIdentifierHints, resolved[0].ID)
		return identification.ResolveResult{InstrumentID: resolved[0].ID, Identified: true, HintDiffs: diffs}, nil
	}
	id, err := ensureWithSuppliedIdentifier(ctx, database, assetClass, currency, idType, domain, value, hintsValidAt)
	if err != nil {
		return identification.ResolveResult{}, err
	}
	// No plugin resolved it and an instrument was ensured from what the row itself
	// carried, which is the same shape as the broker-description-only fallback on
	// the transaction path.
	keys.end(ctx, key, db.TelemetryResolutionBrokerDescriptionOnly, id)
	return identification.ResolveResult{InstrumentID: id}, nil
}

// ensureWithSuppliedIdentifier creates an instrument from a price import row
// whose identifier no plugin resolved. The row's identifier is stored exactly as
// supplied -- in particular an OCC symbol is NOT split-adjusted here, because the
// adjustment ResolveWithPlugins performs applies to its own hint list, not to the
// value this fallback closes over. The identity therefore reflects the market as
// of the request's declared exported_at, and identityAsOf records that. Leaving
// it NULL would tell the retroactive option-split pass that the identity predates
// every split, and it would re-apply splits already baked into the supplied OCC.
// A request with no exported_at is being taken at face value as current (the
// caller has already warned that OCC symbols will not be split-adjusted), so the
// vintage is now.
func ensureWithSuppliedIdentifier(ctx context.Context, database db.DB, assetClass, currency, idType, domain, value string, identityAsOf *time.Time) (string, error) {
	slog.Debug("creating instrument from price import with supplied identifier only",
		"identifier_type", idType, "identifier_domain", domain, "identifier_value", value,
		"asset_class", assetClass, "currency", currency)

	var underlyingID string
	var optFields *db.OptionFields
	if assetClass == db.AssetClassOption {
		parsed, ok := derivative.ParseOptionTicker(value)
		if ok && parsed.Symbol != "" {
			uHint := identifier.Identifier{Type: "MIC_TICKER", Value: parsed.Symbol}
			resolved, err := identification.ResolveByHintsDBOnly(ctx, database, []identifier.Identifier{uHint})
			if err == nil && len(resolved) == 1 {
				underlyingID = resolved[0].ID
			}
			if parsed.Strike.IsPositive() && !parsed.Expiry.IsZero() && parsed.PutCall != "" {
				optFields = &db.OptionFields{
					Strike:  parsed.Strike,
					Expiry:  parsed.Expiry,
					PutCall: parsed.PutCall,
				}
			}
		}
	}

	id, err := database.EnsureInstrument(ctx, assetClass, "", currency, "", "", "",
		[]db.IdentifierInput{{Type: idType, Domain: domain, Value: value, Canonical: true}},
		underlyingID, nil, nil, optFields)
	if err != nil {
		return "", err
	}
	if identityAsOf != nil {
		if err := database.SetIdentityAsOf(ctx, id, *identityAsOf); err != nil {
			return "", fmt.Errorf("set identity_as_of: %w", err)
		}
	} else if err := database.UpdateIdentityAsOf(ctx, id); err != nil {
		return "", fmt.Errorf("update identity_as_of: %w", err)
	}
	return id, nil
}
