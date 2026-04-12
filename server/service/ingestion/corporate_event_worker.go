package ingestion

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/corporateevents"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/service/identification"
	"google.golang.org/protobuf/proto"
)

// processCorporateEventImport loads a persisted ImportCorporateEventsRequest,
// resolves instruments via the existing identifier flow (so unknown
// MIC_TICKER / OPENFIGI_TICKER / ISIN values are passed through identifier
// plugins), upserts splits and cash dividends, records coverage rows tagged
// "import", and runs the split adjustment recompute for every instrument
// that received at least one new split.
//
// Returns true when at least one split, dividend, or coverage row was
// successfully persisted. The caller uses this to decide whether to nudge
// the corporate event fetcher worker -- mirrors the processTx success
// signal so a job that rejected every row does not produce churn.
func processCorporateEventImport(ctx context.Context, database db.DB, pluginRegistry *identifier.Registry, j *JobRequest) bool {
	payload, err := loadAndClearPayload(ctx, database, j.JobID)
	if err != nil {
		log.Printf("corporate event import job %s: load payload: %v", j.JobID, err)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false
	}
	var req apiv1.ImportCorporateEventsRequest
	if err := proto.Unmarshal(payload, &req); err != nil {
		log.Printf("corporate event import job %s: unmarshal payload: %v", j.JobID, err)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false
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

		result, err := resolveCorporateEventRow(ctx, database, pluginRegistry, resolveCache, row)
		if err != nil {
			valErrs = append(valErrs, &apiv1.ValidationError{
				RowIndex: int32(i),
				Field:    "identifier",
				Message:  err.Error(),
			})
			_ = database.IncrJobProcessedCount(ctx, j.JobID)
			continue
		}
		if len(result.HintDiffs) > 0 {
			valErrs = append(valErrs, &apiv1.ValidationError{
				RowIndex: int32(i),
				Field:    "identifier",
				Message:  fmt.Sprintf("resolved instrument differs from import data: %s", hintDiffsSummary(result.HintDiffs)),
			})
			_ = database.IncrJobProcessedCount(ctx, j.JobID)
			continue
		}
		instID := result.InstrumentID

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

	persisted := false

	if len(splits) > 0 {
		if err := database.UpsertStockSplits(ctx, splits); err != nil {
			if len(valErrs) > 0 {
				_ = database.AppendValidationErrors(ctx, j.JobID, valErrs)
			}
			failJob(ctx, database, j.JobID, "splits", err)
			return false
		}
		persisted = true
	}
	if len(dividends) > 0 {
		if err := database.UpsertCashDividends(ctx, dividends); err != nil {
			if len(valErrs) > 0 {
				_ = database.AppendValidationErrors(ctx, j.JobID, valErrs)
			}
			failJob(ctx, database, j.JobID, "dividends", err)
			return false
		}
		persisted = true
	}

	// Coverage rows are recorded after the events are upserted so a partial
	// failure above does not advertise data we did not persist. Per-row
	// coverage validation errors (bad date, unresolvable identifier) are
	// accumulated alongside the per-event errors so the caller sees
	// everything via AppendValidationErrors. A hard DB error from the
	// coverage upsert still fails the job.
	covCount, covErrs, err := writeImportCoverage(ctx, database, req.GetCoverage(), resolveCache, pluginRegistry)
	if err != nil {
		if len(valErrs) > 0 || len(covErrs) > 0 {
			_ = database.AppendValidationErrors(ctx, j.JobID, append(valErrs, covErrs...))
		}
		failJob(ctx, database, j.JobID, "coverage", err)
		return false
	}
	if covCount > 0 {
		persisted = true
	}
	valErrs = append(valErrs, covErrs...)

	if len(valErrs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, valErrs)
	}

	for instID := range splitInstruments {
		if err := database.RecomputeSplitAdjustments(ctx, instID); err != nil {
			log.Printf("corporate event import job %s: recompute %s: %v", j.JobID, instID, err)
		}
		allSplits, err := database.ListStockSplits(ctx, instID)
		if err != nil {
			log.Printf("corporate event import job %s: list splits %s: %v", j.JobID, instID, err)
		} else {
			options := corporateevents.ProcessOptionSplits(ctx, database, instID, allSplits, ingestionLog, nil)
			for _, opt := range options {
				_ = database.RecomputeSplitAdjustments(ctx, opt.ID)
			}
		}
	}

	_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_SUCCESS)
	return persisted
}

// resolveCorporateEventRow resolves an instrument for one import row,
// caching the result so a CSV with thousands of dividends for the same
// ticker invokes the identifier plugins at most once. The asset class
// passed to the resolver is the row's declared asset class -- the same
// hint used by the price import path.
func resolveCorporateEventRow(ctx context.Context, database db.DB, pluginRegistry *identifier.Registry, cache map[string]*resolveEntry, row *apiv1.ImportCorporateEventRow) (identification.ResolveResult, error) {
	key := row.GetIdentifierType() + "\x00" + row.GetIdentifierDomain() + "\x00" + row.GetIdentifierValue()
	if entry, ok := cache[key]; ok {
		return entry.result, entry.err
	}
	acStr := db.AssetClassToStr(row.GetAssetClass())
	result, err := resolveOrIdentifyInstrument(ctx, database, pluginRegistry,
		row.GetIdentifierType(), row.GetIdentifierDomain(), row.GetIdentifierValue(), acStr, "", nil)
	cache[key] = &resolveEntry{result: result, err: err}
	return result, err
}

// buildSplit converts a proto SplitRow into a db.StockSplit. ex_date is
// required; split_from and split_to must parse as positive arbitrary-precision
// decimals (the underlying NUMERIC column accepts any decimal). Validation
// uses big.Rat so values like "0.000001" round-trip without precision loss.
func buildSplit(instID string, s *apiv1.SplitRow, rowIndex int) (db.StockSplit, *apiv1.ValidationError) {
	if s == nil {
		return db.StockSplit{}, &apiv1.ValidationError{RowIndex: int32(rowIndex), Field: "split", Message: "missing split payload"}
	}
	ex, err := time.Parse("2006-01-02", s.GetExDate())
	if err != nil {
		return db.StockSplit{}, &apiv1.ValidationError{RowIndex: int32(rowIndex), Field: "ex_date", Message: fmt.Sprintf("invalid ex_date %q", s.GetExDate())}
	}
	from, err := parseDecimal(s.GetSplitFrom())
	if err != nil || from.Sign() <= 0 {
		return db.StockSplit{}, &apiv1.ValidationError{RowIndex: int32(rowIndex), Field: "split_from", Message: fmt.Sprintf("split_from must be a positive decimal, got %q", s.GetSplitFrom())}
	}
	to, err := parseDecimal(s.GetSplitTo())
	if err != nil || to.Sign() <= 0 {
		return db.StockSplit{}, &apiv1.ValidationError{RowIndex: int32(rowIndex), Field: "split_to", Message: fmt.Sprintf("split_to must be a positive decimal, got %q", s.GetSplitTo())}
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
// ex_date, amount and currency are required; pay/record/declaration dates and
// frequency pass through when supplied. Amount is validated as an arbitrary-
// precision non-negative decimal via big.Rat.
func buildDividend(instID string, d *apiv1.CashDividendRow, rowIndex int) (db.CashDividend, *apiv1.ValidationError) {
	if d == nil {
		return db.CashDividend{}, &apiv1.ValidationError{RowIndex: int32(rowIndex), Field: "dividend", Message: "missing dividend payload"}
	}
	ex, err := time.Parse("2006-01-02", d.GetExDate())
	if err != nil {
		return db.CashDividend{}, &apiv1.ValidationError{RowIndex: int32(rowIndex), Field: "ex_date", Message: fmt.Sprintf("invalid ex_date %q", d.GetExDate())}
	}
	amount, err := parseDecimal(d.GetAmount())
	if err != nil || amount.Sign() < 0 {
		return db.CashDividend{}, &apiv1.ValidationError{RowIndex: int32(rowIndex), Field: "amount", Message: fmt.Sprintf("amount must be a non-negative decimal, got %q", d.GetAmount())}
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
// against the resolved instrument. Returns the number of coverage rows
// successfully written, the per-row validation errors collected (bad dates,
// unresolvable identifiers), and any hard DB error from the underlying
// upsert. Per-row errors do not abort the loop; only a hard error does.
//
// Coverage entries use RowIndex = -1 because the coverage list is separate
// from the events list, so a per-row index would not be meaningful to the
// caller; the Field name carries the location ("coverage.from" etc).
func writeImportCoverage(ctx context.Context, database db.DB, coverage []*apiv1.ImportCorporateEventCoverage, cache map[string]*resolveEntry, pluginRegistry *identifier.Registry) (int, []*apiv1.ValidationError, error) {
	var (
		written int
		errs    []*apiv1.ValidationError
	)
	for _, c := range coverage {
		from, err := time.Parse("2006-01-02", c.GetFrom())
		if err != nil {
			errs = append(errs, &apiv1.ValidationError{
				RowIndex: -1,
				Field:    "coverage.from",
				Message:  fmt.Sprintf("invalid from %q for %s %q", c.GetFrom(), c.GetIdentifierType(), c.GetIdentifierValue()),
			})
			continue
		}
		to, err := time.Parse("2006-01-02", c.GetTo())
		if err != nil {
			errs = append(errs, &apiv1.ValidationError{
				RowIndex: -1,
				Field:    "coverage.to",
				Message:  fmt.Sprintf("invalid to %q for %s %q", c.GetTo(), c.GetIdentifierType(), c.GetIdentifierValue()),
			})
			continue
		}
		key := c.GetIdentifierType() + "\x00" + c.GetIdentifierDomain() + "\x00" + c.GetIdentifierValue()
		entry, ok := cache[key]
		if !ok {
			// The coverage row references an identifier the events did not
			// touch; resolve it now (cached for sibling coverage rows). The
			// plugin registry is passed through so the resolution rules
			// match the per-event resolution above.
			result, rerr := resolveOrIdentifyInstrument(ctx, database, pluginRegistry,
				c.GetIdentifierType(), c.GetIdentifierDomain(), c.GetIdentifierValue(), "", "", nil)
			entry = &resolveEntry{result: result, err: rerr}
			cache[key] = entry
		}
		if entry.err != nil || entry.result.InstrumentID == "" {
			msg := fmt.Sprintf("could not resolve instrument for %s %q", c.GetIdentifierType(), c.GetIdentifierValue())
			if entry.err != nil {
				msg = entry.err.Error()
			}
			errs = append(errs, &apiv1.ValidationError{
				RowIndex: -1,
				Field:    "coverage.identifier",
				Message:  msg,
			})
			continue
		}
		if len(entry.result.HintDiffs) > 0 {
			errs = append(errs, &apiv1.ValidationError{
				RowIndex: -1,
				Field:    "coverage.identifier",
				Message:  fmt.Sprintf("resolved instrument differs from import data: %s", hintDiffsSummary(entry.result.HintDiffs)),
			})
			continue
		}
		if err := database.UpsertCorporateEventCoverage(ctx, entry.result.InstrumentID, db.CorporateEventProviderImport, from, to); err != nil {
			return written, errs, err
		}
		written++
	}
	return written, errs, nil
}

func failJob(ctx context.Context, database db.DB, jobID, field string, err error) {
	log.Printf("corporate event import job %s: %s: %v", jobID, field, err)
	_ = database.AppendValidationErrors(ctx, jobID, []*apiv1.ValidationError{
		{RowIndex: -1, Field: field, Message: err.Error()},
	})
	_ = database.SetJobStatus(ctx, jobID, apiv1.JobStatus_FAILED)
}

// parseDecimal parses an arbitrary-precision decimal string. The CSV import
// values for split ratios and dividend amounts go into PostgreSQL NUMERIC
// columns as text without ever being converted to float, so validation must
// not silently round-trip through float64 (which cannot represent values
// like 0.1 exactly). big.Rat.SetString accepts any decimal of arbitrary
// precision and rejects garbage.
func parseDecimal(s string) (*big.Rat, error) {
	if s == "" {
		return nil, fmt.Errorf("empty decimal")
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("invalid decimal %q", s)
	}
	return r, nil
}
