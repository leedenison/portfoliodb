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

// processPriceImport loads a persisted ImportPricesRequest, resolves instruments,
// and upserts prices. Progress is tracked via SetJobTotalCount / IncrJobProcessedCount.
func processPriceImport(ctx context.Context, database db.DB, pluginRegistry *identifier.Registry, j *JobRequest) {
	payload, err := loadAndClearPayload(ctx, database, j.JobID)
	if err != nil {
		log.Printf("price import job %s: load payload: %v", j.JobID, err)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return
	}
	var req apiv1.ImportPricesRequest
	if err := proto.Unmarshal(payload, &req); err != nil {
		log.Printf("price import job %s: unmarshal payload: %v", j.JobID, err)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return
	}

	rows := req.GetPrices()
	_ = database.SetJobTotalCount(ctx, j.JobID, int32(len(rows)))

	var prices []db.EODPrice
	var valErrs []*apiv1.ValidationError

	// Dedup cache: avoid calling plugins N times for the same identifier.
	type resolveEntry struct {
		instID string
		err    error
	}
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
			instID, resolveErr := resolveOrIdentifyInstrument(ctx, database, pluginRegistry, row.GetIdentifierType(), row.GetIdentifierDomain(), row.GetIdentifierValue(), row.GetAssetClass())
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

	if len(prices) > 0 {
		if err := database.UpsertPrices(ctx, prices); err != nil {
			log.Printf("price import job %s: upsert: %v", j.JobID, err)
			_ = database.AppendValidationErrors(ctx, j.JobID, []*apiv1.ValidationError{
				{RowIndex: -1, Field: "prices", Message: err.Error()},
			})
			_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
			return
		}
	}

	_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_SUCCESS)
}

// resolveOrIdentifyInstrument finds an instrument by identifier, or creates one.
func resolveOrIdentifyInstrument(ctx context.Context, database db.DB, pluginRegistry *identifier.Registry, idType, domain, value, assetClass string) (string, error) {
	hint := identifier.Identifier{Type: idType, Domain: domain, Value: value}

	if assetClass != "" && pluginRegistry != nil {
		fallback := func(ctx context.Context, database db.DB) (string, error) {
			return ensureWithSuppliedIdentifier(ctx, database, idType, domain, value)
		}
		hints := identifier.Hints{SecurityTypeHint: assetClass}
		result, err := identification.ResolveWithPlugins(ctx, database, pluginRegistry,
			"", "", "", hints,
			[]identifier.Identifier{hint},
			false, fallback, nil, nil, 0)
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
		"", nil, nil)
}
