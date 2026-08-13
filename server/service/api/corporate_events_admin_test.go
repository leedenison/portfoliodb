package api

import (
	"errors"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

// The admin RPCs over the unhandled corporate event queue: what is in it, how much
// of it there is, and marking one dealt with.

// A page of events, with the instrument name looked up per event for display and
// the ex-date written as a plain date rather than an instant.
func TestListUnhandledCorporateEvents(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	exDate := time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC)
	created := time.Date(2025, 6, 1, 9, 30, 0, 0, time.UTC)
	name := "APPLE INC"
	mockDB.EXPECT().ListUnhandledCorporateEvents(gomock.Any(), false, int32(50), "").Return(
		[]db.UnhandledCorporateEvent{{
			ID: "e1", InstrumentID: "i1", EventType: "SPINOFF",
			ExDate: &exDate, Detail: "unsupported", Data: []byte(`{"a":1}`),
			Resolved: false, CreatedAt: created,
		}}, int32(1), "next", nil)
	mockDB.EXPECT().GetInstrument(gomock.Any(), "i1").Return(&db.InstrumentRow{ID: "i1", Name: &name}, nil)

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
	if e.GetInstrumentName() != name {
		t.Errorf("instrument name: got %q, want %q", e.GetInstrumentName(), name)
	}
	if e.GetData() != `{"a":1}` {
		t.Errorf("data: got %q", e.GetData())
	}
}

// An unset page_size is a default rather than a page of nothing.
func TestListUnhandledCorporateEvents_DefaultsPageSize(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListUnhandledCorporateEvents(gomock.Any(), true, int32(50), "tok").Return(nil, int32(0), "", nil)

	_, err := srv.ListUnhandledCorporateEvents(adminCtx("admin-1", "sub|admin"), &apiv1.ListUnhandledCorporateEventsRequest{
		IncludeResolved: true, PageToken: "tok",
	})
	if err != nil {
		t.Fatalf("ListUnhandledCorporateEvents: %v", err)
	}
}

func TestListUnhandledCorporateEvents_StoreError(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListUnhandledCorporateEvents(gomock.Any(), false, int32(50), "").Return(nil, int32(0), "", errors.New("boom"))

	_, err := srv.ListUnhandledCorporateEvents(adminCtx("admin-1", "sub|admin"), &apiv1.ListUnhandledCorporateEventsRequest{})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

func TestCountUnhandledCorporateEvents(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().CountUnhandledCorporateEvents(gomock.Any()).Return(int32(7), nil)

	resp, err := srv.CountUnhandledCorporateEvents(adminCtx("admin-1", "sub|admin"), &apiv1.CountUnhandledCorporateEventsRequest{})
	if err != nil {
		t.Fatalf("CountUnhandledCorporateEvents: %v", err)
	}
	if resp.GetCount() != 7 {
		t.Errorf("count: got %d, want 7", resp.GetCount())
	}
}

func TestCountUnhandledCorporateEvents_StoreError(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().CountUnhandledCorporateEvents(gomock.Any()).Return(int32(0), errors.New("boom"))

	_, err := srv.CountUnhandledCorporateEvents(adminCtx("admin-1", "sub|admin"), &apiv1.CountUnhandledCorporateEventsRequest{})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

func TestResolveUnhandledCorporateEvent(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ResolveUnhandledCorporateEvent(gomock.Any(), "e1").Return(nil)

	_, err := srv.ResolveUnhandledCorporateEvent(adminCtx("admin-1", "sub|admin"), &apiv1.ResolveUnhandledCorporateEventRequest{Id: "e1"})
	if err != nil {
		t.Fatalf("ResolveUnhandledCorporateEvent: %v", err)
	}
}

// Resolving nothing in particular would mark whichever row the store guessed at, so
// the id is required before it is asked.
func TestResolveUnhandledCorporateEvent_MissingID(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.ResolveUnhandledCorporateEvent(adminCtx("admin-1", "sub|admin"), &apiv1.ResolveUnhandledCorporateEventRequest{})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
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
		{"ResolveUnhandledCorporateEvent", func() error {
			_, err := srv.ResolveUnhandledCorporateEvent(ctx, &apiv1.ResolveUnhandledCorporateEventRequest{Id: "e1"})
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testutil.RequireGRPCCode(t, tc.call(), codes.PermissionDenied)
		})
	}
}
