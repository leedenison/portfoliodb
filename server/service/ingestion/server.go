package ingestion

import (
	"context"
	"fmt"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	ingestionv1 "github.com/leedenison/portfoliodb/proto/ingestion/v1"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements IngestionService.
type Server struct {
	ingestionv1.UnimplementedIngestionServiceServer
	db    db.DB
	queue chan<- *JobRequest
}

// NewServer returns a new ingestion server that enqueues jobs to queue.
func NewServer(database db.DB, queue chan<- *JobRequest) *Server {
	return &Server{db: database, queue: queue}
}

// UpsertTxs creates a job and enqueues it for async processing.
func (s *Server) UpsertTxs(ctx context.Context, req *ingestionv1.UpsertTxsRequest) (*ingestionv1.IngestionResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if err := ValidateBroker(req.Broker); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Message)
	}
	if err := ValidateSource(req.GetSource()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Message)
	}
	periodErrs := ValidateBulkRequest(req.PeriodFrom, req.PeriodTo)
	if len(periodErrs) > 0 {
		return nil, status.Error(codes.InvalidArgument, periodErrs[0].Message)
	}
	brokerStr, _ := brokerToString(req.Broker)
	jobID, err := s.db.CreateJob(ctx, u.ID, brokerStr, req.GetSource(), req.PeriodFrom, req.PeriodTo)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	txs := req.GetTxs()
	if txs == nil {
		txs = []*apiv1.Tx{}
	}
	select {
	case s.queue <- &JobRequest{
		JobID:      jobID,
		UserID:     u.ID,
		Broker:     brokerStr,
		Source:     req.GetSource(),
		Bulk:       true,
		PeriodFrom: req.PeriodFrom,
		PeriodTo:   req.PeriodTo,
		Txs:        txs,
	}:
	default:
		return nil, status.Error(codes.Unavailable, "job queue full")
	}
	return &ingestionv1.IngestionResponse{JobId: jobID}, nil
}

// CreateTx creates a job and enqueues it for async processing.
func (s *Server) CreateTx(ctx context.Context, req *ingestionv1.CreateTxRequest) (*ingestionv1.IngestionResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if err := ValidateBroker(req.Broker); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Message)
	}
	if err := ValidateSource(req.GetSource()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Message)
	}
	if req.Tx == nil {
		return nil, status.Error(codes.InvalidArgument, "tx required")
	}
	brokerStr, _ := brokerToString(req.Broker)
	jobID, err := s.db.CreateJob(ctx, u.ID, brokerStr, req.GetSource(), nil, nil)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	select {
	case s.queue <- &JobRequest{
		JobID:  jobID,
		UserID: u.ID,
		Broker: brokerStr,
		Source: req.GetSource(),
		Bulk:   false,
		Tx:     req.Tx,
	}:
	default:
		return nil, status.Error(codes.Unavailable, "job queue full")
	}
	return &ingestionv1.IngestionResponse{JobId: jobID}, nil
}

func brokerToString(b apiv1.Broker) (string, error) {
	switch b {
	case apiv1.Broker_IBKR:
		return "IBKR", nil
	case apiv1.Broker_SCHB:
		return "SCHB", nil
	default:
		return "", fmt.Errorf("unknown broker")
	}
}
