package archiveimport

import (
	"context"
	"fmt"
	"time"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// UnhandledEventPart restores an archive's unhandled corporate events and
// reports through rep. It returns how many rows were inserted.
//
// Resolved and unresolved events both come back. The resolved flag is the
// irreplaceable half -- it is the only trace that a person looked at a reverse
// split and decided -- but the rows themselves are created only by a fetch
// detecting something it could not apply, and an import writes events from the
// file rather than fetching them. A resolution with no row to attach to would
// restore nothing, and the queue still waiting for a decision would be lost.
//
// The instrument is looked up and never created, for the same reason fetch
// blocks do it: an event nobody holds the instrument for is not a review anyone
// can act on.
//
// asOf is the envelope's knowledge time and stands in for an event whose file
// does not say when it was detected.
func UnhandledEventPart(ctx context.Context, database db.DB, part *archivev1.UnhandledEventPart, asOf *time.Time, rep *PartReporter) (int32, error) {
	groups := part.GetGroups()
	rep.Total(ctx, len(groups))
	if len(groups) == 0 {
		return 0, nil
	}

	fallback := time.Now()
	if asOf != nil {
		fallback = *asOf
	}

	var events []db.UnhandledCorporateEvent
	for i, g := range groups {
		instrumentID := findArchiveInstrument(ctx, database, g.GetInstrument(), i, rep)
		rep.Advance(ctx, 1)
		if instrumentID == "" {
			continue
		}
		for _, e := range g.GetEvents() {
			row := db.UnhandledCorporateEvent{
				InstrumentID: instrumentID,
				EventType:    e.GetEventType(),
				Detail:       e.GetDetail(),
				Resolved:     e.GetResolved(),
				CreatedAt:    fallback,
			}
			if ts := e.GetDetectedAt(); ts != nil {
				row.CreatedAt = ts.AsTime()
			}
			if e.GetExDate() != "" {
				d, err := time.Parse("2006-01-02", e.GetExDate())
				if err != nil {
					rep.Errf(i, "ex_date", fmt.Sprintf("%s: %v", e.GetEventType(), err))
					continue
				}
				row.ExDate = &d
			}
			if e.GetDataJson() != "" {
				row.Data = []byte(e.GetDataJson())
			}
			events = append(events, row)
		}
	}

	inserted, err := database.RestoreUnhandledCorporateEvents(ctx, events)
	if err != nil {
		return 0, fmt.Errorf("restore unhandled corporate events: %w", err)
	}
	return int32(inserted), nil
}
