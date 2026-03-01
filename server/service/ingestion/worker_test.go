package ingestion

import (
	"context"
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestProcessBulk_AppendsIdentificationErrorsWhenBrokerDescriptionOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry() // no plugins

	ctx := context.Background()
	from := timestamppb.Now()
	to := timestamppb.Now()
	txs := []*apiv1.Tx{
		{Timestamp: from, InstrumentDescription: "UNKNOWN", Type: apiv1.TxType_BUYSTOCK, Quantity: 1},
	}
	j := &JobRequest{
		JobID:       "job-1",
		PortfolioID: "port-1",
		Broker:      "IBKR",
		Source:      "IBKR:test:statement",
		Bulk:        true,
		PeriodFrom:  from,
		PeriodTo:    to,
		Txs:         txs,
	}

	// processBulk: SetJobStatus RUNNING already done in processJob
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-1", apiv1.JobStatus_RUNNING).
		Return(nil)
	// Resolve for "UNKNOWN": DB miss, no plugins, EnsureInstrument broker-only
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "IBKR:test:statement", "UNKNOWN").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any()).
		Return(nil, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "UNKNOWN", []db.IdentifierInput{{Type: "IBKR:test:statement", Value: "UNKNOWN", Canonical: false}}, "", nil, nil).
		Return("broker-only-id", nil)
	database.EXPECT().
		AppendIdentificationErrors(gomock.Any(), "job-1", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, errs []db.IdentificationError) error {
			if len(errs) != 1 {
				t.Errorf("expected 1 identification error, got %d", len(errs))
				return nil
			}
			if errs[0].Message != MsgBrokerDescriptionOnly {
				t.Errorf("identification error message = %q, want %q", errs[0].Message, MsgBrokerDescriptionOnly)
			}
			if errs[0].InstrumentDescription != "UNKNOWN" {
				t.Errorf("instrument description = %q, want UNKNOWN", errs[0].InstrumentDescription)
			}
			return nil
		})
	database.EXPECT().
		ReplaceTxsInPeriod(gomock.Any(), "port-1", "IBKR", from, to, txs, []string{"broker-only-id"}).
		Return(nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-1", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, database, registry, j)
}

func TestProcessBulk_BatchCache_ResolvesSameDescriptionOnce(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	to := timestamppb.Now()
	// Two txs with same instrument description - should resolve once and use cache for second
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(from.AsTime().Add(-1)), InstrumentDescription: "CACHED", Type: apiv1.TxType_BUYSTOCK, Quantity: 1},
		{Timestamp: timestamppb.New(from.AsTime().Add(1)), InstrumentDescription: "CACHED", Type: apiv1.TxType_BUYSTOCK, Quantity: 2},
	}
	j := &JobRequest{
		JobID:       "job-2",
		PortfolioID: "port-1",
		Broker:      "IBKR",
		Source:      "IBKR:test:statement",
		Bulk:        true,
		PeriodFrom:  from,
		PeriodTo:    to,
		Txs:         txs,
	}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-2", apiv1.JobStatus_RUNNING).
		Return(nil)
	// First resolve: DB miss, no plugins, EnsureInstrument
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "IBKR:test:statement", "CACHED").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any()).
		Return(nil, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "CACHED", []db.IdentifierInput{{Type: "IBKR:test:statement", Value: "CACHED", Canonical: false}}, "", nil, nil).
		Return("cached-inst-id", nil)
	// Second tx hits cache - no additional FindInstrumentByIdentifier or ListEnabledPluginConfigs
	database.EXPECT().
		AppendIdentificationErrors(gomock.Any(), "job-2", gomock.Any()).
		Return(nil)
	database.EXPECT().
		ReplaceTxsInPeriod(gomock.Any(), "port-1", "IBKR", from, to, txs, []string{"cached-inst-id", "cached-inst-id"}).
		Return(nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-2", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, database, registry, j)
}
