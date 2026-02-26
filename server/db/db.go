package db

import (
	"context"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// DB is the database abstraction used by the service layer.
type DB interface {
	UserDB
	PortfolioDB
	TxDB
	HoldingsDB
	JobDB
}

// UserDB provides user operations.
type UserDB interface {
	GetOrCreateUser(ctx context.Context, authSub, name, email string) (string, error)
	GetUserByAuthSub(ctx context.Context, authSub string) (string, error)
}

// PortfolioDB provides portfolio CRUD.
type PortfolioDB interface {
	ListPortfolios(ctx context.Context, userID string, pageSize int32, pageToken string) ([]*apiv1.Portfolio, string, error)
	GetPortfolio(ctx context.Context, portfolioID string) (*apiv1.Portfolio, string, error)
	CreatePortfolio(ctx context.Context, userID, name string) (*apiv1.Portfolio, error)
	UpdatePortfolio(ctx context.Context, portfolioID, name string) (*apiv1.Portfolio, error)
	DeletePortfolio(ctx context.Context, portfolioID string) error
	PortfolioBelongsToUser(ctx context.Context, portfolioID, userID string) (bool, error)
}

// TxDB provides transaction write and list.
type TxDB interface {
	ReplaceTxsInPeriod(ctx context.Context, portfolioID, broker string, periodFrom, periodTo *timestamppb.Timestamp, txs []*apiv1.Tx) error
	UpsertTx(ctx context.Context, portfolioID, broker string, tx *apiv1.Tx) error
	ListTxs(ctx context.Context, portfolioID string, broker *apiv1.Broker, periodFrom, periodTo *timestamppb.Timestamp, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error)
}

// HoldingsDB computes holdings at a point in time.
type HoldingsDB interface {
	ComputeHoldings(ctx context.Context, portfolioID string, asOf *timestamppb.Timestamp) ([]*apiv1.Holding, *timestamppb.Timestamp, error)
}

// JobDB provides ingestion job operations.
type JobDB interface {
	CreateJob(ctx context.Context, portfolioID, broker string, periodFrom, periodTo *timestamppb.Timestamp) (string, error)
	GetJob(ctx context.Context, jobID string) (apiv1.JobStatus, []*apiv1.ValidationError, string, error)
	SetJobStatus(ctx context.Context, jobID string, status apiv1.JobStatus) error
	AppendValidationErrors(ctx context.Context, jobID string, errs []*apiv1.ValidationError) error
	ListPendingJobIDs(ctx context.Context) ([]string, error)
}
