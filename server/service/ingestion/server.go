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
	"google.golang.org/protobuf/proto"
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
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
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
	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("serialize request: %v", err))
	}
	brokerStr, _ := brokerToString(req.Broker)
	jobID, err := s.db.CreateJob(ctx, db.CreateJobParams{
		UserID:     u.ID,
		JobType:    "tx",
		Broker:     brokerStr,
		Source:     req.GetSource(),
		Filename:   req.GetFilename(),
		PeriodFrom: req.PeriodFrom,
		PeriodTo:   req.PeriodTo,
		Payload:    payload,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	select {
	case s.queue <- &JobRequest{JobID: jobID, JobType: "tx"}:
	default:
		return nil, status.Error(codes.Unavailable, "job queue full")
	}
	return &ingestionv1.IngestionResponse{JobId: jobID}, nil
}

// CreateTx creates a job and enqueues it for async processing.
func (s *Server) CreateTx(ctx context.Context, req *ingestionv1.CreateTxRequest) (*ingestionv1.IngestionResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
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
	// Wrap single tx in UpsertTxsRequest for uniform payload format.
	brokerStr, _ := brokerToString(req.Broker)
	wrapped := &ingestionv1.UpsertTxsRequest{
		Broker:   req.Broker,
		Source:   req.GetSource(),
		Txs:     []*apiv1.Tx{req.Tx},
	}
	payload, err := proto.Marshal(wrapped)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("serialize request: %v", err))
	}
	jobID, err := s.db.CreateJob(ctx, db.CreateJobParams{
		UserID:  u.ID,
		JobType: "tx",
		Broker:  brokerStr,
		Source:  req.GetSource(),
		Payload: payload,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	select {
	case s.queue <- &JobRequest{JobID: jobID, JobType: "tx"}:
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
	case apiv1.Broker_FIDELITY:
		return "Fidelity", nil
	default:
		return "", fmt.Errorf("unknown broker")
	}
}
