package api

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/archive"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
)

// ImportSystemArchive queues one whole system archive and returns the job to
// poll. Admin only.
//
// The parts are applied in the worker rather than in this request, which is what
// makes an import survive the admin closing the tab: the only thing the client
// has to hold on to is the job id, and the per-part results are readable from
// GetJob for as long as the job row lives.
func (s *Server) ImportSystemArchive(ctx context.Context, req *apiv1.ImportSystemArchiveRequest) (*apiv1.ImportSystemArchiveResponse, error) {
	u, authErr := auth.RequireAdmin(ctx)
	if authErr != nil {
		return nil, authErr
	}
	a := req.GetArchive()
	if err := archive.CheckEnvelope(a.GetEnvelope(), archivev1.ArchiveKind_SYSTEM); err != nil {
		var ve *archive.VersionError
		if errors.As(err, &ve) {
			// The request is well formed and this server is the thing that is out
			// of date, which is a precondition rather than a bad argument.
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	parts := presentParts(a)
	if len(parts) == 0 {
		return nil, status.Error(codes.InvalidArgument, "archive carries no parts")
	}

	// The document alone is stored: the filename is a label for the job row and
	// the worker has no use for it.
	payload, err := proto.Marshal(a)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	jobID, err := s.db.CreateJob(ctx, db.CreateJobParams{
		UserID:   u.ID,
		JobType:  db.JobTypeSystemArchive,
		Filename: req.GetFilename(),
		Payload:  payload,
		Parts:    parts,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := s.enqueueJob(jobID, db.JobTypeSystemArchive); err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &apiv1.ImportSystemArchiveResponse{JobId: jobID}, nil
}

// presentParts names the parts a document carries, in restore order.
//
// Presence, not emptiness: a section present but empty says the export included
// it and there was nothing, which is a different statement from a section that
// was never included, and the import honours the difference.
func presentParts(a *archivev1.SystemArchive) []archivev1.ArchivePart {
	var parts []archivev1.ArchivePart
	if a.GetInstruments() != nil {
		parts = append(parts, archivev1.ArchivePart_INSTRUMENTS)
	}
	if a.GetPrices() != nil {
		parts = append(parts, archivev1.ArchivePart_PRICES)
	}
	if a.GetCorporateEvents() != nil {
		parts = append(parts, archivev1.ArchivePart_CORPORATE_EVENTS)
	}
	return parts
}
