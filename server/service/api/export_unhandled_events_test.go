package api

import (
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	dbpkg "github.com/leedenison/portfoliodb/server/db"
)

func exportUnhandled(value, eventType string, resolved bool, exDate *time.Time) dbpkg.ExportUnhandledCorporateEvent {
	return dbpkg.ExportUnhandledCorporateEvent{
		IdentifierType:   "MIC_TICKER",
		IdentifierValue:  value,
		IdentifierDomain: "XNAS",
		EventType:        eventType,
		ExDate:           exDate,
		Detail:           "1:10 reverse split affects 3 options",
		Resolved:         resolved,
		CreatedAt:        time.Date(2026, 4, 11, 8, 0, 0, 0, time.UTC),
	}
}

func exportUnhandledEvents(t *testing.T, rows []dbpkg.ExportUnhandledCorporateEvent) []*archivev1.UnhandledEventGroup {
	t.Helper()
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListUnhandledCorporateEventsForExport(gomock.Any()).Return(rows, nil)
	stream := &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportSystemArchive(&apiv1.ExportSystemArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_UNHANDLED_EVENTS},
	}, stream); err != nil {
		t.Fatalf("ExportSystemArchive: %v", err)
	}
	return stream.unhandledEventGroups()
}

// Both halves travel: the judgements already made, and the queue still waiting
// for one.
func TestExportSystemArchive_UnhandledEvents_CarriesResolvedAndUnresolved(t *testing.T) {
	exDate := time.Date(2025, 4, 11, 0, 0, 0, 0, time.UTC)
	groups := exportUnhandledEvents(t, []dbpkg.ExportUnhandledCorporateEvent{
		exportUnhandled("XYZ", "REVERSE_SPLIT", true, &exDate),
		exportUnhandled("XYZ", "NON_WHOLE_SPLIT", false, &exDate),
	})
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	events := groups[0].GetEvents()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if !events[0].GetResolved() || events[1].GetResolved() {
		t.Fatalf("resolved flags = %v, %v", events[0].GetResolved(), events[1].GetResolved())
	}
	if events[0].GetExDate() != "2025-04-11" {
		t.Fatalf("ex_date = %q", events[0].GetExDate())
	}
	if events[0].GetDetectedAt() == nil {
		t.Fatal("detected_at not carried")
	}
}

// An event with no ex_date is rare but real, and absent is not a zero date.
func TestExportSystemArchive_UnhandledEvents_OmitsAbsentExDate(t *testing.T) {
	groups := exportUnhandledEvents(t, []dbpkg.ExportUnhandledCorporateEvent{
		exportUnhandled("XYZ", "MERGER", false, nil),
	})
	if groups[0].GetEvents()[0].ExDate != nil {
		t.Fatalf("ex_date = %v, want absent", groups[0].GetEvents()[0].ExDate)
	}
}

// The detector's context is carried as the JSON text the column holds, so
// whatever numbers it names come back as they went in.
func TestExportSystemArchive_UnhandledEvents_CarriesDataVerbatim(t *testing.T) {
	row := exportUnhandled("XYZ", "REVERSE_SPLIT", false, nil)
	row.Data = []byte(`{"split_from":"10","split_to":"1"}`)
	groups := exportUnhandledEvents(t, []dbpkg.ExportUnhandledCorporateEvent{row})
	if got := groups[0].GetEvents()[0].GetDataJson(); got != `{"split_from":"10","split_to":"1"}` {
		t.Fatalf("data_json = %q", got)
	}
}

func TestExportSystemArchive_UnhandledEvents_GroupsByInstrument(t *testing.T) {
	groups := exportUnhandledEvents(t, []dbpkg.ExportUnhandledCorporateEvent{
		exportUnhandled("AAA", "MERGER", false, nil),
		exportUnhandled("BBB", "MERGER", false, nil),
	})
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
}
