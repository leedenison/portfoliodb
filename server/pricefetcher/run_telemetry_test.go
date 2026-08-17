package pricefetcher

import (
	"context"
	"errors"
	"testing"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"go.uber.org/mock/gomock"
)

// TestCycleOpensARun pins the run a trigger opens. The run table is the
// worker-runs chart, so a cycle that found no work still opens one and stamps it
// success -- it ran -- while a cycle whose read failed stamps failed.
func TestCycleOpensARun(t *testing.T) {
	tests := []struct {
		name    string
		readErr error
		want    string
	}{
		{name: "no work", readErr: nil, want: db.TelemetryOutcomeSuccess},
		{name: "read failed", readErr: errors.New("boom"), want: db.TelemetryOutcomeFailed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)
			mockDB := mock.NewMockDB(ctrl)
			tel := mock.NewMockTelemetryDB(ctrl)
			mockDB.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return(nil, tc.readErr)
			mockDB.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			var kind, outcome string
			ended := make(chan struct{})
			tel.EXPECT().StartRun(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, r db.TelemetryRun) string {
					kind = r.Kind
					return "run-1"
				})
			tel.EXPECT().EndRun(gomock.Any(), "run-1", gomock.Any()).
				Do(func(_ context.Context, _, o string) {
					outcome = o
					close(ended)
				})

			trigger := make(chan struct{}, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go RunWorker(ctx, mockDB, NewRegistry(), tel, nil, trigger, nil)
			trigger <- struct{}{}
			<-ended

			if kind != db.TelemetryRunPriceFetchCycle {
				t.Errorf("run kind = %q, want %q", kind, db.TelemetryRunPriceFetchCycle)
			}
			if outcome != tc.want {
				t.Errorf("outcome = %q, want %q", outcome, tc.want)
			}
		})
	}
}
