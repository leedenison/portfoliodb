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

// The admin RPCs over the corporate events a run could not apply: what is in the
// list and how much of it there is.
//
// They read the telemetry schema, which is why the server is built with a
// telemetry mock as well as a store mock. Nothing marks one dealt with any more:
// an operator's decision is state, and telemetry records events (adr/0080).

// newAPIServerWithTelemetry is newAPIServerWithMock plus the telemetry reader the
// unhandled-event RPCs go through.
func newAPIServerWithTelemetry(t *testing.T) (*Server, *mock.MockDB, *mock.MockTelemetryDB) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	store := mock.NewMockDB(ctrl)
	tel := mock.NewMockTelemetryDB(ctrl)
	return NewServer(ServerConfig{DB: store, TelemetryDB: tel}), store, tel
}

// A page of events, with the instrument name looked up per event for display and
// the ex-date written as a plain date rather than an instant.
func TestListUnhandledCorporateEvents(t *testing.T) {
	srv, store, tel := newAPIServerWithTelemetry(t)
	exDate := time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC)
	created := time.Date(2025, 6, 1, 9, 30, 0, 0, time.UTC)
	name := "APPLE INC"
	tel.EXPECT().ListUnhandledCorporateEvents(gomock.Any(), int32(50), "").Return(
		[]db.UnhandledCorporateEvent{{
			ID: "e1", RunID: "run-1", InstrumentID: "i1", EventType: "SPINOFF",
			ExDate: &exDate, Detail: "unsupported", Data: []byte(`{"a":1}`),
			CreatedAt: created,
		}}, int32(1), "next", nil)
	store.EXPECT().GetInstrument(gomock.Any(), "i1").Return(&db.InstrumentRow{ID: "i1", Name: &name}, nil)

	resp, err := srv.ListUnhandledCorporateEvents(adminCtx("admin-1", "sub|admin"), &apiv1.ListUnhandledCorporateEventsRequest{})
	if err != nil {
		t.Fatalf("ListUnhandledCorporateEvents: %v", err)
	}
	if resp.GetTotalCount() != 1 || resp.GetNextPageToken() != "next" {
		t.Errorf("page: got total=%d token=%q, want 1 and \"next\"", resp.GetTotalCount(), resp.GetNextPageToken())
	}
	if len(resp.GetEvents()) != 1 {
		t.Fatalf("events: got %d, want 1", len(resp.GetEvents()))
	}
	e := resp.GetEvents()[0]
	if e.GetId() != "e1" || e.GetEventType() != "SPINOFF" || e.GetExDate() != "2025-06-02" {
		t.Errorf("event: got %+v", e)
	}
	// The run it failed under, which is how long it has been failing: the same
	// event on ten cycles is ten rows under ten runs.
	if e.GetRunId() != "run-1" {
		t.Errorf("run: got %q, want run-1", e.GetRunId())
	}
	if e.GetInstrumentName() != name {
		t.Errorf("instrument name: got %q, want %q", e.GetInstrumentName(), name)
	}
	if e.GetData() != `{"a":1}` {
		t.Errorf("data: got %q", e.GetData())
	}
}

// An instrument a merge has deleted leaves the name empty rather than failing
// the page. Telemetry outlives the work it describes, so the row is expected to
// outlive the instrument it names.
func TestListUnhandledCorporateEvents_MissingInstrumentIsNotFatal(t *testing.T) {
	srv, store, tel := newAPIServerWithTelemetry(t)
	tel.EXPECT().ListUnhandledCorporateEvents(gomock.Any(), int32(50), "").Return(
		[]db.UnhandledCorporateEvent{{ID: "e1", InstrumentID: "gone"}}, int32(1), "", nil)
	store.EXPECT().GetInstrument(gomock.Any(), "gone").Return(nil, errors.New("no such instrument"))

	resp, err := srv.ListUnhandledCorporateEvents(adminCtx("admin-1", "sub|admin"), &apiv1.ListUnhandledCorporateEventsRequest{})
	if err != nil {
		t.Fatalf("ListUnhandledCorporateEvents: %v", err)
	}
	if n := len(resp.GetEvents()); n != 1 {
		t.Fatalf("events: got %d, want the row listed with no name", n)
	}
	if got := resp.GetEvents()[0].GetInstrumentName(); got != "" {
		t.Errorf("instrument name: got %q, want empty", got)
	}
}

// An unset page_size is a default rather than a page of nothing.
func TestListUnhandledCorporateEvents_DefaultsPageSize(t *testing.T) {
	srv, _, tel := newAPIServerWithTelemetry(t)
	tel.EXPECT().ListUnhandledCorporateEvents(gomock.Any(), int32(50), "tok").Return(nil, int32(0), "", nil)

	_, err := srv.ListUnhandledCorporateEvents(adminCtx("admin-1", "sub|admin"), &apiv1.ListUnhandledCorporateEventsRequest{
		PageToken: "tok",
	})
	if err != nil {
		t.Fatalf("ListUnhandledCorporateEvents: %v", err)
	}
}

func TestListUnhandledCorporateEvents_StoreError(t *testing.T) {
	srv, _, tel := newAPIServerWithTelemetry(t)
	tel.EXPECT().ListUnhandledCorporateEvents(gomock.Any(), int32(50), "").Return(nil, int32(0), "", errors.New("boom"))

	_, err := srv.ListUnhandledCorporateEvents(adminCtx("admin-1", "sub|admin"), &apiv1.ListUnhandledCorporateEventsRequest{})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

// A deployment with no telemetry pool lists nothing rather than failing. Nothing
// the system does turns on the answer, which is what lets the RPC read this
// schema at all.
func TestListUnhandledCorporateEvents_WithoutTelemetryListsNothing(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	resp, err := srv.ListUnhandledCorporateEvents(adminCtx("admin-1", "sub|admin"), &apiv1.ListUnhandledCorporateEventsRequest{})
	if err != nil {
		t.Fatalf("ListUnhandledCorporateEvents: %v", err)
	}
	if len(resp.GetEvents()) != 0 || resp.GetTotalCount() != 0 {
		t.Errorf("got %+v, want an empty page", resp)
	}
}

func TestCountUnhandledCorporateEvents(t *testing.T) {
	srv, _, tel := newAPIServerWithTelemetry(t)
	tel.EXPECT().CountUnhandledCorporateEvents(gomock.Any()).Return(int32(7), nil)

	resp, err := srv.CountUnhandledCorporateEvents(adminCtx("admin-1", "sub|admin"), &apiv1.CountUnhandledCorporateEventsRequest{})
	if err != nil {
		t.Fatalf("CountUnhandledCorporateEvents: %v", err)
	}
	if resp.GetCount() != 7 {
		t.Errorf("count: got %d, want 7", resp.GetCount())
	}
}

func TestCountUnhandledCorporateEvents_StoreError(t *testing.T) {
	srv, _, tel := newAPIServerWithTelemetry(t)
	tel.EXPECT().CountUnhandledCorporateEvents(gomock.Any()).Return(int32(0), errors.New("boom"))

	_, err := srv.CountUnhandledCorporateEvents(adminCtx("admin-1", "sub|admin"), &apiv1.CountUnhandledCorporateEventsRequest{})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

func TestCorporateEventQueue_AdminOnly(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	tests := []struct {
		name string
		call func() error
	}{
		{"ListUnhandledCorporateEvents", func() error {
			_, err := srv.ListUnhandledCorporateEvents(ctx, &apiv1.ListUnhandledCorporateEventsRequest{})
			return err
		}},
		{"CountUnhandledCorporateEvents", func() error {
			_, err := srv.CountUnhandledCorporateEvents(ctx, &apiv1.CountUnhandledCorporateEventsRequest{})
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testutil.RequireGRPCCode(t, tc.call(), codes.PermissionDenied)
		})
	}
}
