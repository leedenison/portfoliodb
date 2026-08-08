package archiveimport

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

func unhandledEventPart(groups ...*archivev1.UnhandledEventGroup) *archivev1.UnhandledEventPart {
	return &archivev1.UnhandledEventPart{Groups: groups}
}

func unhandledEventGroup(value string, events ...*archivev1.UnhandledEvent) *archivev1.UnhandledEventGroup {
	return &archivev1.UnhandledEventGroup{
		Instrument: &archivev1.InstrumentRef{
			Type:   typev1.IdentifierType_MIC_TICKER,
			Value:  value,
			Domain: "XNAS",
		},
		Events: events,
	}
}

// The resolved flag is the point of the part, so it has to survive the write.
func TestUnhandledEventPart_RestoresResolvedAndUnresolved(t *testing.T) {
	database, rep := newPartTest(t)
	expectFound(database, "inst-1")
	database.EXPECT().RestoreUnhandledCorporateEvents(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, events []db.UnhandledCorporateEvent) (int, error) {
			if len(events) != 2 {
				t.Fatalf("expected 2 events, got %d", len(events))
			}
			if !events[0].Resolved || events[1].Resolved {
				t.Errorf("resolved = %v, %v", events[0].Resolved, events[1].Resolved)
			}
			if events[0].ExDate == nil || !events[0].ExDate.Equal(time.Date(2025, 4, 11, 0, 0, 0, 0, time.UTC)) {
				t.Errorf("ex_date = %v", events[0].ExDate)
			}
			if string(events[0].Data) != `{"split_from":"10"}` {
				t.Errorf("data = %s", events[0].Data)
			}
			return len(events), nil
		})

	part := unhandledEventPart(unhandledEventGroup("XYZ",
		&archivev1.UnhandledEvent{
			EventType: "REVERSE_SPLIT", ExDate: proto.String("2025-04-11"),
			Detail: "1:10 reverse split", DataJson: proto.String(`{"split_from":"10"}`),
			Resolved: true,
		},
		&archivev1.UnhandledEvent{EventType: "MERGER", Detail: "merged into ABC"},
	))
	written, err := UnhandledEventPart(context.Background(), database, part, nil, rep)
	if err != nil {
		t.Fatalf("UnhandledEventPart: %v", err)
	}
	if written != 2 {
		t.Fatalf("written = %d, want 2", written)
	}
}

// An event whose file does not say when it was detected falls back to the
// envelope's knowledge time.
func TestUnhandledEventPart_DetectedAtFallsBackToTheEnvelope(t *testing.T) {
	database, rep := newPartTest(t)
	expectFound(database, "inst-1")
	asOf := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	detected := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	database.EXPECT().RestoreUnhandledCorporateEvents(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, events []db.UnhandledCorporateEvent) (int, error) {
			if !events[0].CreatedAt.Equal(detected) {
				t.Errorf("stated detected_at = %v, want %v", events[0].CreatedAt, detected)
			}
			if !events[1].CreatedAt.Equal(asOf) {
				t.Errorf("fallback detected_at = %v, want %v", events[1].CreatedAt, asOf)
			}
			return len(events), nil
		})

	part := unhandledEventPart(unhandledEventGroup("XYZ",
		&archivev1.UnhandledEvent{EventType: "MERGER", DetectedAt: timestamppb.New(detected)},
		&archivev1.UnhandledEvent{EventType: "REVERSE_SPLIT"},
	))
	if _, err := UnhandledEventPart(context.Background(), database, part, &asOf, rep); err != nil {
		t.Fatalf("UnhandledEventPart: %v", err)
	}
}

// A bad date is a rejected event, and the rest of the group still lands.
func TestUnhandledEventPart_BadExDateIsRejected(t *testing.T) {
	database, rep := newPartTest(t)
	expectFound(database, "inst-1")
	database.EXPECT().RestoreUnhandledCorporateEvents(gomock.Any(), gomock.Len(1)).Return(1, nil)

	part := unhandledEventPart(unhandledEventGroup("XYZ",
		&archivev1.UnhandledEvent{EventType: "MERGER", ExDate: proto.String("not-a-date")},
		&archivev1.UnhandledEvent{EventType: "REVERSE_SPLIT"},
	))
	if _, err := UnhandledEventPart(context.Background(), database, part, nil, rep); err != nil {
		t.Fatalf("UnhandledEventPart: %v", err)
	}
	if rep.ErrCount() != 1 || rep.Errors()[0].GetField() != "ex_date" {
		t.Fatalf("problems = %v", rep.Errors())
	}
}

// An event for an instrument nobody has is not a review anyone can act on.
func TestUnhandledEventPart_UnknownInstrumentIsRejected(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return("", nil)
	database.EXPECT().RestoreUnhandledCorporateEvents(gomock.Any(), gomock.Len(0)).Return(0, nil)

	part := unhandledEventPart(unhandledEventGroup("XYZ",
		&archivev1.UnhandledEvent{EventType: "MERGER"}))
	written, err := UnhandledEventPart(context.Background(), database, part, nil, rep)
	if err != nil {
		t.Fatalf("UnhandledEventPart: %v", err)
	}
	if written != 0 {
		t.Fatalf("written = %d, want 0", written)
	}
	if rep.ErrCount() != 1 || rep.Errors()[0].GetField() != "instrument" {
		t.Fatalf("problems = %v", rep.Errors())
	}
}

func TestUnhandledEventPart_WriteFailureFailsThePart(t *testing.T) {
	database, rep := newPartTest(t)
	expectFound(database, "inst-1")
	database.EXPECT().RestoreUnhandledCorporateEvents(gomock.Any(), gomock.Any()).
		Return(0, errors.New("boom"))

	part := unhandledEventPart(unhandledEventGroup("XYZ",
		&archivev1.UnhandledEvent{EventType: "MERGER"}))
	if _, err := UnhandledEventPart(context.Background(), database, part, nil, rep); err == nil {
		t.Fatal("expected the part to fail")
	}
}

func TestUnhandledEventPart_EmptyPart(t *testing.T) {
	database, rep := newPartTest(t)
	written, err := UnhandledEventPart(context.Background(), database, unhandledEventPart(), nil, rep)
	if err != nil || written != 0 {
		t.Fatalf("UnhandledEventPart = %d, %v", written, err)
	}
}
