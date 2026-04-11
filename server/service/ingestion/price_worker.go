package ingestion

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/service/identification"
	"google.golang.org/protobuf/proto"
)

// resolveEntry caches the result of instrument resolution for a given identifier.
type resolveEntry struct {
	instID string
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

	var hintsValidAt *time.Time
	if req.GetExportedAt() != nil {
		t := req.GetExportedAt().AsTime()
		hintsValidAt = &t
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
			instID, resolveErr := resolveOrIdentifyInstrument(ctx, database, pluginRegistry, row.GetIdentifierType(), row.GetIdentifierDomain(), row.GetIdentifierValue(), acStr, hintsValidAt)
			entry = &resolveEntry{instID: instID, err: resolveErr}
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

		p := db.EODPrice{
			InstrumentID: entry.instID,
			PriceDate:    priceDate,
			Close:        row.GetClose(),
			DataProvider: "import",
		}
		if row.Open != nil {
			p.Open = row.Open
		}
		if row.High != nil {
			p.High = row.High
		}
		if row.Low != nil {
			p.Low = row.Low
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

// coverageKey builds a lookup key for ImportCoverage entries.
func coverageKey(idType, domain, value string) string {
	return idType + "\x00" + domain + "\x00" + value
}

// upsertWithCoverage upserts prices, using UpsertPricesWithFill for instruments
// that have coverage ranges and plain UpsertPrices for the rest.
func upsertWithCoverage(ctx context.Context, database db.DB, prices []db.EODPrice, coverage []*apiv1.ImportCoverage, resolveCache map[string]*resolveEntry) error {
	if len(coverage) == 0 {
		return database.UpsertPrices(ctx, prices)
	}

	// Build map: instrument ID -> []coverage ranges.
	type dateRange struct{ from, to time.Time }
	instCoverage := make(map[string][]dateRange)
	for _, c := range coverage {
		from, err := time.Parse("2006-01-02", c.GetFrom())
		if err != nil {
			continue
		}
		to, err := time.Parse("2006-01-02", c.GetTo())
		if err != nil {
			continue
		}
		key := coverageKey(c.GetIdentifierType(), c.GetIdentifierDomain(), c.GetIdentifierValue())
		entry, ok := resolveCache[key]
		if !ok || entry.err != nil || entry.instID == "" {
			continue
		}
		instCoverage[entry.instID] = append(instCoverage[entry.instID], dateRange{from, to})
	}

	// Group prices by instrument ID.
	byInst := make(map[string][]db.EODPrice)
	for _, p := range prices {
		byInst[p.InstrumentID] = append(byInst[p.InstrumentID], p)
	}

	// Upsert each instrument: with fill if coverage exists, plain otherwise.
	var uncovered []db.EODPrice
	for instID, instPrices := range byInst {
		ranges, hasCoverage := instCoverage[instID]
		if !hasCoverage {
			uncovered = append(uncovered, instPrices...)
			continue
		}
		covered := make(map[int]bool)
		for _, r := range ranges {
			// Filter prices within this range.
			var inRange []db.EODPrice
			for i, p := range instPrices {
				if !p.PriceDate.Before(r.from) && p.PriceDate.Before(r.to) {
					inRange = append(inRange, p)
					covered[i] = true
				}
			}
			provider := "import"
			if len(inRange) > 0 {
				provider = inRange[0].DataProvider
			}
			if err := database.UpsertPricesWithFill(ctx, instID, provider, inRange, r.from, r.to); err != nil {
				return err
			}
		}
		// Prices outside all coverage ranges get plain upsert.
		for i, p := range instPrices {
			if !covered[i] {
				uncovered = append(uncovered, p)
			}
		}
	}

	// Upsert any prices without coverage.
	if len(uncovered) > 0 {
		return database.UpsertPrices(ctx, uncovered)
	}
	return nil
}

// resolveOrIdentifyInstrument finds an instrument by identifier, or creates one.
func resolveOrIdentifyInstrument(ctx context.Context, database db.DB, pluginRegistry *identifier.Registry, idType, domain, value, assetClass string, hintsValidAt *time.Time) (string, error) {
	hint := identifier.Identifier{Type: idType, Domain: domain, Value: value}

	if assetClass != "" && pluginRegistry != nil {
		fallback := func(ctx context.Context, database db.DB) (string, error) {
			return ensureWithSuppliedIdentifier(ctx, database, idType, domain, value)
		}
		hints := identifier.Hints{SecurityTypeHint: assetClass}
		result, err := identification.ResolveWithPlugins(ctx, database, pluginRegistry,
			"", "", "", hints,
			[]identifier.Identifier{hint},
			false, fallback, nil, nil, 0, hintsValidAt)
		if err != nil {
			return "", fmt.Errorf("identification error for %s %q: %v", idType, value, err)
		}
		return result.InstrumentID, nil
	}

	ids, err := identification.ResolveByHintsDBOnly(ctx, database, []identifier.Identifier{hint})
	if err != nil {
		return "", fmt.Errorf("lookup error for %s %q: %v", idType, value, err)
	}
	if len(ids) > 1 {
		return "", fmt.Errorf("ambiguous: multiple instruments match %s %q", idType, value)
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	return ensureWithSuppliedIdentifier(ctx, database, idType, domain, value)
}

func ensureWithSuppliedIdentifier(ctx context.Context, database db.DB, idType, domain, value string) (string, error) {
	slog.Debug("creating instrument from price import with supplied identifier only",
		"identifier_type", idType, "identifier_domain", domain, "identifier_value", value)
	return database.EnsureInstrument(ctx, "", "", "", "", "", "",
		[]db.IdentifierInput{{Type: idType, Domain: domain, Value: value, Canonical: true}},
		"", nil, nil, nil)
}
