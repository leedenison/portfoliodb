package ingestion

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// systemArchivePayload is a document carrying one instrument, one price group
// and one corporate event group, which is enough to watch all three parts run.
func systemArchivePayload(t *testing.T) []byte {
	t.Helper()
	ref := &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS"}
	priced := &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS", Currency: "USD"}
	b, err := proto.Marshal(&archivev1.SystemArchive{
		Envelope: &archivev1.Envelope{
			FormatVersion: 1,
			ExportedAt:    timestamppb.Now(),
			Kind:          archivev1.ArchiveKind_SYSTEM,
		},
		Instruments: &archivev1.InstrumentPart{Instruments: []*archivev1.Instrument{{
			AssetClass: typev1.AssetClass_STOCK,
			// The ticker names a line, so it travels on the line the prices below
			// are quoted in.
			Listings: []*archivev1.Listing{{
				Currency:    "USD",
				Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS", Canonical: true}},
			}},
		}}},
		Prices: &archivev1.PricePart{Groups: []*archivev1.PriceGroup{{
			// A price group names a line, so its ref carries the currency; the
			// corporate event group below names the security and shares `ref`
			// exactly as it stands.
			Instrument: priced,
			Rows:       []*archivev1.PriceRow{{PriceDate: "2024-01-15", Close: "185.90"}},
		}}},
		CorporateEvents: &archivev1.CorporateEventPart{Groups: []*archivev1.CorporateEventGroup{{
			Instrument: ref,
		}}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// partRows is the job's own record of which parts it will apply, which is what
// the worker reads to decide what to run.
func partRows(parts ...archivev1.ArchivePart) []db.JobPartResult {
	rows := make([]db.JobPartResult, 0, len(parts))
	for _, p := range parts {
		rows = append(rows, db.JobPartResult{Part: p, Status: apiv1.JobStatus_PENDING})
	}
	return rows
}

// The parts run in restore order, each marked RUNNING then SUCCESS, and the job
// is SUCCESS once they are all done.
func TestProcessSystemImport_RunsPartsInRestoreOrder(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().MergeInstrumentFromArchive(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("inst-1", nil).AnyTimes()
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return("inst-1", "STOCK", []string{"USD"}, nil).AnyTimes()
	database.EXPECT().EnsureInstrument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("inst-1", "listing-id", nil).AnyTimes()
	database.EXPECT().EnsureArchiveInstrument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("inst-1", "listing-id", nil).AnyTimes()
	database.EXPECT().EnsureListing(gomock.Any(), gomock.Any(), gomock.Any()).Return("listing-id", nil).AnyTimes()
	database.EXPECT().EnsureListing(gomock.Any(), gomock.Any(), gomock.Any()).Return("listing-id", nil).AnyTimes()
	database.EXPECT().UpsertPrices(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().UpsertPricesForRange(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobPartTotalCount(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().AddJobPartProcessedCount(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().AppendValidationErrors(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobTotalCount(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobProcessedCount(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	j := &JobRequest{JobID: "job-sa-1", JobType: db.JobTypeSystemArchive}
	database.EXPECT().LoadJobPayload(gomock.Any(), j.JobID).Return(systemArchivePayload(t), nil)
	database.EXPECT().GetJob(gomock.Any(), j.JobID).Return(&db.JobDetail{
		UserID: "user-1",
		Parts:  partRows(archivev1.ArchivePart_INSTRUMENTS, archivev1.ArchivePart_PRICES, archivev1.ArchivePart_CORPORATE_EVENTS),
	}, nil).AnyTimes()

	var order []string
	database.EXPECT().SetJobPartStatus(gomock.Any(), j.JobID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, part archivev1.ArchivePart, st apiv1.JobStatus) error {
			order = append(order, part.String()+":"+st.String())
			return nil
		}).AnyTimes()
	gomock.InOrder(
		database.EXPECT().SetJobStatus(gomock.Any(), j.JobID, apiv1.JobStatus_SUCCESS).Return(nil),
		database.EXPECT().ClearJobPayload(gomock.Any(), j.JobID).Return(nil),
	)

	processSystemImport(context.Background(), ingestDeps{DB: database, Registry: identifier.NewRegistry()}, j)

	want := []string{
		"INSTRUMENTS:RUNNING", "INSTRUMENTS:SUCCESS",
		"PRICES:RUNNING", "PRICES:SUCCESS",
		"CORPORATE_EVENTS:RUNNING", "CORPORATE_EVENTS:SUCCESS",
	}
	if len(order) != len(want) {
		t.Fatalf("part transitions = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("part transitions = %v, want %v", order, want)
		}
	}
}

// A part that fails does not stop the ones after it: the parts are not
// hard-dependent, and abandoning the rest would throw away work that would
// otherwise land. The job is FAILED, and the per-part results say which part.
func TestProcessSystemImport_FailedPartDoesNotStopTheRest(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().MergeInstrumentFromArchive(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("inst-1", nil).AnyTimes()
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return("inst-1", "STOCK", []string{"USD"}, nil).AnyTimes()
	database.EXPECT().EnsureInstrument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("inst-1", "listing-id", nil).AnyTimes()
	database.EXPECT().EnsureArchiveInstrument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("inst-1", "listing-id", nil).AnyTimes()
	database.EXPECT().EnsureListing(gomock.Any(), gomock.Any(), gomock.Any()).Return("listing-id", nil).AnyTimes()
	database.EXPECT().SetJobPartTotalCount(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().AddJobPartProcessedCount(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().AppendValidationErrors(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobTotalCount(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobProcessedCount(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	var transitions []string
	database.EXPECT().SetJobPartStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, part archivev1.ArchivePart, st apiv1.JobStatus) error {
			transitions = append(transitions, part.String()+":"+st.String())
			return nil
		}).AnyTimes()

	// The price part cannot write, which is a hard failure for that part.
	database.EXPECT().UpsertPrices(gomock.Any(), gomock.Any()).Return(errors.New("disk on fire")).AnyTimes()
	database.EXPECT().UpsertPricesForRange(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("disk on fire")).AnyTimes()

	j := &JobRequest{JobID: "job-sa-2", JobType: db.JobTypeSystemArchive}
	database.EXPECT().LoadJobPayload(gomock.Any(), j.JobID).Return(systemArchivePayload(t), nil)
	database.EXPECT().GetJob(gomock.Any(), j.JobID).Return(&db.JobDetail{
		UserID: "user-1",
		Parts:  partRows(archivev1.ArchivePart_INSTRUMENTS, archivev1.ArchivePart_PRICES, archivev1.ArchivePart_CORPORATE_EVENTS),
	}, nil).AnyTimes()

	var failedPart archivev1.ArchivePart
	database.EXPECT().SetJobPartFailed(gomock.Any(), j.JobID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, part archivev1.ArchivePart, _ string) error {
			failedPart = part
			return nil
		})
	// The corporate event part still runs, which is what proves the sequence
	// carried on past the failure.
	database.EXPECT().UpsertCorporateEventCoverage(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobStatus(gomock.Any(), j.JobID, apiv1.JobStatus_FAILED).Return(nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), j.JobID).Return(nil)

	processSystemImport(context.Background(), ingestDeps{DB: database, Registry: identifier.NewRegistry()}, j)

	if failedPart != archivev1.ArchivePart_PRICES {
		t.Fatalf("failed part = %v, want PRICES", failedPart)
	}
	// The part after the failure still ran and still succeeded, which is the
	// whole point: a failed part costs its own work and nothing else's.
	if !contains(transitions, "CORPORATE_EVENTS:RUNNING") || !contains(transitions, "CORPORATE_EVENTS:SUCCESS") {
		t.Fatalf("corporate events did not run after the price part failed: %v", transitions)
	}
	// And the failed part was never marked SUCCESS.
	if contains(transitions, "PRICES:SUCCESS") {
		t.Fatalf("a failed part was marked SUCCESS: %v", transitions)
	}
}

// A job resumed after a restart skips the parts that already finished rather
// than redoing them.
func TestProcessSystemImport_ResumeSkipsFinishedParts(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().MergeInstrumentFromArchive(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("inst-1", nil).AnyTimes()
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return("inst-1", "STOCK", []string{"USD"}, nil).AnyTimes()
	database.EXPECT().EnsureListing(gomock.Any(), gomock.Any(), gomock.Any()).Return("listing-id", nil).AnyTimes()
	database.EXPECT().UpsertPrices(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().UpsertPricesForRange(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobPartTotalCount(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().AddJobPartProcessedCount(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().AppendValidationErrors(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobTotalCount(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobProcessedCount(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobPartStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().ClearJobPayload(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	j := &JobRequest{JobID: "job-sa-3", JobType: db.JobTypeSystemArchive}
	database.EXPECT().LoadJobPayload(gomock.Any(), j.JobID).Return(systemArchivePayload(t), nil)
	database.EXPECT().GetJob(gomock.Any(), j.JobID).Return(&db.JobDetail{
		UserID: "user-1",
		Parts: []db.JobPartResult{
			{Part: archivev1.ArchivePart_INSTRUMENTS, Status: apiv1.JobStatus_SUCCESS, TotalCount: 1, ProcessedCount: 1},
			// Interrupted halfway, so its progress is reset before it re-runs.
			{Part: archivev1.ArchivePart_PRICES, Status: apiv1.JobStatus_RUNNING, TotalCount: 1, ProcessedCount: 1},
		},
	}, nil).AnyTimes()

	// The finished instrument part is not re-ensured.
	database.EXPECT().EnsureInstrument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	database.EXPECT().ResetJobPartProgress(gomock.Any(), j.JobID, archivev1.ArchivePart_PRICES).Return(nil)

	processSystemImport(context.Background(), ingestDeps{DB: database, Registry: identifier.NewRegistry()}, j)
}

// A payload that cannot be read is the job's failure rather than any part's,
// because no part was reached.
func TestProcessSystemImport_UnreadablePayloadFailsTheJob(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	j := &JobRequest{JobID: "job-sa-4", JobType: db.JobTypeSystemArchive}

	database.EXPECT().LoadJobPayload(gomock.Any(), j.JobID).Return([]byte("not a proto"), nil)
	database.EXPECT().
		AppendValidationErrors(gomock.Any(), j.JobID, archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED, gomock.Any()).
		Return(nil)
	gomock.InOrder(
		database.EXPECT().SetJobStatus(gomock.Any(), j.JobID, apiv1.JobStatus_FAILED).Return(nil),
		database.EXPECT().ClearJobPayload(gomock.Any(), j.JobID).Return(nil),
	)

	processSystemImport(context.Background(), ingestDeps{DB: database, Registry: identifier.NewRegistry()}, j)
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
