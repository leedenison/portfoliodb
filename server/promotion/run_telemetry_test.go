package promotion

import (
	"context"
	"errors"
	"testing"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"go.uber.org/mock/gomock"
)

// TestCycleOpensARun pins the run a trigger opens. The run table is the
// worker-runs chart, so a cycle that promoted nothing still opens one and stamps
// it success -- it ran -- while a cycle that could not read stamps failed.
//
// A threshold it cannot use fails the cycle rather than defaulting to one.
// Substituting a number an admin did not choose would promote mappings on terms
// nobody set, and a sweep that refuses to run is visible where one that quietly
// picked its own threshold is not.
func TestCycleOpensARun(t *testing.T) {
	tests := []struct {
		name         string
		thresholdErr error
		promoteErr   error
		want         string
	}{
		{name: "nothing to promote", want: db.TelemetryOutcomeSuccess},
		{name: "no usable threshold", thresholdErr: errors.New("boom"), want: db.TelemetryOutcomeFailed},
		{name: "promotion failed", promoteErr: errors.New("boom"), want: db.TelemetryOutcomeFailed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)
			mockDB := mock.NewMockDB(ctrl)
			tel := mock.NewMockTelemetryDB(ctrl)
			mockDB.EXPECT().PromotionThreshold(gomock.Any()).Return(2, tc.thresholdErr)
			if tc.thresholdErr == nil {
				mockDB.EXPECT().PromoteCorroboratedIdentifiers(gomock.Any(), 2).
					Return(db.PromotionResult{}, tc.promoteErr)
			}
			if tc.thresholdErr == nil && tc.promoteErr == nil {
				mockDB.EXPECT().CountUnpromotableClaims(gomock.Any()).Return(0, nil)
			}

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
			go RunWorker(ctx, mockDB, tel, nil, trigger, nil)
			trigger <- struct{}{}
			<-ended

			if kind != db.TelemetryRunPromotionCycle {
				t.Errorf("run kind = %q, want %q", kind, db.TelemetryRunPromotionCycle)
			}
			if outcome != tc.want {
				t.Errorf("outcome = %q, want %q", outcome, tc.want)
			}
		})
	}
}

// The threshold is read on every cycle rather than held from startup, so an
// admin lowering it takes effect on the next sweep rather than on the next
// deployment -- which is the case it is most worth changing for: every mapping
// one user short of the old number becomes promotable the moment it changes.
func TestCycleReadsTheThresholdEveryTime(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockDB := mock.NewMockDB(ctrl)
	tel := mock.NewMockTelemetryDB(ctrl)

	thresholds := []int{3, 1}
	var used []int
	gomock.InOrder(
		mockDB.EXPECT().PromotionThreshold(gomock.Any()).Return(thresholds[0], nil),
		mockDB.EXPECT().PromotionThreshold(gomock.Any()).Return(thresholds[1], nil),
	)
	mockDB.EXPECT().PromoteCorroboratedIdentifiers(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, n int) (db.PromotionResult, error) {
			used = append(used, n)
			return db.PromotionResult{}, nil
		}).Times(2)
	mockDB.EXPECT().CountUnpromotableClaims(gomock.Any()).Return(0, nil).Times(2)

	tel.EXPECT().StartRun(gomock.Any(), gomock.Any()).Return("run-1").Times(2)
	ended := make(chan struct{}, 2)
	tel.EXPECT().EndRun(gomock.Any(), "run-1", gomock.Any()).
		Do(func(_ context.Context, _, _ string) { ended <- struct{}{} }).Times(2)

	trigger := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go RunWorker(ctx, mockDB, tel, nil, trigger, nil)
	for range 2 {
		trigger <- struct{}{}
		<-ended
	}

	if len(used) != 2 || used[0] != thresholds[0] || used[1] != thresholds[1] {
		t.Errorf("swept at %v, want %v", used, thresholds)
	}
}
