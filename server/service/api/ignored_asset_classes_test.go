package api

import (
	"errors"
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

// rule is the wire shape of one ignore rule.
func rule(broker, account string, class typev1.AssetClass) *apiv1.IgnoredAssetClassRule {
	return &apiv1.IgnoredAssetClassRule{Broker: broker, Account: account, AssetClass: class}
}

// An empty account means every account of that broker, so it survives the round
// trip rather than being filled in with something.
func TestGetIgnoredAssetClasses(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListIgnoredAssetClasses(gomock.Any(), "user-1").Return([]db.IgnoredAssetClass{
		{Broker: "IBKR", Account: "U1000001", AssetClass: "OPTION"},
		{Broker: "FIDELITY", Account: "", AssetClass: "CASH"},
	}, nil)

	resp, err := srv.GetIgnoredAssetClasses(authCtx("user-1", "sub|1"), &apiv1.GetIgnoredAssetClassesRequest{})
	if err != nil {
		t.Fatalf("GetIgnoredAssetClasses: %v", err)
	}
	want := []*apiv1.IgnoredAssetClassRule{
		rule("IBKR", "U1000001", typev1.AssetClass_OPTION),
		rule("FIDELITY", "", typev1.AssetClass_CASH),
	}
	got := resp.GetRules()
	if len(got) != len(want) {
		t.Fatalf("rules: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].GetBroker() != want[i].GetBroker() ||
			got[i].GetAccount() != want[i].GetAccount() ||
			got[i].GetAssetClass() != want[i].GetAssetClass() {
			t.Errorf("rule %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestGetIgnoredAssetClasses_StoreError(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListIgnoredAssetClasses(gomock.Any(), "user-1").Return(nil, errors.New("boom"))

	_, err := srv.GetIgnoredAssetClasses(authCtx("user-1", "sub|1"), &apiv1.GetIgnoredAssetClassesRequest{})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

// Set replaces the whole set, so what reaches the store is the request and nothing
// merged into it.
func TestSetIgnoredAssetClasses(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().SetIgnoredAssetClasses(gomock.Any(), "user-1", []db.IgnoredAssetClass{
		{Broker: "IBKR", Account: "U1000001", AssetClass: "OPTION"},
	}).Return(nil)

	_, err := srv.SetIgnoredAssetClasses(authCtx("user-1", "sub|1"), &apiv1.SetIgnoredAssetClassesRequest{
		Rules: []*apiv1.IgnoredAssetClassRule{rule("IBKR", "U1000001", typev1.AssetClass_OPTION)},
	})
	if err != nil {
		t.Fatalf("SetIgnoredAssetClasses: %v", err)
	}
}

// An empty set is a valid instruction -- it clears the rules -- rather than a
// request with something missing from it.
func TestSetIgnoredAssetClasses_ClearsWithNoRules(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().SetIgnoredAssetClasses(gomock.Any(), "user-1", []db.IgnoredAssetClass{}).Return(nil)

	_, err := srv.SetIgnoredAssetClasses(authCtx("user-1", "sub|1"), &apiv1.SetIgnoredAssetClassesRequest{})
	if err != nil {
		t.Fatalf("SetIgnoredAssetClasses: %v", err)
	}
}

// A rule naming no broker is rejected before the store is asked, because a rule
// that matched every broker is not what an unset field means.
func TestSetIgnoredAssetClasses_RejectsRuleWithNoBroker(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.SetIgnoredAssetClasses(authCtx("user-1", "sub|1"), &apiv1.SetIgnoredAssetClassesRequest{
		Rules: []*apiv1.IgnoredAssetClassRule{rule("", "", typev1.AssetClass_OPTION)},
	})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

// Count answers the question the confirmation dialog asks before a set: how much
// would this destroy. Declarations are counted separately from transactions,
// because they are a separate thing to lose.
func TestCountIgnoredTxs(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().CountIgnoredTxs(gomock.Any(), "user-1", []db.IgnoredAssetClass{
		{Broker: "IBKR", Account: "", AssetClass: "OPTION"},
	}).Return(int32(12), int32(3), nil)

	resp, err := srv.CountIgnoredTxs(authCtx("user-1", "sub|1"), &apiv1.CountIgnoredTxsRequest{
		Rules: []*apiv1.IgnoredAssetClassRule{rule("IBKR", "", typev1.AssetClass_OPTION)},
	})
	if err != nil {
		t.Fatalf("CountIgnoredTxs: %v", err)
	}
	if resp.GetTxCount() != 12 || resp.GetDeclarationCount() != 3 {
		t.Errorf("counts: got txs=%d declarations=%d, want 12 and 3", resp.GetTxCount(), resp.GetDeclarationCount())
	}
}

func TestCountIgnoredTxs_StoreError(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().CountIgnoredTxs(gomock.Any(), "user-1", gomock.Any()).Return(int32(0), int32(0), errors.New("boom"))

	_, err := srv.CountIgnoredTxs(authCtx("user-1", "sub|1"), &apiv1.CountIgnoredTxsRequest{})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}
