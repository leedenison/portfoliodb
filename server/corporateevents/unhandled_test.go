package corporateevents

import (
	"context"

	"github.com/leedenison/portfoliodb/server/db"
)

// unhandledSpy captures the corporate events a run could not apply. It embeds
// NopTelemetry so the rest of the interface stays out of the tests: what a cycle
// refuses is the only thing they examine.
type unhandledSpy struct {
	db.NopTelemetry
	events []db.UnhandledCorporateEvent
}

func (s *unhandledSpy) WriteUnhandledCorporateEvent(_ context.Context, e db.UnhandledCorporateEvent) {
	s.events = append(s.events, e)
}

// spyQueue is a scope writing into a fresh spy. The run id is any non-empty
// value: the scope drops a row with none, and nothing here joins it to a run.
func spyQueue() (Unhandled, *unhandledSpy) {
	s := &unhandledSpy{}
	return Unhandled{DB: s, RunID: "run-1"}, s
}
