package ingestion

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/archiveimport"
	"github.com/leedenison/portfoliodb/server/corporateevents"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// importCorporateEventPart applies one archive corporate event part, reporting
// through rep. Like the price part it returns whether anything was persisted,
// and an error only for a hard failure: an event the import could not read is a
// validation error on a part that still succeeded.
func importCorporateEventPart(ctx context.Context, database db.DB, pluginRegistry *identifier.Registry,
	part *archivev1.CorporateEventPart, eventsAsOf *time.Time, resolveCache map[string]*resolveEntry, rep *archiveimport.PartReporter) (bool, error) {
	groups := part.GetGroups()
	total := 0
	for _, g := range groups {
		total += len(g.GetEvents())
	}
	rep.Total(ctx, total)

	var splits []db.StockSplit
	var dividends []db.CashDividend
	splitInstruments := make(map[string]bool)

	for gi, g := range groups {
		instID, gErr := resolveEventGroupInstrument(ctx, database, pluginRegistry, resolveCache, g, eventsAsOf)
		if gErr != nil {
			// One unresolvable instrument fails its whole group: every event
			// under it names the same instrument, so none of them can land.
			gErr.RowIndex = int32(gi)
			rep.Err(gErr)
			rep.Advance(ctx, len(g.GetEvents()))
			continue
		}
		for ei, ev := range g.GetEvents() {
			switch e := ev.GetEvent().(type) {
			case *archivev1.CorporateEvent_Split:
				sp, vErr := buildSplit(instID, e.Split, gi, ei, eventsAsOf)
				if vErr != nil {
					rep.Err(vErr)
					break
				}
				splits = append(splits, sp)
				splitInstruments[instID] = true
			case *archivev1.CorporateEvent_Dividend:
				d, vErr := buildDividend(instID, e.Dividend, gi, ei, eventsAsOf)
				if vErr != nil {
					rep.Err(vErr)
					break
				}
				dividends = append(dividends, d)
			default:
				rep.Errf(gi, fmt.Sprintf("events[%d]", ei), "event is neither a split nor a dividend")
			}
			rep.Advance(ctx, 1)
		}
	}

	persisted := false
	if len(splits) > 0 {
		if err := database.UpsertStockSplits(ctx, splits); err != nil {
			rep.Errf(-1, "splits", err.Error())
			return false, fmt.Errorf("upsert splits: %w", err)
		}
		persisted = true
	}
	if len(dividends) > 0 {
		if err := database.UpsertCashDividends(ctx, dividends); err != nil {
			rep.Errf(-1, "dividends", err.Error())
			return false, fmt.Errorf("upsert dividends: %w", err)
		}
		persisted = true
	}

	// Coverage rows are recorded after the events are upserted so a partial
	// failure above does not advertise data we did not persist. Per-span
	// validation errors (a bad date, an empty interval) join the per-event ones.
	// A hard DB error from the coverage upsert still fails the part.
	covCount, covErrs, err := writeImportCoverage(ctx, database, groups, resolveCache, pluginRegistry, eventsAsOf)
	rep.Errs(covErrs)
	if err != nil {
		rep.Errf(-1, "coverage", err.Error())
		return false, fmt.Errorf("upsert coverage: %w", err)
	}
	if covCount > 0 {
		persisted = true
	}

	for instID := range splitInstruments {
		if err := database.RecomputeSplitAdjustments(ctx, instID); err != nil {
			log.Printf("corporate event import: recompute %s: %v", instID, err)
		}
	}
	// Adjust options whose identity predates an effective split, once for the
	// whole import rather than per instrument. The pass derives its own work
	// from the stored identity, so it also picks up anything an earlier run
	// failed to apply. ApplyOptionSplit recomputes each adjusted option's
	// split-adjusted values inside its own transaction.
	if len(splitInstruments) > 0 {
		corporateevents.ProcessPendingOptionSplits(ctx, database, "", ingestionLog)
	}
	return persisted, nil
}

// resolveEventGroupInstrument resolves the instrument one archive group names,
// caching the result so a second group naming the same instrument -- or a
// coverage span under it -- invokes the identifier plugins at most once. The
// asset class passed to the resolver is the group's declared hint, as is asOf,
// which split-adjusts OCC symbols to the file's declared knowledge time.
func resolveEventGroupInstrument(ctx context.Context, database db.DB, pluginRegistry *identifier.Registry, cache map[string]*resolveEntry, g *archivev1.CorporateEventGroup, asOf *time.Time) (string, *apiv1.ValidationError) {
	ref := g.GetInstrument()
	idType := typev1.IdentifierType_name[int32(ref.GetType())]
	if !identifier.AllowedIdentifierTypes[idType] {
		return "", &apiv1.ValidationError{Field: "instrument.type", Message: fmt.Sprintf("unknown identifier type %q", idType)}
	}
	key := idType + "\x00" + ref.GetDomain() + "\x00" + ref.GetValue()
	entry, ok := cache[key]
	if !ok {
		acStr := db.AssetClassToStr(g.GetAssetClass())
		result, err := resolveOrIdentifyInstrument(ctx, database, pluginRegistry,
			idType, ref.GetDomain(), ref.GetValue(), acStr, "", asOf)
		entry = &resolveEntry{result: result, err: err}
		cache[key] = entry
	}
	if entry.err != nil {
		return "", &apiv1.ValidationError{Field: "instrument", Message: entry.err.Error()}
	}
	if entry.result.InstrumentID == "" {
		return "", &apiv1.ValidationError{Field: "instrument", Message: fmt.Sprintf("could not resolve instrument for %s %q", idType, ref.GetValue())}
	}
	if len(entry.result.HintDiffs) > 0 {
		return "", &apiv1.ValidationError{
			Field:   "instrument",
			Message: fmt.Sprintf("resolved instrument differs from import data: %s", hintDiffsSummary(entry.result.HintDiffs)),
		}
	}
	return entry.result.InstrumentID, nil
}

// buildSplit converts an archive Split into a db.StockSplit. split_from and
// split_to must parse as positive arbitrary-precision decimals (the underlying
// NUMERIC column accepts any decimal). Validation uses big.Rat so values like
// "0.000001" round-trip without precision loss.
//
// Knowledge time comes from the event, else from the envelope's exported_at
// (asOf), else stays zero for the DB to stamp. Preserving it matters because
// it gates retroactive adjustment of options on the underlying.
func buildSplit(instID string, s *archivev1.Split, groupIndex, eventIndex int, asOf *time.Time) (db.StockSplit, *apiv1.ValidationError) {
	fail := func(field, msg string) *apiv1.ValidationError {
		return &apiv1.ValidationError{RowIndex: int32(groupIndex), Field: fmt.Sprintf("events[%d].%s", eventIndex, field), Message: msg}
	}
	if s == nil {
		return db.StockSplit{}, fail("split", "missing split payload")
	}
	ex, err := time.Parse("2006-01-02", s.GetExDate())
	if err != nil {
		return db.StockSplit{}, fail("ex_date", fmt.Sprintf("invalid ex_date %q", s.GetExDate()))
	}
	from, err := parseDecimal(s.GetSplitFrom())
	if err != nil || from.Sign() <= 0 {
		return db.StockSplit{}, fail("split_from", fmt.Sprintf("split_from must be a positive decimal, got %q", s.GetSplitFrom()))
	}
	to, err := parseDecimal(s.GetSplitTo())
	if err != nil || to.Sign() <= 0 {
		return db.StockSplit{}, fail("split_to", fmt.Sprintf("split_to must be a positive decimal, got %q", s.GetSplitTo()))
	}
	return db.StockSplit{
		InstrumentID: instID,
		ExDate:       ex,
		SplitFrom:    s.GetSplitFrom(),
		SplitTo:      s.GetSplitTo(),
		DataProvider: db.CorporateEventProviderImport,
		FirstKnownAt: knowledgeTime(s.GetFirstKnownAt(), asOf),
	}, nil
}

// knowledgeTime resolves an event's knowledge time: the event's own value, else
// the envelope's exported_at, else zero, which leaves the DB to stamp the
// storage time.
func knowledgeTime(eventTS *timestamppb.Timestamp, asOf *time.Time) time.Time {
	if eventTS != nil {
		return eventTS.AsTime()
	}
	if asOf != nil {
		return *asOf
	}
	return time.Time{}
}

// buildDividend converts an archive CashDividend into a db.CashDividend.
// ex_date, amount and currency are required; pay/record/declaration dates and
// frequency pass through when supplied. Amount is validated as an arbitrary-
// precision non-negative decimal via big.Rat. Knowledge time follows the same
// event / envelope / storage-time fallback as buildSplit.
func buildDividend(instID string, d *archivev1.CashDividend, groupIndex, eventIndex int, asOf *time.Time) (db.CashDividend, *apiv1.ValidationError) {
	fail := func(field, msg string) *apiv1.ValidationError {
		return &apiv1.ValidationError{RowIndex: int32(groupIndex), Field: fmt.Sprintf("events[%d].%s", eventIndex, field), Message: msg}
	}
	if d == nil {
		return db.CashDividend{}, fail("dividend", "missing dividend payload")
	}
	ex, err := time.Parse("2006-01-02", d.GetExDate())
	if err != nil {
		return db.CashDividend{}, fail("ex_date", fmt.Sprintf("invalid ex_date %q", d.GetExDate()))
	}
	amount, err := parseDecimal(d.GetAmount())
	if err != nil || amount.Sign() < 0 {
		return db.CashDividend{}, fail("amount", fmt.Sprintf("amount must be a non-negative decimal, got %q", d.GetAmount()))
	}
	if d.GetCurrency() == "" {
		return db.CashDividend{}, fail("currency", "currency required")
	}
	out := db.CashDividend{
		InstrumentID: instID,
		ExDate:       ex,
		Amount:       d.GetAmount(),
		Currency:     d.GetCurrency(),
		Frequency:    d.GetFrequency(),
		Type:         dividendTypeToString(d.GetType()),
		DataProvider: db.CorporateEventProviderImport,
		FirstKnownAt: knowledgeTime(d.GetFirstKnownAt(), asOf),
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

// dividendTypeToString maps the archive enum onto the stored two-letter
// vocabulary. Unspecified reads as CD, which is what the format says it means.
func dividendTypeToString(t archivev1.DividendType) string {
	if t == archivev1.DividendType_DIVIDEND_TYPE_UNSPECIFIED {
		return "CD"
	}
	return t.String()
}

// writeImportCoverage records each group's coverage spans tagged
// data_provider="import" against the resolved instrument, stamping the span
// with the file's declared knowledge time (asOf) so an imported span does not
// claim to have been confirmed at import time. Returns the number of coverage
// rows successfully written, the per-span validation errors collected, and any
// hard DB error from the underlying upsert. Per-span errors do not abort the
// loop; only a hard error does.
//
// A group whose instrument did not resolve is skipped in silence here: the
// event pass already reported it, and saying so twice would double-count one
// failure.
func writeImportCoverage(ctx context.Context, database db.DB, groups []*archivev1.CorporateEventGroup, cache map[string]*resolveEntry, pluginRegistry *identifier.Registry, asOf *time.Time) (int, []*apiv1.ValidationError, error) {
	var (
		written int
		errs    []*apiv1.ValidationError
	)
	for gi, g := range groups {
		if len(g.GetCoverage()) == 0 {
			continue
		}
		instID, gErr := resolveEventGroupInstrument(ctx, database, pluginRegistry, cache, g, asOf)
		if gErr != nil {
			continue
		}
		for ci, c := range g.GetCoverage() {
			fail := func(field, msg string) {
				errs = append(errs, &apiv1.ValidationError{
					RowIndex: int32(gi),
					Field:    fmt.Sprintf("coverage[%d].%s", ci, field),
					Message:  msg,
				})
			}
			from, err := time.Parse("2006-01-02", c.GetFrom())
			if err != nil {
				fail("from", fmt.Sprintf("invalid from %q", c.GetFrom()))
				continue
			}
			before, err := time.Parse("2006-01-02", c.GetBefore())
			if err != nil {
				fail("before", fmt.Sprintf("invalid before %q", c.GetBefore()))
				continue
			}
			// Caught here rather than left to the DB: an empty interval asserts
			// nothing, and letting it through would fail the whole import on a
			// single bad span.
			if !before.After(from) {
				fail("before", fmt.Sprintf("before %q must be after from %q", c.GetBefore(), c.GetFrom()))
				continue
			}
			if err := database.UpsertCorporateEventCoverage(ctx, instID, db.CorporateEventProviderImport, from, before, asOf); err != nil {
				return written, errs, err
			}
			written++
		}
	}
	return written, errs, nil
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
