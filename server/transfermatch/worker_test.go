package transfermatch

import (
	"context"
	"errors"
	"testing"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"go.uber.org/mock/gomock"
)

// TestRunCycle_WritesThePairsItFinds verifies the cycle passes what it read to the
// matcher and stores the result.
func TestRunCycle_WritesThePairsItFinds(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	mockDB.EXPECT().ListUnmatchedTransferSides(gomock.Any(), db.TransferSideOpts{}).
		Return([]db.TransferSide{
			side("g-cma", "AW10000001", "20000", 0, "971613430"),
			side("g-isa", "AS10000001", "-20000", 0, "971613431"),
		}, nil)
	mockDB.EXPECT().CreateTransferMatches(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, ms []db.TransferMatch) (int, error) {
			if len(ms) != 1 {
				t.Fatalf("wrote %d matches, want 1", len(ms))
			}
			if ms[0].FromGroupID != "g-cma" || ms[0].ToGroupID != "g-isa" {
				t.Errorf("link is %s -> %s, want g-cma -> g-isa", ms[0].FromGroupID, ms[0].ToGroupID)
			}
			if ms[0].Method != db.TransferMatchReference {
				t.Errorf("method = %q, want %q", ms[0].Method, db.TransferMatchReference)
			}
			return len(ms), nil
		})

	runCycle(ctx, mockDB, nil, nil, nil)
}

// TestRunCycle_ReadsEveryUser verifies the pass is whole-corpus rather than scoped to
// the upload that triggered it. A side stored months ago becomes matchable the moment
// its counterpart arrives, and the arriving side need not be in the import being
// processed.
func TestRunCycle_ReadsEveryUser(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)

	mockDB.EXPECT().ListUnmatchedTransferSides(gomock.Any(), db.TransferSideOpts{UserID: ""}).
		Return(nil, nil)

	runCycle(context.Background(), mockDB, nil, nil, nil)
}

// TestRunCycle_WritesNothingWhenNothingPairs verifies a corpus of unmatchable sides
// costs one query and no write. A deposit from outside an account has no counterpart
// and stays unmatched for good, so this is the steady state rather than an edge case.
func TestRunCycle_WritesNothingWhenNothingPairs(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)

	mockDB.EXPECT().ListUnmatchedTransferSides(gomock.Any(), gomock.Any()).
		Return([]db.TransferSide{side("g-isa", "AS10000001", "-19599", 0, "553113055")}, nil)
	// No CreateTransferMatches call is expected: gomock fails the test if one comes.

	runCycle(context.Background(), mockDB, nil, nil, nil)
}

// TestRunCycle_SurvivesAReadError verifies a failing cycle returns rather than
// panicking. Matching is triggered by an import that has already committed, so a
// failure here must not be able to take anything else down with it.
func TestRunCycle_SurvivesAReadError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)

	mockDB.EXPECT().ListUnmatchedTransferSides(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("connection reset"))

	runCycle(context.Background(), mockDB, nil, nil, nil)
}

// TestRunCycle_SurvivesAWriteError verifies the same for the write half.
func TestRunCycle_SurvivesAWriteError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)

	mockDB.EXPECT().ListUnmatchedTransferSides(gomock.Any(), gomock.Any()).
		Return([]db.TransferSide{
			side("g-cma", "AW10000001", "20000", 0, "971613430"),
			side("g-isa", "AS10000001", "-20000", 0, "971613431"),
		}, nil)
	mockDB.EXPECT().CreateTransferMatches(gomock.Any(), gomock.Any()).
		Return(0, errors.New("deadlock detected"))

	runCycle(context.Background(), mockDB, nil, nil, nil)
}
