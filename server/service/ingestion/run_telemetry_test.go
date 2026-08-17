package ingestion

import (
	"context"
	"errors"
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	ingestionv1 "github.com/leedenison/portfoliodb/proto/ingestion/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	"go.uber.org/mock/gomock"
)

// expectRun records the run a job opens and the outcome it is stamped with, and
// returns pointers to both so a test can assert on them after processJob returns.
func expectRun(tel *mock.MockTelemetryDB, runID string) (*db.TelemetryRun, *string) {
	var started db.TelemetryRun
	var outcome string
	tel.EXPECT().
		StartRun(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, r db.TelemetryRun) string {
			started = r
			return runID
		})
	tel.EXPECT().
		EndRun(gomock.Any(), runID, gomock.Any()).
		Do(func(_ context.Context, _, o string) { outcome = o })
	return &started, &outcome
}

// TestJobRunScopeComesFromTheJobRow pins where a run's scope is read from. Taking
// it off the job row rather than the payload is what lets a job whose payload will
// not load still record whose upload it was, which is the case most worth seeing.
func TestJobRunScopeComesFromTheJobRow(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	database := mock.NewMockDB(ctrl)
	tel := mock.NewMockTelemetryDB(ctrl)
	j := &JobRequest{JobID: "job-1", JobType: db.JobTypeTx}

	database.EXPECT().SetJobStatus(gomock.Any(), "job-1", apiv1.JobStatus_RUNNING).Return(nil)
	database.EXPECT().GetJob(gomock.Any(), "job-1").Return(&db.JobDetail{
		Status: apiv1.JobStatus_RUNNING, UserID: "user-1", Broker: "FIDELITY", Source: "FIDELITY:acct:history",
	}, nil)
	database.EXPECT().LoadJobPayload(gomock.Any(), "job-1").Return(nil, errors.New("payload gone"))
	database.EXPECT().SetJobStatus(gomock.Any(), "job-1", apiv1.JobStatus_FAILED).Return(nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-1").Return(nil)
	started, outcome := expectRun(tel, "run-1")

	processJob(context.Background(), WorkerOptions{DB: database, TelemetryDB: tel}, j)

	want := db.TelemetryRun{
		Kind:   db.TelemetryRunTxImport,
		JobID:  "job-1",
		UserID: "user-1",
		Broker: "FIDELITY",
		Source: "FIDELITY:acct:history",
	}
	if *started != want {
		t.Errorf("StartRun(%+v), want %+v", *started, want)
	}
	if *outcome != db.TelemetryOutcomeFailed {
		t.Errorf("outcome = %q, want %q", *outcome, db.TelemetryOutcomeFailed)
	}
}

// TestJobRunStampsSuccess pins the other half: a job that reaches SUCCESS stamps
// its run success, so the run's outcome and the job's agree without a join.
func TestJobRunStampsSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	database := mock.NewMockDB(ctrl)
	tel := mock.NewMockTelemetryDB(ctrl)
	registry := identifier.NewRegistry()

	// A window carrying no postings and no period, which is the shortest path to
	// a job that stores nothing and still succeeds.
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Window: &archivev1.TxWindow{
			Broker: typev1.Broker_IBKR,
			Source: "IBKR:test:statement",
		},
	})
	j := &JobRequest{JobID: "job-2", JobType: db.JobTypeTx}

	database.EXPECT().SetJobStatus(gomock.Any(), "job-2", apiv1.JobStatus_RUNNING).Return(nil)
	database.EXPECT().GetJob(gomock.Any(), "job-2").Return(
		&db.JobDetail{Status: apiv1.JobStatus_RUNNING, UserID: "user-1"}, nil)
	database.EXPECT().LoadJobPayload(gomock.Any(), "job-2").Return(payload, nil)
	database.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return(nil, nil)
	database.EXPECT().SetJobStatus(gomock.Any(), "job-2", apiv1.JobStatus_SUCCESS).Return(nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-2").Return(nil)
	_, outcome := expectRun(tel, "run-2")

	processJob(context.Background(), WorkerOptions{
		DB: database, TelemetryDB: tel, IdentifierRegistry: registry,
	}, j)

	if *outcome != db.TelemetryOutcomeSuccess {
		t.Errorf("outcome = %q, want %q", *outcome, db.TelemetryOutcomeSuccess)
	}
}

// TestUnknownJobTypeOpensNoRun pins the closed vocabulary. A job type this build
// does not know has no kind to file a run under, and inventing one would put a
// value in the column no panel could interpret.
func TestUnknownJobTypeOpensNoRun(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	database := mock.NewMockDB(ctrl)
	tel := mock.NewMockTelemetryDB(ctrl)
	j := &JobRequest{JobID: "job-3", JobType: "not_a_job_type"}

	database.EXPECT().SetJobStatus(gomock.Any(), "job-3", apiv1.JobStatus_RUNNING).Return(nil)
	database.EXPECT().GetJob(gomock.Any(), "job-3").Return(&db.JobDetail{UserID: "user-1"}, nil)
	database.EXPECT().SetJobStatus(gomock.Any(), "job-3", apiv1.JobStatus_FAILED).Return(nil)
	// No StartRun. EndRun is still called, with the empty id the writer skips on.
	tel.EXPECT().EndRun(gomock.Any(), "", db.TelemetryOutcomeFailed)

	processJob(context.Background(), WorkerOptions{DB: database, TelemetryDB: tel}, j)
}

// TestJobRunKinds pins the map from job type to run kind. The three import kinds
// in the vocabulary are exactly the three job types; anything else opens no run.
func TestJobRunKinds(t *testing.T) {
	tests := []struct {
		name    string
		jobType string
		want    string
	}{
		{name: "tx", jobType: db.JobTypeTx, want: db.TelemetryRunTxImport},
		{name: "system archive", jobType: db.JobTypeSystemArchive, want: db.TelemetryRunSystemArchiveImport},
		{name: "user archive", jobType: db.JobTypeUserArchive, want: db.TelemetryRunUserArchiveImport},
		{name: "unknown", jobType: "price", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := telemetryRunKind(tc.jobType); got != tc.want {
				t.Errorf("telemetryRunKind(%q) = %q, want %q", tc.jobType, got, tc.want)
			}
		})
	}
}
