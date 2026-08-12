package grouping

import (
	"context"
	"errors"
	"testing"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/txtype"
	"go.uber.org/mock/gomock"
)

// seeded puts a posting in a user and a stored group, which is the shape a seed read
// returns.
//
// It carries a resolved value too, because a stored posting has one -- and because a
// cycle that agreed about membership but found none would write the retype, which is
// not what these tests are about.
func seeded(p db.GroupingPosting, userID, groupID string) db.GroupingPosting {
	p.UserID = userID
	p.GroupID = groupID
	p.Resolved = txtype.Resolve(p.Declared).String()
	return p
}

// TestRunCycle_SeedsFromResidualGroups verifies the cadence cycle starts from the
// groups holding something a missing leg would explain, rather than from everything.
func TestRunCycle_SeedsFromResidualGroups(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)

	mockDB.EXPECT().ListGroupingSeeds(gomock.Any(), db.GroupingSeedOpts{Residual: true}).
		Return(nil, nil)

	runCycle(context.Background(), mockDB, nil, nil, nil)
}

// A cycle that repartitions nothing writes nothing. The two postings here already
// share a group, so the engine draws what is stored and Diff produces no change --
// which is what keeps a group's id, and the transfer matches keyed on it, through a
// cycle over a region far wider than any upload.
//
// gomock fails an unexpected call, so naming only the reads is what asserts it.
func TestRunCycle_WritesNothingWhenItAgrees(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)

	a := seeded(correlated(posting("a", typev1.TxType_TRADE_ASSET), "", "t1", db.ScopeAccount, db.MatchExact), "u1", "g1")
	b := seeded(correlated(posting("b", typev1.TxType_TRADE_CASH), "", "t1", db.ScopeAccount, db.MatchExact), "u1", "g1")

	mockDB.EXPECT().ListGroupingSeeds(gomock.Any(), gomock.Any()).
		Return([]db.GroupingPosting{a}, nil)
	mockDB.EXPECT().PostingsByToken(gomock.Any(), "u1", gomock.Any(), gomock.Any()).
		Return([]db.GroupingPosting{b}, nil).AnyTimes()
	mockDB.EXPECT().PostingsByDates(gomock.Any(), "u1", gomock.Any(), gomock.Any()).
		Return(nil, nil).AnyTimes()
	mockDB.EXPECT().PostingsByOrdinals(gomock.Any(), "u1", gomock.Any(), gomock.Any()).
		Return(nil, nil).AnyTimes()

	runCycle(context.Background(), mockDB, nil, nil, nil)
}

// TestRunCycle_PartitionsEachUsersOwnData verifies a neighbourhood never crosses a
// user. The reads take a user id, and a cycle that mixed two would ask one user's
// questions of another's data.
func TestRunCycle_PartitionsEachUsersOwnData(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)

	one := seeded(posting("a", typev1.TxType_TRADE_ASSET), "u1", "g1")
	two := seeded(posting("b", typev1.TxType_TRADE_ASSET), "u2", "g2")
	one.SettlementAmount, two.SettlementAmount = decp("100"), decp("100")

	mockDB.EXPECT().ListGroupingSeeds(gomock.Any(), gomock.Any()).
		Return([]db.GroupingPosting{one, two}, nil)
	seen := map[string]bool{}
	mockDB.EXPECT().PostingsByDates(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, userID string, _ []db.DateQuery, _ []string) ([]db.GroupingPosting, error) {
			seen[userID] = true
			return nil, nil
		}).AnyTimes()
	mockDB.EXPECT().PostingsByToken(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, nil).AnyTimes()
	mockDB.EXPECT().PostingsByOrdinals(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, nil).AnyTimes()

	runCycle(context.Background(), mockDB, nil, nil, nil)

	if !seen["u1"] || !seen["u2"] {
		t.Fatalf("read for users %v, want both u1 and u2", seen)
	}
}

// TestRunCycle_SurvivesAReadError verifies a failure ends the cycle rather than the
// process. The next trigger runs it again.
func TestRunCycle_SurvivesAReadError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)

	mockDB.EXPECT().ListGroupingSeeds(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("read failed"))

	runCycle(context.Background(), mockDB, nil, nil, nil)
}

// TestRunCycle_SurvivesOneUsersFailure verifies a neighbourhood that cannot be read
// costs that user's result rather than the whole cycle.
func TestRunCycle_SurvivesOneUsersFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)

	one := seeded(posting("a", typev1.TxType_TRADE_ASSET), "u1", "g1")
	two := seeded(posting("b", typev1.TxType_TRADE_ASSET), "u2", "g2")

	mockDB.EXPECT().ListGroupingSeeds(gomock.Any(), gomock.Any()).
		Return([]db.GroupingPosting{one, two}, nil)
	mockDB.EXPECT().PostingsByToken(gomock.Any(), "u1", gomock.Any(), gomock.Any()).
		Return(nil, errors.New("read failed")).AnyTimes()
	mockDB.EXPECT().PostingsByToken(gomock.Any(), "u2", gomock.Any(), gomock.Any()).
		Return(nil, nil).AnyTimes()
	mockDB.EXPECT().PostingsByDates(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, nil).AnyTimes()
	mockDB.EXPECT().PostingsByOrdinals(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, nil).AnyTimes()

	runCycle(context.Background(), mockDB, nil, nil, nil)
}
