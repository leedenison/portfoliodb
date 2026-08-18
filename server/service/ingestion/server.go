package ingestion

import (
	"context"
	"fmt"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	ingestionv1 "github.com/leedenison/portfoliodb/proto/ingestion/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
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
func (s *Server) UpsertTxs(ctx context.Context, req *ingestionv1.UpsertTxsRequest) (*ingestionv1.UpsertTxsResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	w := req.GetWindow()
	if err := ValidateBroker(w.GetBroker()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Message)
	}
	if err := ValidateSource(w.GetSource()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Message)
	}
	periodErrs := ValidateBulkRequest(w.GetPeriodFrom(), w.GetPeriodBefore())
	if len(periodErrs) > 0 {
		return nil, status.Error(codes.InvalidArgument, periodErrs[0].Message)
	}
	// An upload that states no vintage is its own export, so the clock that
	// stamps it is this one. Stamped into the payload rather than read when the
	// job runs, because a job re-enqueued by the restart recovery path would
	// otherwise take the vintage of the retry instead of the upload.
	req = withUploadVintage(req)
	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("serialize request: %v", err))
	}
	brokerStr := db.BrokerToStr(w.GetBroker())
	jobID, err := s.db.CreateJob(ctx, db.CreateJobParams{
		UserID:       u.ID,
		JobType:      "tx",
		Broker:       brokerStr,
		Source:       w.GetSource(),
		Filename:     req.GetFilename(),
		PeriodFrom:   w.GetPeriodFrom(),
		PeriodBefore: w.GetPeriodBefore(),
		Payload:      payload,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	select {
	case s.queue <- &JobRequest{JobID: jobID, JobType: "tx"}:
	default:
		return nil, status.Error(codes.Unavailable, "job queue full")
	}
	return &ingestionv1.UpsertTxsResponse{JobId: jobID}, nil
}

// CreateTx creates a job and enqueues it for async processing.
func (s *Server) CreateTx(ctx context.Context, req *ingestionv1.CreateTxRequest) (*ingestionv1.CreateTxResponse, error) {
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
	if req.Posting == nil {
		return nil, status.Error(codes.InvalidArgument, "posting required")
	}
	// One payload shape for both paths: a window with no period, which is what
	// makes this an append rather than a replacement.
	brokerStr := db.BrokerToStr(req.Broker)
	wrapped := &ingestionv1.UpsertTxsRequest{
		Window: &archivev1.TxWindow{
			Broker:   req.Broker,
			Source:   req.GetSource(),
			Postings: []*archivev1.Posting{req.GetPosting()},
		},
	}
	payload, err := proto.Marshal(withUploadVintage(wrapped))
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
	return &ingestionv1.CreateTxResponse{JobId: jobID}, nil
}

// withUploadVintage returns req with exported_at filled in from this server's
// clock when the caller stated none. The identifiers a posting names are as of
// the moment its file was written; an uploader that knows that moment says so,
// and for one that does not the upload is the closest thing to it.
//
// The request is cloned rather than mutated: it belongs to the gRPC call, and a
// handler that writes to it makes the vintage visible to an interceptor reading
// the same message.
func withUploadVintage(req *ingestionv1.UpsertTxsRequest) *ingestionv1.UpsertTxsRequest {
	if req.GetExportedAt() != nil {
		return req
	}
	out := proto.CloneOf(req)
	out.ExportedAt = timestamppb.Now()
	return out
}
