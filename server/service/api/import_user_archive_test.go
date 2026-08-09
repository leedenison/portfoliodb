package api

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/archive"
	dbpkg "github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
)

func userArchive(mutate func(*archivev1.UserArchive)) *apiv1.ImportUserArchiveRequest {
	a := &archivev1.UserArchive{
		Envelope: archive.NewEnvelope("portfoliodb.example.com", archivev1.ArchiveKind_USER),
	}
	if mutate != nil {
		mutate(a)
	}
	return &apiv1.ImportUserArchiveRequest{Archive: a, Filename: "user-archive.json"}
}

func TestImportUserArchive_Unauthenticated(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	req := userArchive(func(a *archivev1.UserArchive) { a.Preferences = &archivev1.PreferencePart{} })
	_, err := srv.ImportUserArchive(ctxNoAuth(), req)
	testutil.RequireGRPCCode(t, err, codes.Unauthenticated)
}

func TestImportUserArchive_NoParts_ReturnsError(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.ImportUserArchive(authCtx("user-1", "sub|1"), userArchive(nil))
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

// The document's message type says which archive it is, but protojson records
// no type name, so the envelope has to carry it and the importer has to check.
func TestImportUserArchive_SystemArchive_Refused(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	req := userArchive(func(a *archivev1.UserArchive) {
		a.Preferences = &archivev1.PreferencePart{}
		a.Envelope.Kind = archivev1.ArchiveKind_SYSTEM
	})
	_, err := srv.ImportUserArchive(authCtx("user-1", "sub|1"), req)
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

// The request is well formed and this server is the thing that is out of date,
// so this is a precondition rather than a bad argument.
func TestImportUserArchive_NewerFormatVersion_Refused(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	req := userArchive(func(a *archivev1.UserArchive) {
		a.Preferences = &archivev1.PreferencePart{}
		a.Envelope.FormatVersion = archive.FormatVersion + 1
	})
	_, err := srv.ImportUserArchive(authCtx("user-1", "sub|1"), req)
	testutil.RequireGRPCCode(t, err, codes.FailedPrecondition)
}

func TestImportUserArchive_CreatesAndEnqueuesTheJob(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	req := userArchive(func(a *archivev1.UserArchive) {
		a.Preferences = &archivev1.PreferencePart{}
	})
	var enqueuedType string
	srv.enqueueJob = func(_, jobType string) error { enqueuedType = jobType; return nil }
	var got dbpkg.CreateJobParams
	mockDB.EXPECT().CreateJob(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params dbpkg.CreateJobParams) (string, error) {
			got = params
			return "job-ua-1", nil
		})

	resp, err := srv.ImportUserArchive(authCtx("user-9", "sub|9"), req)
	if err != nil {
		t.Fatalf("ImportUserArchive: %v", err)
	}
	if resp.GetJobId() != "job-ua-1" {
		t.Fatalf("job_id = %q", resp.GetJobId())
	}
	if got.JobType != dbpkg.JobTypeUserArchive || enqueuedType != dbpkg.JobTypeUserArchive {
		t.Fatalf("job type = %q, enqueued as %q", got.JobType, enqueuedType)
	}
	// A user archive does not name its user, so the account it restores into is
	// whoever asked.
	if got.UserID != "user-9" {
		t.Fatalf("user = %q", got.UserID)
	}
	if len(got.Parts) != 1 || got.Parts[0] != archivev1.ArchivePart_PREFERENCES {
		t.Fatalf("parts = %v, want [PREFERENCES]", got.Parts)
	}
	if len(got.Payload) == 0 {
		t.Fatal("job carries no payload")
	}
}

func TestImportUserArchive_FullQueue_Unavailable(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	req := userArchive(func(a *archivev1.UserArchive) { a.Preferences = &archivev1.PreferencePart{} })
	srv.enqueueJob = func(_, _ string) error { return errors.New("job queue full") }
	mockDB.EXPECT().CreateJob(gomock.Any(), gomock.Any()).Return("job-ua-2", nil)
	_, err := srv.ImportUserArchive(authCtx("user-1", "sub|1"), req)
	testutil.RequireGRPCCode(t, err, codes.Unavailable)
}
