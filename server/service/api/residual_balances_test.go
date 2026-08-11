package api

import (
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	dbpkg "github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/testutil"
	"github.com/shopspring/decimal"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
	"time"
)

func TestListResidualBalances_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.ListResidualBalances(authCtx("user-1", "sub|1"), &apiv1.ListResidualBalancesRequest{})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestListResidualBalances_Unauthenticated(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.ListResidualBalances(ctxNoAuth(), &apiv1.ListResidualBalancesRequest{})
	testutil.RequireGRPCCode(t, err, codes.Unauthenticated)
}

func TestListResidualBalances_Empty(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().ListResidualBalances(gomock.Any(), gomock.Any()).Return(nil, nil)

	resp, err := srv.ListResidualBalances(adminCtx("user-1", "sub|1"), &apiv1.ListResidualBalancesRequest{})
	if err != nil {
		t.Fatalf("ListResidualBalances: %v", err)
	}
	if len(resp.GetBalances()) != 0 {
		t.Fatalf("expected 0 balances, got %d", len(resp.GetBalances()))
	}
}

func TestListResidualBalances_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	oldest := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	newest := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	db.EXPECT().ListResidualBalances(gomock.Any(), gomock.Any()).Return([]dbpkg.ResidualBalance{
		{
			AccountType:    typev1.AccountType_ACCOUNT_TYPE_IMBALANCE,
			Broker:         typev1.Broker_FIDELITY,
			Account:        "X123",
			InstrumentID:   "inst-1",
			Commodity:      "USD",
			AssetClass:     "CASH",
			ResolvedTxType: typev1.TxType_INCOME,
			Balance:        decimal.RequireFromString("-1234.56"),
			PostingCount:   7,
			Oldest:         &oldest,
			Newest:         &newest,
		},
		{
			// A transfer with no posting on the outstanding side reports no age.
			AccountType:    typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING,
			Broker:         typev1.Broker_IBKR,
			Account:        "U9",
			InstrumentID:   "inst-2",
			Commodity:      "AAPL",
			AssetClass:     "STOCK",
			ResolvedTxType: typev1.TxType_TRANSFER,
			Balance:        decimal.RequireFromString("50"),
			PostingCount:   1,
		},
	}, nil)

	resp, err := srv.ListResidualBalances(adminCtx("user-1", "sub|1"), &apiv1.ListResidualBalancesRequest{})
	if err != nil {
		t.Fatalf("ListResidualBalances: %v", err)
	}
	if len(resp.GetBalances()) != 2 {
		t.Fatalf("expected 2 balances, got %d", len(resp.GetBalances()))
	}

	got := resp.GetBalances()[0]
	if got.GetAccountType() != typev1.AccountType_ACCOUNT_TYPE_IMBALANCE {
		t.Errorf("account type = %v, want IMBALANCE", got.GetAccountType())
	}
	if got.GetBroker() != typev1.Broker_FIDELITY {
		t.Errorf("broker = %v, want FIDELITY", got.GetBroker())
	}
	if got.GetAccount() != "X123" || got.GetInstrumentId() != "inst-1" {
		t.Errorf("account/instrument = %q/%q, want X123/inst-1", got.GetAccount(), got.GetInstrumentId())
	}
	if got.GetCommodity() != "USD" {
		t.Errorf("commodity = %q, want USD", got.GetCommodity())
	}
	if got.GetAssetClass() != typev1.AssetClass_CASH {
		t.Errorf("asset class = %v, want CASH", got.GetAssetClass())
	}
	if got.GetResolvedTxType() != typev1.TxType_INCOME {
		t.Errorf("resolved tx type = %v, want INCOME", got.GetResolvedTxType())
	}
	if got.GetBalance() != "-1234.56" {
		t.Errorf("balance = %v, want -1234.56", got.GetBalance())
	}
	if got.GetPostingCount() != 7 {
		t.Errorf("posting count = %d, want 7", got.GetPostingCount())
	}
	if !got.GetOldestTimestamp().AsTime().Equal(oldest) {
		t.Errorf("oldest = %v, want %v", got.GetOldestTimestamp().AsTime(), oldest)
	}
	if !got.GetNewestTimestamp().AsTime().Equal(newest) {
		t.Errorf("newest = %v, want %v", got.GetNewestTimestamp().AsTime(), newest)
	}

	second := resp.GetBalances()[1]
	if second.GetOldestTimestamp() != nil || second.GetNewestTimestamp() != nil {
		t.Errorf("expected unset timestamps for a balance with none, got %v/%v",
			second.GetOldestTimestamp(), second.GetNewestTimestamp())
	}
	if second.GetAssetClass() != typev1.AssetClass_STOCK {
		t.Errorf("asset class = %v, want STOCK (a quantity, not money)", second.GetAssetClass())
	}
}

// TestListResidualBalances_Filters verifies the request bounds and account type
// reach the db layer, since a filter silently dropped here would show a report of
// the wrong period without saying so.
func TestListResidualBalances_Filters(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	var got dbpkg.ResidualBalanceOpts
	db.EXPECT().ListResidualBalances(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, opts dbpkg.ResidualBalanceOpts) ([]dbpkg.ResidualBalance, error) {
			got = opts
			return nil, nil
		})

	_, err := srv.ListResidualBalances(adminCtx("user-1", "sub|1"), &apiv1.ListResidualBalancesRequest{
		PeriodFrom:   timestamppb.New(from),
		PeriodBefore: timestamppb.New(before),
		AccountType:  typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING,
	})
	if err != nil {
		t.Fatalf("ListResidualBalances: %v", err)
	}
	if got.From == nil || !got.From.Equal(from) {
		t.Errorf("from = %v, want %v", got.From, from)
	}
	if got.Before == nil || !got.Before.Equal(before) {
		t.Errorf("before = %v, want %v", got.Before, before)
	}
	if got.AccountType != typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING {
		t.Errorf("account type = %v, want TRANSFER_CLEARING", got.AccountType)
	}
}

// TestListResidualBalances_UnsetPeriodIsOpen verifies an omitted bound is open
// rather than becoming the zero time, which would filter everything out.
func TestListResidualBalances_UnsetPeriodIsOpen(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	var got dbpkg.ResidualBalanceOpts
	db.EXPECT().ListResidualBalances(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, opts dbpkg.ResidualBalanceOpts) ([]dbpkg.ResidualBalance, error) {
			got = opts
			return nil, nil
		})

	if _, err := srv.ListResidualBalances(adminCtx("user-1", "sub|1"), &apiv1.ListResidualBalancesRequest{}); err != nil {
		t.Fatalf("ListResidualBalances: %v", err)
	}
	if got.From != nil || got.Before != nil {
		t.Errorf("expected open bounds, got from=%v before=%v", got.From, got.Before)
	}
}

func TestCountResidualBalances_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.CountResidualBalances(authCtx("user-1", "sub|1"), &apiv1.CountResidualBalancesRequest{})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestCountResidualBalances_Unauthenticated(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.CountResidualBalances(ctxNoAuth(), &apiv1.CountResidualBalancesRequest{})
	testutil.RequireGRPCCode(t, err, codes.Unauthenticated)
}

func TestCountResidualBalances_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().CountResidualBalances(gomock.Any(), gomock.Any()).Return(int32(3), int32(1), nil)

	resp, err := srv.CountResidualBalances(adminCtx("user-1", "sub|1"), &apiv1.CountResidualBalancesRequest{})
	if err != nil {
		t.Fatalf("CountResidualBalances: %v", err)
	}
	if resp.GetImbalanceCount() != 3 {
		t.Errorf("imbalance count = %d, want 3", resp.GetImbalanceCount())
	}
	if resp.GetStaleTransferCount() != 1 {
		t.Errorf("stale transfer count = %d, want 1", resp.GetStaleTransferCount())
	}
}

// TestCountResidualBalances_StaleWindow verifies the request's stale window is
// applied, and that omitting it selects the default rather than counting every open
// transfer as stale.
func TestCountResidualBalances_StaleWindow(t *testing.T) {
	cases := []struct {
		name     string
		days     int32
		wantDays int
	}{
		{"default", 0, defaultStaleTransferDays},
		{"explicit", 30, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := newAPIServerWithMock(t)
			var got time.Time
			db.EXPECT().CountResidualBalances(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ any, staleBefore time.Time) (int32, int32, error) {
					got = staleBefore
					return 0, 0, nil
				})

			before := time.Now().UTC()
			if _, err := srv.CountResidualBalances(adminCtx("user-1", "sub|1"),
				&apiv1.CountResidualBalancesRequest{StaleAfterDays: tc.days}); err != nil {
				t.Fatalf("CountResidualBalances: %v", err)
			}
			// A window rather than an equality: the handler reads the clock itself.
			want := before.AddDate(0, 0, -tc.wantDays)
			if got.Before(want.Add(-time.Minute)) || got.After(want.Add(time.Minute)) {
				t.Errorf("staleBefore = %v, want ~%v (%d days back)", got, want, tc.wantDays)
			}
		})
	}
}

func TestTriggerTransferMatch_RequiresAdmin(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|user")
	_, err := srv.TriggerTransferMatch(ctx, &apiv1.TriggerTransferMatchRequest{})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestTriggerTransferMatch_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	mockDB := mock.NewMockDB(ctrl)
	trigger := make(chan struct{}, 1)
	srv := NewServer(ServerConfig{DB: mockDB, TransferMatchTrigger: trigger})

	ctx := adminCtx("admin-1", "sub|admin")
	if _, err := srv.TriggerTransferMatch(ctx, &apiv1.TriggerTransferMatchRequest{}); err != nil {
		t.Fatalf("TriggerTransferMatch: %v", err)
	}
	select {
	case <-trigger:
	default:
		t.Error("expected a signal on the trigger channel")
	}
}

// TestTriggerTransferMatch_NonBlocking verifies a cron job calling this while a pass
// is already pending returns rather than waiting. The channel is buffered to one, so
// a burst of calls collapses into a single cycle.
func TestTriggerTransferMatch_NonBlocking(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	mockDB := mock.NewMockDB(ctrl)
	trigger := make(chan struct{}, 1)
	trigger <- struct{}{}
	srv := NewServer(ServerConfig{DB: mockDB, TransferMatchTrigger: trigger})

	ctx := adminCtx("admin-1", "sub|admin")
	if _, err := srv.TriggerTransferMatch(ctx, &apiv1.TriggerTransferMatchRequest{}); err != nil {
		t.Fatalf("TriggerTransferMatch should not block: %v", err)
	}
}

// TestTriggerTransferMatch_NilTrigger verifies the RPC is a no-op rather than an
// error where no worker is wired, matching the other trigger endpoints.
func TestTriggerTransferMatch_NilTrigger(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := adminCtx("admin-1", "sub|admin")
	if _, err := srv.TriggerTransferMatch(ctx, &apiv1.TriggerTransferMatchRequest{}); err != nil {
		t.Fatalf("TriggerTransferMatch with nil trigger should succeed: %v", err)
	}
}

func TestTriggerGrouping_RequiresAdmin(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|user")
	_, err := srv.TriggerGrouping(ctx, &apiv1.TriggerGroupingRequest{})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestTriggerGrouping_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	mockDB := mock.NewMockDB(ctrl)
	trigger := make(chan struct{}, 1)
	srv := NewServer(ServerConfig{DB: mockDB, GroupingTrigger: trigger})

	ctx := adminCtx("admin-1", "sub|admin")
	if _, err := srv.TriggerGrouping(ctx, &apiv1.TriggerGroupingRequest{}); err != nil {
		t.Fatalf("TriggerGrouping: %v", err)
	}
	select {
	case <-trigger:
	default:
		t.Error("expected a signal on the trigger channel")
	}
}

// TestTriggerGrouping_NonBlocking verifies a cron job calling this while a pass is
// already pending returns rather than waiting, as the other trigger endpoints do.
func TestTriggerGrouping_NonBlocking(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	mockDB := mock.NewMockDB(ctrl)
	trigger := make(chan struct{}, 1)
	trigger <- struct{}{}
	srv := NewServer(ServerConfig{DB: mockDB, GroupingTrigger: trigger})

	ctx := adminCtx("admin-1", "sub|admin")
	if _, err := srv.TriggerGrouping(ctx, &apiv1.TriggerGroupingRequest{}); err != nil {
		t.Fatalf("TriggerGrouping should not block: %v", err)
	}
}

// TestTriggerGrouping_NilTrigger verifies the RPC is a no-op rather than an error
// where no worker is wired.
func TestTriggerGrouping_NilTrigger(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := adminCtx("admin-1", "sub|admin")
	if _, err := srv.TriggerGrouping(ctx, &apiv1.TriggerGroupingRequest{}); err != nil {
		t.Fatalf("TriggerGrouping with nil trigger should succeed: %v", err)
	}
}
