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
		listingID, err := resolveGroupListing(ctx, database, pluginRegistry, resolveCache, keys, g, pricesAsOf)
		if err != nil {
			rep.Errf(i, "instrument", err.Error())
			rep.Advance(ctx, len(g.GetRows()))
			continue
		}

		bars, rowErrs := groupBars(g, i, listingID, pricesAsOf)
		rep.Errs(rowErrs)
		rep.Advance(ctx, len(g.GetRows()))
		var covErrs []*apiv1.ValidationError
		resolved = append(resolved, resolvedGroup{
			listingID: listingID,
			coverage:  groupCoverage(g, i, &covErrs),
			bars:      bars,
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

// resolvedGroup is one archive group after its listing has been resolved.
type resolvedGroup struct {
	listingID string
	coverage  []dateRange
	bars      []db.EODPrice
}

// resolveGroupListing maps a group's identifier and currency to the listing its
// bars belong to, going to the identifier plugins at most once per pair. The
// group's asset class and currency are the hints that route them, which is why a
// group carrying no rows is still worth writing: it is the only place they
// travel for a listing that was covered and had nothing.
//
// The currency is part of the ref and part of the memo key, not a hint: an
// identifier resolves to the security and the currency names which of its lines,
// so two groups sharing an identifier and differing in currency are two listings
// and must not share an answer.
// See docs/adr/0069-a-listing-is-named-by-a-security-identifier-and-a-currency.md.
//
// A group with no currency is refused. A price with no stated currency asserts
// nothing, and the line it would land on -- the security's unknown one -- is not
// priceable.
func resolveGroupListing(ctx context.Context, database db.DB, pluginRegistry *identifier.Registry,
	cache map[string]*resolveEntry, keys *resolutionKeys, g *archivev1.PriceGroup, asOf *time.Time) (string, error) {
	ref := g.GetInstrument()
	idType := ref.GetType().String()
	if !identifier.Known(idType) {
		return "", fmt.Errorf("unknown identifier_type %q", idType)
	}
	currency := ref.GetCurrency()
	if currency == "" {
		return "", fmt.Errorf("price group states no currency, so it names no listing")
	}

	r := identifier.Identifier{Type: idType, Domain: ref.GetDomain(), Value: ref.GetValue()}
	key := r.Key() + "|" + currency
	entry, cached := cache[key]
	if !cached {
		acStr := db.AssetClassToStr(g.GetAssetClass())
		result, err := resolveOrIdentifyInstrument(ctx, database, pluginRegistry,
			idType, ref.GetDomain(), ref.GetValue(), acStr, currency, asOf, keys, key)
		entry = &resolveEntry{result: result, err: err}
		cache[key] = entry
	}
	if entry.err != nil {
		return "", entry.err
	}
	if len(entry.result.HintDiffs) > 0 {
		return "", fmt.Errorf("resolved instrument differs from import data: %s", hintDiffsSummary(entry.result.HintDiffs))
	}
	// The resolution may have landed on a line already, but not every branch of
	// it names one, and the group's currency is the authority here in any case.
	listingID, err := database.EnsureListing(ctx, entry.result.InstrumentID, currency)
	if err != nil {
		return "", err
	}
	if listingID == "" {
		return "", fmt.Errorf("no %s listing for the resolved security", currency)
	}
	return listingID, nil
}

// groupBars converts one group's rows into storable bars, reporting a bad row
// against the group it came from and carrying on with the rest. Every row is
// accounted for either as a bar or as an error, so the caller advances the
// progress counter by the whole group and does not count rows itself.
func groupBars(g *archivev1.PriceGroup, groupIndex int, listingID string, fetchedAt *time.Time) ([]db.EODPrice, []*apiv1.ValidationError) {
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
			ListingID:     listingID,
			PriceDate:     priceDate,
			Close:         dec.Close,
			Open:          dec.Open,
			High:          dec.High,
			Low:           dec.Low,
			AdjustedClose: dec.AdjustedClose,
			DataProvider:  "import",
			LastFetchedAt: fetchedAt,
		}
		// An undeclared basis means as-traded, which is what PortfolioDB's own
		// export emits. exported_at is knowledge time and does not imply the
		// file was back-adjusted.
		if b := row.GetShareCountBasis(); b != "" {
			basis, err := time.Parse("2006-01-02", b)
			if err != nil {
				fail(i, "share_count_basis", fmt.Sprintf("invalid date %q: want YYYY-MM-DD", b))
				continue
			}
			p.ShareCountBasis = &basis
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
// Groups are folded by listing first. Coverage is stored per listing with no
// basis dimension, and two groups naming one listing would otherwise have their
// spans applied against partial bar sets.
func upsertGroups(ctx context.Context, database db.DB, groups []resolvedGroup) error {
	byListing := make(map[string]*resolvedGroup, len(groups))
	order := make([]string, 0, len(groups))
	for i := range groups {
		g := groups[i]
		cur, ok := byListing[g.listingID]
		if !ok {
			c := g
			byListing[g.listingID] = &c
			order = append(order, g.listingID)
			continue
		}
		cur.coverage = append(cur.coverage, g.coverage...)
		cur.bars = append(cur.bars, g.bars...)
	}

	for _, listingID := range order {
		g := byListing[listingID]
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
			if err := database.UpsertPricesForRange(ctx, listingID, db.PriceProviderImport, inRange, r.from, r.before, fetchedAt); err != nil {
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
		// A price or corporate-event archive names the instrument itself, so
		// everything here is stated and nothing is proposed.
		ident := identifier.Identity{Stated: []identifier.Identifier{hint}, Hints: hints}
		result, err := identification.ResolveWithPlugins(ctx, database, pluginRegistry,
			"", "", "", ident,
			false, fallback, keys.attempt(key, db.TelemetryPurposePrimary), nil, 0, hintsValidAt)
		if err != nil {
			return identification.ResolveResult{}, fmt.Errorf("identification error for %s %q: %v", idType, value, err)
		}
		keys.end(ctx, key, resolutionOutcome(result), result.InstrumentID)
		return result, nil
	}

	// One hint, so at most one instrument: ResolveByHintsDBOnly looks each name up
	// once and returns one entry per distinct instrument. There used to be a
	// branch here for several, reporting "ambiguous" and failing the row, and it
	// could not be reached.
	//
	// The ambiguity it was reaching for is real and lands elsewhere. One triple
	// can be held by two instruments over disjoint intervals, and the lookup
	// settles that by taking the name in force and falling back to the most
	// recently closed one -- which answers "who holds this now" rather than "who
	// held it on the row's date". Asking the second question is 0122.
	resolved, err := identification.ResolveByHintsDBOnly(ctx, database, []identifier.Identifier{hint})
	if err != nil {
		return identification.ResolveResult{}, fmt.Errorf("lookup error for %s %q: %v", idType, value, err)
	}
	if len(resolved) == 1 {
		diffs := identification.CompareDBMeta(hints, resolved[0])
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
// supplied: nothing rebases a hint onto another vintage, so an OCC symbol is
// stored under the name the request spelled. That name became correct at the
// request's declared exported_at, and the identifier's valid_from records that.
// Leaving it NULL would tell the retroactive option-split pass that the name
// predates every split, and it would restate a symbol already carrying them.
// A request with no exported_at is being taken at face value as current, so the
// vintage is now.
func ensureWithSuppliedIdentifier(ctx context.Context, database db.DB, assetClass, currency, idType, domain, value string, namedAt *time.Time) (string, error) {
	slog.Debug("creating instrument from price import with supplied identifier only",
		"identifier_type", idType, "identifier_domain", domain, "identifier_value", value,
		"asset_class", assetClass, "currency", currency)

	var underlyingListingID string
	var optFields *db.OptionFields
	if assetClass == db.AssetClassOption {
		parsed, ok := derivative.ParseOptionTicker(value)
		if ok && parsed.Symbol != "" {
			uHint := identifier.Identifier{Type: "MIC_TICKER", Value: parsed.Symbol}
			resolved, err := identification.ResolveByHintsDBOnly(ctx, database, []identifier.Identifier{uHint})
			if err == nil && len(resolved) == 1 {
				// The line the contract's strike is quoted in: the request's own
				// currency, else what its symbology implies. The request states
				// the contract outright, so it asserts that line of the
				// underlying and may mint it; stating no currency at all names no
				// line, and EnsureInstrument then refuses the option rather than
				// storing a strike denominated in nothing. See adr/0074.
				if cur := identifier.StrikeCurrency(currency, []string{idType}); cur != "" {
					if line, lErr := database.EnsureListing(ctx, resolved[0].ID, cur); lErr == nil {
						underlyingListingID = line
					}
				}
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

	validFrom := namedAt
	if validFrom == nil {
		now := time.Now()
		validFrom = &now
	}
	// No claim: a price import states one identifier, so there is no
	// association for it to have asserted.
	id, _, err := database.EnsureInstrument(ctx, assetClass, currency, "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: idType, Value: value, Domain: domain},
			Canonical: true,
			ValidFrom: db.VintageDate(validFrom),
		}},
		nil, underlyingListingID, optFields, "")
	if err != nil {
		return "", err
	}
	return id, nil
}
