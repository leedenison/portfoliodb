package ingestion

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/derivative"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/service/identification"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
)

// resolveEntry caches the result of instrument resolution for a given identifier.
type resolveEntry struct {
	result identification.ResolveResult
	err    error
}

// processPriceImport loads a persisted ImportPricesRequest, resolves
// instruments, and upserts prices. Progress is tracked via
// SetJobTotalCount / IncrJobProcessedCount.
//
// Returns true when at least one price row was successfully persisted. The
// caller uses this to decide whether to nudge the price fetcher worker --
// mirrors the processTx and processCorporateEventImport success signal so a
// job that rejected every row does not produce churn.
func processPriceImport(ctx context.Context, database db.DB, pluginRegistry *identifier.Registry, j *JobRequest) bool {
	payload, err := loadAndClearPayload(ctx, database, j.JobID)
	if err != nil {
		log.Printf("price import job %s: load payload: %v", j.JobID, err)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false
	}
	var req apiv1.ImportPricesRequest
	if err := proto.Unmarshal(payload, &req); err != nil {
		log.Printf("price import job %s: unmarshal payload: %v", j.JobID, err)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false
	}

	var pricesAsOf *time.Time
	if req.GetExportedAt() != nil {
		t := req.GetExportedAt().AsTime()
		pricesAsOf = &t
	} else {
		slog.Warn("price import missing exported_at; OCC symbols will not be split-adjusted", "job_id", j.JobID)
	}

	rows := req.GetPrices()
	_ = database.SetJobTotalCount(ctx, j.JobID, int32(len(rows)))

	var prices []db.EODPrice
	var valErrs []*apiv1.ValidationError

	// Dedup cache: avoid calling plugins N times for the same identifier.
	resolveCache := make(map[string]*resolveEntry)

	for i, row := range rows {
		idType := row.GetIdentifierType()
		if !identifier.AllowedIdentifierTypes[idType] {
			valErrs = append(valErrs, &apiv1.ValidationError{
				RowIndex: int32(i),
				Field:    "identifier_type",
				Message:  fmt.Sprintf("unknown identifier_type %q", idType),
			})
			_ = database.IncrJobProcessedCount(ctx, j.JobID)
			continue
		}

		priceDate, err := time.Parse("2006-01-02", row.GetPriceDate())
		if err != nil {
			valErrs = append(valErrs, &apiv1.ValidationError{
				RowIndex: int32(i),
				Field:    "price_date",
				Message:  fmt.Sprintf("invalid price_date %q: %v", row.GetPriceDate(), err),
			})
			_ = database.IncrJobProcessedCount(ctx, j.JobID)
			continue
		}

		cacheKey := row.GetIdentifierType() + "\x00" + row.GetIdentifierDomain() + "\x00" + row.GetIdentifierValue()
		entry, cached := resolveCache[cacheKey]
		if !cached {
			acStr := db.AssetClassToStr(row.GetAssetClass())
			result, resolveErr := resolveOrIdentifyInstrument(ctx, database, pluginRegistry, row.GetIdentifierType(), row.GetIdentifierDomain(), row.GetIdentifierValue(), acStr, row.GetCurrency(), pricesAsOf)
			entry = &resolveEntry{result: result, err: resolveErr}
			resolveCache[cacheKey] = entry
		}
		if entry.err != nil {
			valErrs = append(valErrs, &apiv1.ValidationError{
				RowIndex: int32(i),
				Field:    "identifier",
				Message:  entry.err.Error(),
			})
			_ = database.IncrJobProcessedCount(ctx, j.JobID)
			continue
		}
		if len(entry.result.HintDiffs) > 0 {
			valErrs = append(valErrs, &apiv1.ValidationError{
				RowIndex: int32(i),
				Field:    "identifier",
				Message:  fmt.Sprintf("resolved instrument differs from import data: %s", hintDiffsSummary(entry.result.HintDiffs)),
			})
			_ = database.IncrJobProcessedCount(ctx, j.JobID)
			continue
		}

		dec, field, err := parsePriceDecimals(row)
		if err != nil {
			valErrs = append(valErrs, &apiv1.ValidationError{
				RowIndex: int32(i),
				Field:    field,
				Message:  err.Error(),
			})
			_ = database.IncrJobProcessedCount(ctx, j.JobID)
			continue
		}

		p := db.EODPrice{
			InstrumentID:  entry.result.InstrumentID,
			PriceDate:     priceDate,
			Close:         dec.Close,
			Open:          dec.Open,
			High:          dec.High,
			Low:           dec.Low,
			AdjustedClose: dec.AdjustedClose,
			DataProvider:  "import",
			LastFetchedAt: pricesAsOf,
		}
		// An undeclared basis means as-traded, which is what PortfolioDB's own
		// export emits. exported_at is knowledge time and does not imply the
		// file was back-adjusted.
		if b := row.GetShareCountBasis(); b != "" {
			basis, err := time.Parse("2006-01-02", b)
			if err != nil {
				valErrs = append(valErrs, &apiv1.ValidationError{
					RowIndex: int32(i),
					Field:    "share_count_basis",
					Message:  fmt.Sprintf("invalid date %q: want YYYY-MM-DD", b),
				})
				_ = database.IncrJobProcessedCount(ctx, j.JobID)
				continue
			}
			p.ShareCountBasis = &basis
		}
		if row.Volume != nil {
			p.Volume = row.Volume
		}
		prices = append(prices, p)
		_ = database.IncrJobProcessedCount(ctx, j.JobID)
	}

	if len(valErrs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, valErrs)
	}

	persisted := false
	if len(prices) > 0 {
		if err := upsertWithCoverage(ctx, database, prices, req.GetCoverage(), resolveCache); err != nil {
			log.Printf("price import job %s: upsert: %v", j.JobID, err)
			_ = database.AppendValidationErrors(ctx, j.JobID, []*apiv1.ValidationError{
				{RowIndex: -1, Field: "prices", Message: err.Error()},
			})
			_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
			return false
		}
		persisted = true
	}

	_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_SUCCESS)
	return persisted
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
// ImportPrices is unary, so the protovalidate patterns on ImportPriceRow reject a
// malformed value at the interceptor before this runs. Reaching the error here
// means the request came from somewhere that bypassed it, and it is reported per
// row rather than failing the job, like every other row-level problem.
func parsePriceDecimals(row *apiv1.ImportPriceRow) (priceDecimals, string, error) {
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

// coverageKey builds a lookup key for ImportCoverage entries.
func coverageKey(idType, domain, value string) string {
	return idType + "\x00" + domain + "\x00" + value
}

// upsertWithCoverage stores prices, recording each declared range as coverage so
// valuation carries prices forward across the non-trading days inside it. Rows
// falling outside every declared range cover only their own dates, which keeps
// the gaps between them gaps.
func upsertWithCoverage(ctx context.Context, database db.DB, prices []db.EODPrice, coverage []*apiv1.ImportCoverage, resolveCache map[string]*resolveEntry) error {
	if len(coverage) == 0 {
		return database.UpsertPrices(ctx, prices)
	}

	// Build map: instrument ID -> []coverage ranges.
	type dateRange struct{ from, before time.Time }
	instCoverage := make(map[string][]dateRange)
	for _, c := range coverage {
		from, err := time.Parse("2006-01-02", c.GetFrom())
		if err != nil {
			continue
		}
		before, err := time.Parse("2006-01-02", c.GetBefore())
		if err != nil {
			continue
		}
		key := coverageKey(c.GetIdentifierType(), c.GetIdentifierDomain(), c.GetIdentifierValue())
		entry, ok := resolveCache[key]
		if !ok || entry.err != nil || entry.result.InstrumentID == "" {
			continue
		}
		instCoverage[entry.result.InstrumentID] = append(instCoverage[entry.result.InstrumentID], dateRange{from, before})
	}

	// Group prices by instrument ID.
	byInst := make(map[string][]db.EODPrice)
	for _, p := range prices {
		byInst[p.InstrumentID] = append(byInst[p.InstrumentID], p)
	}

	uncovered := make([]db.EODPrice, 0, len(prices))
	for instID, ranges := range instCoverage {
		instPrices := byInst[instID]
		covered := make(map[int]bool)
		for _, r := range ranges {
			// Filter prices within this range.
			var inRange []db.EODPrice
			for i, p := range instPrices {
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
		// Prices outside all coverage ranges cover only their own dates.
		for i, p := range instPrices {
			if !covered[i] {
				uncovered = append(uncovered, p)
			}
		}
	}
	// Instruments the file declared no coverage for at all.
	for instID, instPrices := range byInst {
		if _, declared := instCoverage[instID]; !declared {
			uncovered = append(uncovered, instPrices...)
		}
	}

	if len(uncovered) > 0 {
		return database.UpsertPrices(ctx, uncovered)
	}
	return nil
}

// resolveOrIdentifyInstrument finds an instrument by identifier, or creates one.
// When the resolved instrument's metadata differs from the supplied hints, the
// returned ResolveResult.HintDiffs will be non-empty.
func resolveOrIdentifyInstrument(ctx context.Context, database db.DB, pluginRegistry *identifier.Registry, idType, domain, value, assetClass, currency string, hintsValidAt *time.Time) (identification.ResolveResult, error) {
	hint := identifier.Identifier{Type: idType, Domain: domain, Value: value}
	hints := identifier.Hints{SecurityTypeHint: assetClass, Currency: currency}

	if assetClass != "" && pluginRegistry != nil {
		fallback := func(ctx context.Context, database db.DB) (string, error) {
			return ensureWithSuppliedIdentifier(ctx, database, assetClass, currency, idType, domain, value, hintsValidAt)
		}
		result, err := identification.ResolveWithPlugins(ctx, database, pluginRegistry,
			"", "", "", hints,
			[]identifier.Identifier{hint},
			false, fallback, nil, nil, 0, hintsValidAt)
		if err != nil {
			return identification.ResolveResult{}, fmt.Errorf("identification error for %s %q: %v", idType, value, err)
		}
		return result, nil
	}

	resolved, err := identification.ResolveByHintsDBOnly(ctx, database, []identifier.Identifier{hint})
	if err != nil {
		return identification.ResolveResult{}, fmt.Errorf("lookup error for %s %q: %v", idType, value, err)
	}
	if len(resolved) > 1 {
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
		return identification.ResolveResult{InstrumentID: resolved[0].ID, Identified: true, HintDiffs: diffs}, nil
	}
	id, err := ensureWithSuppliedIdentifier(ctx, database, assetClass, currency, idType, domain, value, hintsValidAt)
	if err != nil {
		return identification.ResolveResult{}, err
	}
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
			if parsed.Strike > 0 && !parsed.Expiry.IsZero() && parsed.PutCall != "" {
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
