package api

import (
	"context"
	"errors"
	"testing"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type mockDB struct {
	db.DB
	getOrCreateUser          func(ctx context.Context, authSub, name, email string) (string, error)
	getUserByAuthSub         func(ctx context.Context, authSub string) (string, error)
	listPortfolios           func(ctx context.Context, userID string, pageSize int32, pageToken string) ([]*apiv1.Portfolio, string, error)
	getPortfolio             func(ctx context.Context, portfolioID string) (*apiv1.Portfolio, string, error)
	createPortfolio          func(ctx context.Context, userID, name string) (*apiv1.Portfolio, error)
	updatePortfolio          func(ctx context.Context, portfolioID, name string) (*apiv1.Portfolio, error)
	deletePortfolio          func(ctx context.Context, portfolioID string) error
	portfolioBelongsToUser   func(ctx context.Context, portfolioID, userID string) (bool, error)
	replaceTxsInPeriod       func(ctx context.Context, portfolioID, broker string, periodFrom, periodTo *timestamppb.Timestamp, txs []*apiv1.Tx) error
	upsertTx                 func(ctx context.Context, portfolioID, broker string, tx *apiv1.Tx) error
	listTxs                  func(ctx context.Context, portfolioID string, broker *apiv1.Broker, periodFrom, periodTo *timestamppb.Timestamp, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error)
	computeHoldings          func(ctx context.Context, portfolioID string, asOf *timestamppb.Timestamp) ([]*apiv1.Holding, *timestamppb.Timestamp, error)
	createJob                func(ctx context.Context, portfolioID, broker string, periodFrom, periodTo *timestamppb.Timestamp) (string, error)
	getJob                   func(ctx context.Context, jobID string) (apiv1.JobStatus, []*apiv1.ValidationError, string, error)
	setJobStatus             func(ctx context.Context, jobID string, status apiv1.JobStatus) error
	appendValidationErrors   func(ctx context.Context, jobID string, errs []*apiv1.ValidationError) error
	listPendingJobIDs        func(ctx context.Context) ([]string, error)
}

func (m *mockDB) GetOrCreateUser(ctx context.Context, authSub, name, email string) (string, error) {
	if m.getOrCreateUser != nil {
		return m.getOrCreateUser(ctx, authSub, name, email)
	}
	return "", nil
}
func (m *mockDB) GetUserByAuthSub(ctx context.Context, authSub string) (string, error) {
	if m.getUserByAuthSub != nil {
		return m.getUserByAuthSub(ctx, authSub)
	}
	return "", nil
}
func (m *mockDB) ListPortfolios(ctx context.Context, userID string, pageSize int32, pageToken string) ([]*apiv1.Portfolio, string, error) {
	if m.listPortfolios != nil {
		return m.listPortfolios(ctx, userID, pageSize, pageToken)
	}
	return nil, "", nil
}
func (m *mockDB) GetPortfolio(ctx context.Context, portfolioID string) (*apiv1.Portfolio, string, error) {
	if m.getPortfolio != nil {
		return m.getPortfolio(ctx, portfolioID)
	}
	return nil, "", nil
}
func (m *mockDB) CreatePortfolio(ctx context.Context, userID, name string) (*apiv1.Portfolio, error) {
	if m.createPortfolio != nil {
		return m.createPortfolio(ctx, userID, name)
	}
	return nil, nil
}
func (m *mockDB) UpdatePortfolio(ctx context.Context, portfolioID, name string) (*apiv1.Portfolio, error) {
	if m.updatePortfolio != nil {
		return m.updatePortfolio(ctx, portfolioID, name)
	}
	return nil, nil
}
func (m *mockDB) DeletePortfolio(ctx context.Context, portfolioID string) error {
	if m.deletePortfolio != nil {
		return m.deletePortfolio(ctx, portfolioID)
	}
	return nil
}
func (m *mockDB) PortfolioBelongsToUser(ctx context.Context, portfolioID, userID string) (bool, error) {
	if m.portfolioBelongsToUser != nil {
		return m.portfolioBelongsToUser(ctx, portfolioID, userID)
	}
	return false, nil
}
func (m *mockDB) ReplaceTxsInPeriod(ctx context.Context, portfolioID, broker string, periodFrom, periodTo *timestamppb.Timestamp, txs []*apiv1.Tx) error {
	if m.replaceTxsInPeriod != nil {
		return m.replaceTxsInPeriod(ctx, portfolioID, broker, periodFrom, periodTo, txs)
	}
	return nil
}
func (m *mockDB) UpsertTx(ctx context.Context, portfolioID, broker string, tx *apiv1.Tx) error {
	if m.upsertTx != nil {
		return m.upsertTx(ctx, portfolioID, broker, tx)
	}
	return nil
}
func (m *mockDB) ListTxs(ctx context.Context, portfolioID string, broker *apiv1.Broker, periodFrom, periodTo *timestamppb.Timestamp, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error) {
	if m.listTxs != nil {
		return m.listTxs(ctx, portfolioID, broker, periodFrom, periodTo, pageSize, pageToken)
	}
	return nil, "", nil
}
func (m *mockDB) ComputeHoldings(ctx context.Context, portfolioID string, asOf *timestamppb.Timestamp) ([]*apiv1.Holding, *timestamppb.Timestamp, error) {
	if m.computeHoldings != nil {
		return m.computeHoldings(ctx, portfolioID, asOf)
	}
	return nil, nil, nil
}
func (m *mockDB) CreateJob(ctx context.Context, portfolioID, broker string, periodFrom, periodTo *timestamppb.Timestamp) (string, error) {
	if m.createJob != nil {
		return m.createJob(ctx, portfolioID, broker, periodFrom, periodTo)
	}
	return "", nil
}
func (m *mockDB) GetJob(ctx context.Context, jobID string) (apiv1.JobStatus, []*apiv1.ValidationError, string, error) {
	if m.getJob != nil {
		return m.getJob(ctx, jobID)
	}
	return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, "", nil
}
func (m *mockDB) SetJobStatus(ctx context.Context, jobID string, status apiv1.JobStatus) error {
	if m.setJobStatus != nil {
		return m.setJobStatus(ctx, jobID, status)
	}
	return nil
}
func (m *mockDB) AppendValidationErrors(ctx context.Context, jobID string, errs []*apiv1.ValidationError) error {
	if m.appendValidationErrors != nil {
		return m.appendValidationErrors(ctx, jobID, errs)
	}
	return nil
}
func (m *mockDB) ListPendingJobIDs(ctx context.Context) ([]string, error) {
	if m.listPendingJobIDs != nil {
		return m.listPendingJobIDs(ctx)
	}
	return nil, nil
}

func TestGetPortfolio_Unauthenticated(t *testing.T) {
	srv := NewServer(&mockDB{})
	ctx := context.Background()
	_, err := srv.GetPortfolio(ctx, &apiv1.GetPortfolioRequest{PortfolioId: "any"})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("got %v", status.Code(err))
	}
}

func TestGetPortfolio_NotFound(t *testing.T) {
	srv := NewServer(&mockDB{
		portfolioBelongsToUser: func(ctx context.Context, portfolioID, userID string) (bool, error) {
			return false, nil
		},
	})
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "user-1"})
	_, err := srv.GetPortfolio(ctx, &apiv1.GetPortfolioRequest{PortfolioId: "port-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.NotFound {
		t.Fatalf("got %v", status.Code(err))
	}
}

func TestCreateUser_Success(t *testing.T) {
	srv := NewServer(&mockDB{
		getOrCreateUser: func(ctx context.Context, authSub, name, email string) (string, error) {
			return "user-123", nil
		},
	})
	ctx := auth.WithUser(context.Background(), &auth.User{AuthSub: "sub|1", Name: "Alice", Email: "a@b.com"})
	resp, err := srv.CreateUser(ctx, &apiv1.CreateUserRequest{AuthSub: "sub|1", Name: "Alice", Email: "a@b.com"})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if resp.GetUserId() != "user-123" {
		t.Fatalf("got user_id %s", resp.GetUserId())
	}
}

func TestCreateUser_DBError(t *testing.T) {
	srv := NewServer(&mockDB{
		getOrCreateUser: func(ctx context.Context, authSub, name, email string) (string, error) {
			return "", errors.New("db error")
		},
	})
	ctx := auth.WithUser(context.Background(), &auth.User{AuthSub: "sub|1"})
	_, err := srv.CreateUser(ctx, &apiv1.CreateUserRequest{AuthSub: "sub|1", Name: "A", Email: "a@b.com"})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.Internal {
		t.Fatalf("got %v", status.Code(err))
	}
}
