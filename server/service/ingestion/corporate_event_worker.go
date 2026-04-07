package ingestion

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"google.golang.org/protobuf/proto"
)

// processCorporateEventImport loads a persisted ImportCorporateEventsRequest,
// resolves instruments via the existing identifier flow (so unknown
// MIC_TICKER / OPENFIGI_TICKER / ISIN values are passed through identifier
// plugins), upserts splits and cash dividends, records coverage rows tagged
// "import", and runs the split adjustment recompute for every instrument
// that received at least one new split. Mirrors processPriceImport.
func processCorporateEventImport(ctx context.Context, database db.DB, pluginRegistry *identifier.Registry, j *JobRequest) {
	payload, err := loadAndClearPayload(ctx, database, j.JobID)
	if err != nil {
		log.Printf("corporate event import job %s: load payload: %v", j.JobID, err)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return
	}
	var req apiv1.ImportCorporateEventsRequest
	if err := proto.Unmarshal(payload, &req); err != nil {
		log.Printf("corporate event import job %s: unmarshal payload: %v", j.JobID, err)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return
	}

	rows := req.GetEvents()
	_ = database.SetJobTotalCount(ctx, j.JobID, int32(len(rows)))

	resolveCache := make(map[string]*resolveEntry)
	var valErrs []*apiv1.ValidationError
	var splits []db.StockSplit
	var dividends []db.CashDividend
	splitInstruments := make(map[string]bool)

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

		instID, err := resolveCorporateEventRow(ctx, database, pluginRegistry, resolveCache, row)
		if err != nil {
			valErrs = append(valErrs, &apiv1.ValidationError{
				RowIndex: int32(i),
				Field:    "identifier",
				Message:  err.Error(),
			})
			_ = database.IncrJobProcessedCount(ctx, j.JobID)
			continue
		}

		switch ev := row.GetEvent().(type) {
		case *apiv1.ImportCorporateEventRow_Split:
			s, vErr := buildSplit(instID, ev.Split, i)
			if vErr != nil {
				valErrs = append(valErrs, vErr)
				_ = database.IncrJobProcessedCount(ctx, j.JobID)
				continue
			}
			splits = append(splits, s)
			splitInstruments[instID] = true
		case *apiv1.ImportCorporateEventRow_Dividend:
			d, vErr := buildDividend(instID, ev.Dividend, i)
			if vErr != nil {
				valErrs = append(valErrs, vErr)
				_ = database.IncrJobProcessedCount(ctx, j.JobID)
				continue
			}
			dividends = append(dividends, d)
		default:
			valErrs = append(valErrs, &apiv1.ValidationError{
				RowIndex: int32(i),
				Field:    "event",
				Message:  "row has neither split nor dividend",
			})
		}
		_ = database.IncrJobProcessedCount(ctx, j.JobID)
	}

	if len(valErrs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, valErrs)
	}

	if len(splits) > 0 {
		if err := database.UpsertStockSplits(ctx, splits); err != nil {
			failJob(ctx, database, j.JobID, "splits", err)
			return
		}
	}
	if len(dividends) > 0 {
		if err := database.UpsertCashDividends(ctx, dividends); err != nil {
			failJob(ctx, database, j.JobID, "dividends", err)
			return
		}
	}

	// Coverage rows are recorded after the events are upserted so a partial
	// failure above does not advertise data we did not persist.
	if err := writeImportCoverage(ctx, database, req.GetCoverage(), resolveCache); err != nil {
		failJob(ctx, database, j.JobID, "coverage", err)
		return
	}

	for instID := range splitInstruments {
		if err := database.RecomputeSplitAdjustments(ctx, instID); err != nil {
			log.Printf("corporate event import job %s: recompute %s: %v", j.JobID, instID, err)
		}
	}

	_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_SUCCESS)
}

// resolveCorporateEventRow resolves an instrument id for one import row,
// caching the result so a CSV with thousands of dividends for the same
// ticker invokes the identifier plugins at most once. The asset class
// passed to the resolver is the row's declared asset class -- the same
// hint used by the price import path.
func resolveCorporateEventRow(ctx context.Context, database db.DB, pluginRegistry *identifier.Registry, cache map[string]*resolveEntry, row *apiv1.ImportCorporateEventRow) (string, error) {
	key := row.GetIdentifierType() + "\x00" + row.GetIdentifierDomain() + "\x00" + row.GetIdentifierValue()
	if entry, ok := cache[key]; ok {
		return entry.instID, entry.err
	}
	acStr := db.AssetClassToStr(row.GetAssetClass())
	instID, err := resolveOrIdentifyInstrument(ctx, database, pluginRegistry,
		row.GetIdentifierType(), row.GetIdentifierDomain(), row.GetIdentifierValue(), acStr)
	cache[key] = &resolveEntry{instID: instID, err: err}
	return instID, err
}

// buildSplit converts a proto SplitRow into a db.StockSplit. ex_date is
// required; split_from and split_to must parse as positive numerics.
func buildSplit(instID string, s *apiv1.SplitRow, rowIndex int) (db.StockSplit, *apiv1.ValidationError) {
	if s == nil {
		return db.StockSplit{}, &apiv1.ValidationError{RowIndex: int32(rowIndex), Field: "split", Message: "missing split payload"}
	}
	ex, err := time.Parse("2006-01-02", s.GetExDate())
	if err != nil {
		return db.StockSplit{}, &apiv1.ValidationError{RowIndex: int32(rowIndex), Field: "ex_date", Message: fmt.Sprintf("invalid ex_date %q", s.GetExDate())}
	}
	if !isPositiveNumeric(s.GetSplitFrom()) || !isPositiveNumeric(s.GetSplitTo()) {
		return db.StockSplit{}, &apiv1.ValidationError{RowIndex: int32(rowIndex), Field: "split_from/split_to", Message: "split_from and split_to must be positive numerics"}
	}
	return db.StockSplit{
		InstrumentID: instID,
		ExDate:       ex,
		SplitFrom:    s.GetSplitFrom(),
		SplitTo:      s.GetSplitTo(),
		DataProvider: db.CorporateEventProviderImport,
	}, nil
}

// buildDividend converts a proto CashDividendRow into a db.CashDividend.
// ex_date and amount are required; pay/record/declaration dates and frequency
// pass through when supplied.
func buildDividend(instID string, d *apiv1.CashDividendRow, rowIndex int) (db.CashDividend, *apiv1.ValidationError) {
	if d == nil {
		return db.CashDividend{}, &apiv1.ValidationError{RowIndex: int32(rowIndex), Field: "dividend", Message: "missing dividend payload"}
	}
	ex, err := time.Parse("2006-01-02", d.GetExDate())
	if err != nil {
		return db.CashDividend{}, &apiv1.ValidationError{RowIndex: int32(rowIndex), Field: "ex_date", Message: fmt.Sprintf("invalid ex_date %q", d.GetExDate())}
	}
	if !isNonNegativeNumeric(d.GetAmount()) {
		return db.CashDividend{}, &apiv1.ValidationError{RowIndex: int32(rowIndex), Field: "amount", Message: "amount must be a non-negative numeric"}
	}
	if d.GetCurrency() == "" {
		return db.CashDividend{}, &apiv1.ValidationError{RowIndex: int32(rowIndex), Field: "currency", Message: "currency required"}
	}
	out := db.CashDividend{
		InstrumentID: instID,
		ExDate:       ex,
		Amount:       d.GetAmount(),
		Currency:     d.GetCurrency(),
		Frequency:    d.GetFrequency(),
		DataProvider: db.CorporateEventProviderImport,
	}
	if t, err := time.Parse("2006-01-02", d.GetPayDate()); err == nil {
		out.PayDate = &t
	}
	if t, err := time.Parse("2006-01-02", d.GetRecordDate()); err == nil {
		out.RecordDate = &t
	}
	if t, err := time.Parse("2006-01-02", d.GetDeclarationDate()); err == nil {
		out.DeclarationDate = &t
	}
	return out, nil
}

// writeImportCoverage records each coverage row tagged data_provider="import"
// against the resolved instrument. Coverage rows whose identifier failed to
// resolve are skipped silently -- the per-event validation errors already
// surface the failure.
func writeImportCoverage(ctx context.Context, database db.DB, coverage []*apiv1.ImportCorporateEventCoverage, cache map[string]*resolveEntry) error {
	for _, c := range coverage {
		from, err := time.Parse("2006-01-02", c.GetFrom())
		if err != nil {
			continue
		}
		to, err := time.Parse("2006-01-02", c.GetTo())
		if err != nil {
			continue
		}
		key := c.GetIdentifierType() + "\x00" + c.GetIdentifierDomain() + "\x00" + c.GetIdentifierValue()
		entry, ok := cache[key]
		if !ok {
			// The coverage row references an identifier the events did not
			// touch; resolve it now (still cached for any sibling coverage rows).
			instID, rerr := resolveOrIdentifyInstrument(ctx, database, nil,
				c.GetIdentifierType(), c.GetIdentifierDomain(), c.GetIdentifierValue(), "")
			entry = &resolveEntry{instID: instID, err: rerr}
			cache[key] = entry
		}
		if entry.err != nil || entry.instID == "" {
			continue
		}
		if err := database.UpsertCorporateEventCoverage(ctx, entry.instID, db.CorporateEventProviderImport, from, to); err != nil {
			return err
		}
	}
	return nil
}

func failJob(ctx context.Context, database db.DB, jobID, field string, err error) {
	log.Printf("corporate event import job %s: %s: %v", jobID, field, err)
	_ = database.AppendValidationErrors(ctx, jobID, []*apiv1.ValidationError{
		{RowIndex: -1, Field: field, Message: err.Error()},
	})
	_ = database.SetJobStatus(ctx, jobID, apiv1.JobStatus_FAILED)
}

func isPositiveNumeric(s string) bool {
	v, err := strconv.ParseFloat(s, 64)
	return err == nil && v > 0
}

func isNonNegativeNumeric(s string) bool {
	v, err := strconv.ParseFloat(s, 64)
	return err == nil && v >= 0
}
