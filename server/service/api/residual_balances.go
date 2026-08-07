package api

import (
	"context"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// defaultStaleTransferDays is how long a journal side may wait for its pair before
// it is worth raising. Brokers report the two sides on separate statements, so a
// balance is expected immediately after an import and only means something once it
// persists.
const defaultStaleTransferDays = 7

// ListResidualBalances returns the residual balances left in non-asset accounts.
// Admin only: it measures how lossy each broker converter is, not what is in any one
// portfolio.
func (s *Server) ListResidualBalances(ctx context.Context, req *apiv1.ListResidualBalancesRequest) (*apiv1.ListResidualBalancesResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}

	opts := db.ResidualBalanceOpts{
		From:        tsToTimePtr(req.GetPeriodFrom()),
		Before:      tsToTimePtr(req.GetPeriodBefore()),
		AccountType: req.GetAccountType(),
	}
	balances, err := s.db.ListResidualBalances(ctx, opts)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list residual balances: %v", err)
	}

	out := make([]*apiv1.ResidualBalance, len(balances))
	for i, b := range balances {
		out[i] = &apiv1.ResidualBalance{
			AccountType:     b.AccountType,
			Broker:          b.Broker,
			Account:         b.Account,
			InstrumentId:    b.InstrumentID,
			Commodity:       b.Commodity,
			AssetClass:      db.StrToAssetClass(b.AssetClass),
			TxType:          b.TxType,
			Balance:         decStr(b.Balance),
			PostingCount:    b.PostingCount,
			OldestTimestamp: timePtrToTs(b.Oldest),
			NewestTimestamp: timePtrToTs(b.Newest),
		}
	}
	return &apiv1.ListResidualBalancesResponse{Balances: out}, nil
}

// CountResidualBalances returns the dashboard headline counts. Admin only.
func (s *Server) CountResidualBalances(ctx context.Context, req *apiv1.CountResidualBalancesRequest) (*apiv1.CountResidualBalancesResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}

	days := req.GetStaleAfterDays()
	if days <= 0 {
		days = defaultStaleTransferDays
	}
	// The db method takes an instant rather than a duration so that what counts as
	// stale is decided in one place and its tests are not clock-dependent.
	staleBefore := time.Now().UTC().AddDate(0, 0, -int(days))

	imbalances, staleTransfers, err := s.db.CountResidualBalances(ctx, staleBefore)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "count residual balances: %v", err)
	}
	return &apiv1.CountResidualBalancesResponse{
		ImbalanceCount:     imbalances,
		StaleTransferCount: staleTransfers,
	}, nil
}

func tsToTimePtr(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil || !ts.IsValid() {
		return nil
	}
	t := ts.AsTime()
	return &t
}

func timePtrToTs(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

// TriggerTransferMatch signals the transfer matcher to run a cycle. Admin only.
// Returns immediately; the pass runs asynchronously.
//
// This is the endpoint an external cron job or CLI calls, and it is how matching is
// expected to run on a cadence. The ingestion worker fires the same trigger after a
// tx import, so a pair whose second side has just landed is matched without waiting
// for the next tick; the two are the same channel, buffered to one, so a burst of
// either collapses into a single pass.
//
// Calling it more often than needed is cheap. A cycle reads every side no match
// names, bounded by the partial index over residual postings, so a corpus with
// nothing outstanding costs one query and writes nothing.
func (s *Server) TriggerTransferMatch(ctx context.Context, req *apiv1.TriggerTransferMatchRequest) (*apiv1.TriggerTransferMatchResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	if s.transferMatchTrigger != nil {
		select {
		case s.transferMatchTrigger <- struct{}{}:
		default:
		}
	}
	return &apiv1.TriggerTransferMatchResponse{}, nil
}
