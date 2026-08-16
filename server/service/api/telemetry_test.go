package api

import (
	"errors"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/testutil"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

// newPurgeServer returns a server with a mocked telemetry writer.
func newPurgeServer(t *testing.T) (*Server, *mock.MockTelemetryDB) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	tel := mock.NewMockTelemetryDB(ctrl)
	return NewServer(ServerConfig{DB: mock.NewMockDB(ctrl), TelemetryDB: tel}), tel
}

func TestPurgeTelemetry_RequiresAdmin(t *testing.T) {
	srv, _ := newPurgeServer(t)
	_, err := srv.PurgeTelemetry(authCtx("user-1", "sub|user"), &apiv1.PurgeTelemetryRequest{})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

// TestPurgeTelemetry_DeletesPastTheRetentionWindow pins the cutoff being the
// window rather than anything the caller chose. The scheduler says when to run;
// how far back to keep is the schema's business.
func TestPurgeTelemetry_DeletesPastTheRetentionWindow(t *testing.T) {
	srv, tel := newPurgeServer(t)
	var got time.Time
	tel.EXPECT().PurgeRunsBefore(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, cutoff time.Time) (int64, error) {
			got = cutoff
			return 7, nil
		})

	resp, err := srv.PurgeTelemetry(adminCtx("admin-1", "sub|admin"), &apiv1.PurgeTelemetryRequest{})
	if err != nil {
		t.Fatalf("PurgeTelemetry: %v", err)
	}
	if resp.GetRunsDeleted() != 7 {
		t.Errorf("runs_deleted = %d, want 7", resp.GetRunsDeleted())
	}
	want := time.Now().Add(-db.TelemetryRetention)
	if diff := got.Sub(want); diff > time.Minute || diff < -time.Minute {
		t.Errorf("cutoff = %v, want within a minute of %v", got, want)
	}
}

func TestPurgeTelemetry_ReportsAFailedDelete(t *testing.T) {
	srv, tel := newPurgeServer(t)
	tel.EXPECT().PurgeRunsBefore(gomock.Any(), gomock.Any()).Return(int64(0), errors.New("boom"))

	_, err := srv.PurgeTelemetry(adminCtx("admin-1", "sub|admin"), &apiv1.PurgeTelemetryRequest{})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

// TestPurgeTelemetry_UnconfiguredIsUnavailable covers a server built without a
// writer, which says so rather than reporting nothing deleted -- a scheduler
// reading zeroes forever would have no way to tell the two apart.
func TestPurgeTelemetry_UnconfiguredIsUnavailable(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.PurgeTelemetry(adminCtx("admin-1", "sub|admin"), &apiv1.PurgeTelemetryRequest{})
	testutil.RequireGRPCCode(t, err, codes.Unavailable)
}
