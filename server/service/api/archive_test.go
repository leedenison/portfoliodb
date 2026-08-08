package api

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/archive"
	dbpkg "github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
)

func systemArchive(mutate func(*archivev1.SystemArchive)) *apiv1.ImportSystemArchiveRequest {
	a := &archivev1.SystemArchive{
		Envelope: archive.NewEnvelope("portfoliodb.example.com", archivev1.ArchiveKind_SYSTEM),
	}
	if mutate != nil {
		mutate(a)
	}
	return &apiv1.ImportSystemArchiveRequest{Archive: a, Filename: "system-archive.json"}
}

func TestImportSystemArchive_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	req := systemArchive(func(a *archivev1.SystemArchive) {
		a.Instruments = &archivev1.InstrumentPart{}
	})
	_, err := srv.ImportSystemArchive(authCtx("user-1", "sub|1"), req)
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestImportSystemArchive_NoParts_ReturnsError(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.ImportSystemArchive(adminCtx("user-1", "sub|1"), systemArchive(nil))
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestImportSystemArchive_NewerFormatVersion_Refused(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	req := systemArchive(func(a *archivev1.SystemArchive) {
		a.Instruments = &archivev1.InstrumentPart{}
		a.Envelope.FormatVersion = archive.FormatVersion + 1
	})
	// The request is well formed and this server is the thing that is out of
	// date, so this is a precondition rather than a bad argument.
	_, err := srv.ImportSystemArchive(adminCtx("user-1", "sub|1"), req)
	testutil.RequireGRPCCode(t, err, codes.FailedPrecondition)
}

func TestImportSystemArchive_UserArchive_Refused(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	req := systemArchive(func(a *archivev1.SystemArchive) {
		a.Instruments = &archivev1.InstrumentPart{}
		a.Envelope.Kind = archivev1.ArchiveKind_USER
	})
	_, err := srv.ImportSystemArchive(adminCtx("user-1", "sub|1"), req)
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

// The job records the parts the document carried, in restore order, so a caller
// polling immediately sees what the import will apply.
func TestImportSystemArchive_QueuesJobWithPresentParts(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	req := systemArchive(func(a *archivev1.SystemArchive) {
		a.CorporateEvents = &archivev1.CorporateEventPart{}
		a.Instruments = &archivev1.InstrumentPart{}
	})
	var enqueuedType string
	srv.enqueueJob = func(_, jobType string) error {
		enqueuedType = jobType
		return nil
	}
	var got dbpkg.CreateJobParams
	mockDB.EXPECT().CreateJob(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params dbpkg.CreateJobParams) (string, error) {
			got = params
			return "job-1", nil
		})
	resp, err := srv.ImportSystemArchive(adminCtx("user-1", "sub|1"), req)
	if err != nil {
		t.Fatalf("ImportSystemArchive: %v", err)
	}
	if resp.GetJobId() != "job-1" {
		t.Fatalf("job_id = %q", resp.GetJobId())
	}
	if got.JobType != dbpkg.JobTypeSystemArchive || got.Filename != "system-archive.json" {
		t.Fatalf("job = %q %q", got.JobType, got.Filename)
	}
	if enqueuedType != dbpkg.JobTypeSystemArchive {
		t.Fatalf("enqueued as %q", enqueuedType)
	}
	want := []archivev1.ArchivePart{archivev1.ArchivePart_INSTRUMENTS, archivev1.ArchivePart_CORPORATE_EVENTS}
	if len(got.Parts) != len(want) {
		t.Fatalf("parts = %v, want %v", got.Parts, want)
	}
	for i := range want {
		if got.Parts[i] != want[i] {
			t.Fatalf("parts = %v, want %v", got.Parts, want)
		}
	}
	if len(got.Payload) == 0 {
		t.Fatal("job carries no payload")
	}
}

// A part present but empty says the export included it and there was nothing.
// That is a different statement from a part never included, so it is applied.
func TestImportSystemArchive_EmptyPartIsStillApplied(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	req := systemArchive(func(a *archivev1.SystemArchive) {
		a.Prices = &archivev1.PricePart{}
	})
	srv.enqueueJob = func(_, _ string) error { return nil }
	var got dbpkg.CreateJobParams
	mockDB.EXPECT().CreateJob(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params dbpkg.CreateJobParams) (string, error) {
			got = params
			return "job-2", nil
		})
	if _, err := srv.ImportSystemArchive(adminCtx("user-1", "sub|1"), req); err != nil {
		t.Fatalf("ImportSystemArchive: %v", err)
	}
	if len(got.Parts) != 1 || got.Parts[0] != archivev1.ArchivePart_PRICES {
		t.Fatalf("parts = %v, want [PRICES]", got.Parts)
	}
}
