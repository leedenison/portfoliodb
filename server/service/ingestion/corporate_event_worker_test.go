package ingestion

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/archiveimport"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// eventPayload builds the persisted job payload for a corporate event import.
// exportedAt is nil only to exercise the worker's guard for a payload written
// before the envelope was required.
func eventPart(groups ...*archivev1.CorporateEventGroup) *archivev1.CorporateEventPart {
	return &archivev1.CorporateEventPart{Groups: groups}
}

// runEventPart applies a corporate event part with a detached reporter and
// returns what it persisted along with the problems it recorded.
func runEventPart(t *testing.T, database db.DB, registry *identifier.Registry,
	part *archivev1.CorporateEventPart, asOf *time.Time) (bool, []*apiv1.ValidationError, error) {
	t.Helper()
	rep := archiveimport.NewDetachedReporter()
	persisted, err := importCorporateEventPart(context.Background(), database, registry, part, asOf, newResolveCache(), rep)
	return persisted, rep.Errors(), err
}

// tickerGroup is one group naming an instrument by MIC ticker.
func tickerGroup(value string, ac typev1.AssetClass) *archivev1.CorporateEventGroup {
	return &archivev1.CorporateEventGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: value, Domain: "XNAS"},
		AssetClass: ac,
	}
}

func splitEvent(s *archivev1.Split) *archivev1.CorporateEvent {
	return &archivev1.CorporateEvent{Event: &archivev1.CorporateEvent_Split{Split: s}}
}

func dividendEvent(d *archivev1.CashDividend) *archivev1.CorporateEvent {
	return &archivev1.CorporateEvent{Event: &archivev1.CorporateEvent_Dividend{Dividend: d}}
}

// TestProcessCorporateEventImport_HappyPath verifies that a group holding a
// split and a dividend resolves its instrument once, upserts both event types,
// writes the coverage span, calls RecomputeSplitAdjustments because a split
// landed, and marks the job SUCCESS.
func TestProcessCorporateEventImport_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	// The split carries its own knowledge time; the dividend does not and so
	// falls back to the envelope's exported_at.
	splitKnownAt := time.Date(2015, 3, 4, 9, 30, 0, 0, time.UTC)
	exportedAt := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	g := tickerGroup("AAPL", typev1.AssetClass_STOCK)
	g.Coverage = []*archivev1.DateInterval{{From: "2014-01-01", Before: "2025-01-01"}}
	g.Events = []*archivev1.CorporateEvent{
		splitEvent(&archivev1.Split{
			ExDate: "2020-08-31", SplitFrom: "1", SplitTo: "4",
			FirstKnownAt: timestamppb.New(splitKnownAt),
		}),
		dividendEvent(&archivev1.CashDividend{
			ExDate:    "2024-02-09",
			PayDate:   proto.String("2024-02-15"),
			Amount:    "0.24",
			Currency:  "USD",
			Frequency: proto.String("quarterly"),
			Type:      archivev1.DividendType_SC,
		}),
	}
	part := eventPart(g)

	// Resolution: one cache miss for the group, and the coverage pass reuses
	// it rather than resolving the same instrument a second time.
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", "STOCK", "XNAS", "USD", nil)

	database.EXPECT().UpsertStockSplits(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, splits []db.StockSplit) error {
			if len(splits) != 1 {
				t.Errorf("expected 1 split, got %d", len(splits))
			}
			if splits[0].InstrumentID != "inst-aapl" {
				t.Errorf("instrument: %s", splits[0].InstrumentID)
			}
			if splits[0].SplitFrom != "1" || splits[0].SplitTo != "4" {
				t.Errorf("ratio: %+v", splits[0])
			}
			if splits[0].DataProvider != db.CorporateEventProviderImport {
				t.Errorf("provider: %s", splits[0].DataProvider)
			}
			// The event's own knowledge time wins over the envelope's.
			if !splits[0].FirstKnownAt.Equal(splitKnownAt) {
				t.Errorf("first known at: want %s, got %s", splitKnownAt, splits[0].FirstKnownAt)
			}
			return nil
		})
	database.EXPECT().UpsertCashDividends(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, divs []db.CashDividend) error {
			if len(divs) != 1 {
				t.Errorf("expected 1 dividend, got %d", len(divs))
			}
			if divs[0].Amount != "0.24" || divs[0].Currency != "USD" {
				t.Errorf("dividend: %+v", divs[0])
			}
			if divs[0].PayDate == nil || !divs[0].PayDate.Equal(time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC)) {
				t.Errorf("pay date: %+v", divs[0].PayDate)
			}
			if divs[0].Type != "SC" {
				t.Errorf("type: want SC, got %q", divs[0].Type)
			}
			// No event-level knowledge time, so the envelope's stands in.
			if !divs[0].FirstKnownAt.Equal(exportedAt) {
				t.Errorf("first known at: want %s, got %s", exportedAt, divs[0].FirstKnownAt)
			}
			return nil
		})
	// Imported coverage is stamped with the file's knowledge time rather than
	// claiming to have been confirmed at import time.
	database.EXPECT().UpsertCorporateEventCoverage(gomock.Any(), "inst-aapl", db.CorporateEventProviderImport,
		time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), &exportedAt).Return(nil)
	database.EXPECT().RecomputeSplitAdjustments(gomock.Any(), "inst-aapl").Return(nil)
	// The option pass runs once for the import, across all underlyings, and
	// derives its own work rather than being handed this instrument's splits.
	database.EXPECT().ListPendingOptionSplits(gomock.Any(), "").Return(nil, nil)

	persisted, _, err := runEventPart(t, database, registry, part, &exportedAt)
	if err != nil {
		t.Fatalf("importCorporateEventPart: %v", err)
	}
	if !persisted {
		t.Error("expected persisted=true after a successful upsert")
	}
}

// A group whose only span holds no events still reaches the coverage table.
// It is the only way a file can record that a provider was asked about those
// dates and had nothing.
func TestProcessCorporateEventImport_CoverageWithNoEvents(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	exportedAt := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	g := tickerGroup("AAPL", typev1.AssetClass_STOCK)
	g.Coverage = []*archivev1.DateInterval{{From: "2014-01-01", Before: "2025-01-01"}}
	part := eventPart(g)

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", "STOCK", "XNAS", "USD", nil)
	database.EXPECT().UpsertCorporateEventCoverage(gomock.Any(), "inst-aapl", db.CorporateEventProviderImport,
		time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), &exportedAt).Return(nil)

	persisted, _, err := runEventPart(t, database, registry, part, &exportedAt)
	if err != nil {
		t.Fatalf("importCorporateEventPart: %v", err)
	}
	if !persisted {
		t.Error("expected persisted=true after a coverage-only import")
	}
}

// TestProcessCorporateEventImport_RejectsBadSplitRatio verifies that an event
// with split_to = 0 is reported as a validation error naming its position in
// the group, and does not reach the upsert path.
func TestProcessCorporateEventImport_RejectsBadSplitRatio(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	g := tickerGroup("AAPL", typev1.AssetClass_STOCK)
	g.Events = []*archivev1.CorporateEvent{splitEvent(&archivev1.Split{ExDate: "2020-08-31", SplitFrom: "1", SplitTo: "0"})}
	part := eventPart(g)

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").Return("inst-aapl", "STOCK", "XNAS", "USD", nil)

	// No upserts and no recompute (no valid splits landed).

	persisted, capturedErrs, err := runEventPart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importCorporateEventPart: %v", err)
	}
	if persisted {
		t.Error("expected persisted=false when every event was rejected")
	}

	if len(capturedErrs) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(capturedErrs))
	}
	if capturedErrs[0].Field != "events[0].split_to" {
		t.Errorf("field: %s", capturedErrs[0].Field)
	}
	if capturedErrs[0].RowIndex != 0 {
		t.Errorf("row index: got %d, want the group index 0", capturedErrs[0].RowIndex)
	}
}

// TestProcessCorporateEventImport_DividendOnlyDoesNotRecompute verifies that a
// dividend-only import does NOT call RecomputeSplitAdjustments.
func TestProcessCorporateEventImport_DividendOnlyDoesNotRecompute(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	g := tickerGroup("MSFT", typev1.AssetClass_STOCK)
	g.Events = []*archivev1.CorporateEvent{dividendEvent(&archivev1.CashDividend{ExDate: "2024-02-15", Amount: "0.75", Currency: "USD"})}
	part := eventPart(g)

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "MSFT").Return("inst-msft", "STOCK", "XNAS", "USD", nil)
	database.EXPECT().UpsertCashDividends(gomock.Any(), gomock.Any()).Return(nil)
	// Critically: NO RecomputeSplitAdjustments call.

	persisted, _, err := runEventPart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importCorporateEventPart: %v", err)
	}
	if !persisted {
		t.Error("expected persisted=true after a successful dividend upsert")
	}
}

// TestProcessCorporateEventImport_RejectsBadCoverageDate verifies that a span
// with an invalid from-date is recorded as a validation error naming its
// position, and does not silently disappear.
func TestProcessCorporateEventImport_RejectsBadCoverageDate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	g := tickerGroup("AAPL", typev1.AssetClass_STOCK)
	g.Coverage = []*archivev1.DateInterval{{From: "2024-13-01", Before: "2025-01-01"}} // invalid month
	part := eventPart(g)

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", "STOCK", "XNAS", "USD", nil)

	persisted, capturedErrs, err := runEventPart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importCorporateEventPart: %v", err)
	}
	if persisted {
		t.Error("expected persisted=false when no events or coverage spans succeeded")
	}

	if len(capturedErrs) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(capturedErrs))
	}
	if capturedErrs[0].Field != "coverage[0].from" {
		t.Errorf("field: got %q, want coverage[0].from", capturedErrs[0].Field)
	}
}

// An empty coverage interval is reported per span rather than left to the DB,
// where the error would abort the whole import.
func TestProcessCorporateEventImport_EmptyCoverageInterval(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	g := tickerGroup("AAPL", typev1.AssetClass_STOCK)
	g.Coverage = []*archivev1.DateInterval{{From: "2024-01-01", Before: "2024-01-01"}}
	part := eventPart(g)

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", "STOCK", "XNAS", "USD", nil)

	persisted, capturedErrs, err := runEventPart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importCorporateEventPart: %v", err)
	}
	if persisted {
		t.Error("expected persisted=false when the only coverage span was rejected")
	}
	if len(capturedErrs) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(capturedErrs))
	}
	if capturedErrs[0].Field != "coverage[0].before" {
		t.Errorf("field: got %q, want coverage[0].before", capturedErrs[0].Field)
	}
}

// TestProcessCorporateEventImport_AcceptsHighPrecisionDecimal verifies that
// the parseDecimal helper accepts values that have no exact float64
// representation (e.g. "0.1") -- a ParseFloat-based validator would silently
// round-trip these.
func TestProcessCorporateEventImport_AcceptsHighPrecisionDecimal(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	g := tickerGroup("MSFT", typev1.AssetClass_STOCK)
	g.Events = []*archivev1.CorporateEvent{dividendEvent(&archivev1.CashDividend{ExDate: "2024-02-15", Amount: "0.1", Currency: "USD"})}
	part := eventPart(g)

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "MSFT").Return("inst-msft", "STOCK", "XNAS", "USD", nil)
	database.EXPECT().UpsertCashDividends(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, divs []db.CashDividend) error {
			if divs[0].Amount != "0.1" {
				t.Errorf("expected raw string 0.1 stored, got %q", divs[0].Amount)
			}
			return nil
		})

	persisted, _, err := runEventPart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importCorporateEventPart: %v", err)
	}
	if !persisted {
		t.Error("expected persisted=true")
	}
}

// TestProcessCorporateEventImport_RejectsInvalidDecimal verifies that a
// non-numeric amount is reported as a validation error.
func TestProcessCorporateEventImport_RejectsInvalidDecimal(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	g := tickerGroup("MSFT", typev1.AssetClass_STOCK)
	g.Events = []*archivev1.CorporateEvent{dividendEvent(&archivev1.CashDividend{ExDate: "2024-02-15", Amount: "abc", Currency: "USD"})}
	part := eventPart(g)

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "MSFT").Return("inst-msft", "STOCK", "XNAS", "USD", nil)

	persisted, capturedErrs, err := runEventPart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importCorporateEventPart: %v", err)
	}
	if persisted {
		t.Error("expected persisted=false")
	}
	if len(capturedErrs) != 1 || capturedErrs[0].Field != "events[0].amount" {
		t.Fatalf("expected one validation error on field=events[0].amount, got %+v", capturedErrs)
	}
}

// TestProcessCorporateEventImport_RejectsHintDiff verifies that when the
// resolved instrument's asset class differs from the group's declared asset
// class, the whole group is rejected: every event under it names that same
// instrument, so none of them can land.
func TestProcessCorporateEventImport_RejectsHintDiff(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	g := tickerGroup("AAPL", typev1.AssetClass_STOCK)
	g.Events = []*archivev1.CorporateEvent{splitEvent(&archivev1.Split{ExDate: "2020-08-31", SplitFrom: "1", SplitTo: "4"})}
	part := eventPart(g)

	// Instrument found but has asset class ETF, not STOCK.
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", "ETF", "XNAS", "USD", nil)

	persisted, capturedErrs, err := runEventPart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importCorporateEventPart: %v", err)
	}
	if persisted {
		t.Error("expected persisted=false when the group was rejected for a hint diff")
	}
	if len(capturedErrs) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(capturedErrs))
	}
	if capturedErrs[0].Field != "instrument" {
		t.Errorf("expected field=instrument, got %s", capturedErrs[0].Field)
	}
	if !strings.Contains(capturedErrs[0].Message, "SecurityType") {
		t.Errorf("expected message to mention SecurityType, got %s", capturedErrs[0].Message)
	}
}

// A file with no knowledge time anywhere leaves FirstKnownAt zero, which the
// db layer reads as "stamp it with the storage time".
func TestBuildEvent_KnowledgeTimeFallsBackToStorageTime(t *testing.T) {
	split, vErr := buildSplit("inst-aapl", &archivev1.Split{
		ExDate: "2020-08-31", SplitFrom: "1", SplitTo: "4",
	}, 0, 0, nil)
	if vErr != nil {
		t.Fatalf("buildSplit: %+v", vErr)
	}
	if !split.FirstKnownAt.IsZero() {
		t.Errorf("split knowledge time: want zero, got %s", split.FirstKnownAt)
	}

	div, vErr := buildDividend("inst-aapl", &archivev1.CashDividend{
		ExDate: "2024-02-09", Amount: "0.24", Currency: "USD",
	}, 0, 0, nil)
	if vErr != nil {
		t.Fatalf("buildDividend: %+v", vErr)
	}
	if !div.FirstKnownAt.IsZero() {
		t.Errorf("dividend knowledge time: want zero, got %s", div.FirstKnownAt)
	}
	// An unspecified dividend type is a regular cash dividend, which the format
	// states and which the column would have defaulted to anyway.
	if div.Type != "CD" {
		t.Errorf("type: want CD, got %q", div.Type)
	}
}
