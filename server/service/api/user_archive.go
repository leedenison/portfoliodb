package api

import (
	"context"
	"errors"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/archive"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
)

// ImportUserArchive queues one whole user archive and returns the job to poll.
// It applies to the caller's own account: a user archive does not name its user,
// so restoring one into a different account is a feature rather than an
// accident.
//
// The parts are applied in the worker rather than in this request, for the same
// reason the system import is: the only thing the client has to hold on to is
// the job id, and the per-part results outlive the page that started it.
func (s *Server) ImportUserArchive(ctx context.Context, req *apiv1.ImportUserArchiveRequest) (*apiv1.ImportUserArchiveResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	a := req.GetArchive()
	if err := archive.CheckEnvelope(a.GetEnvelope(), archivev1.ArchiveKind_USER); err != nil {
		var ve *archive.VersionError
		if errors.As(err, &ve) {
			// The request is well formed and this server is the thing that is out
			// of date, which is a precondition rather than a bad argument.
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	parts := presentUserParts(a)
	if len(parts) == 0 {
		return nil, status.Error(codes.InvalidArgument, "archive carries no parts")
	}

	payload, err := proto.Marshal(a)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	jobID, err := s.db.CreateJob(ctx, db.CreateJobParams{
		UserID:   u.ID,
		JobType:  db.JobTypeUserArchive,
		Filename: req.GetFilename(),
		Payload:  payload,
		Parts:    parts,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := s.enqueueJob(jobID, db.JobTypeUserArchive); err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &apiv1.ImportUserArchiveResponse{JobId: jobID}, nil
}

// presentUserParts names the parts a user archive carries, in restore order.
//
// Presence, not emptiness, as in the system archive: a section present but empty
// says the export included it and there was nothing, which is a different
// statement from a section that was never included.
func presentUserParts(a *archivev1.UserArchive) []archivev1.ArchivePart {
	var parts []archivev1.ArchivePart
	if a.GetPreferences() != nil {
		parts = append(parts, archivev1.ArchivePart_PREFERENCES)
	}
	return parts
}

// ExportUserArchive streams one user archive: the envelope, then the selected
// parts in restore order. It carries the caller's own data and no system data
// at all, which is why it is scoped with RequireUser rather than RequireAdmin
// and why it is a separate document.
// See docs/adr/0033-system-and-user-archives-are-separate.md.
//
// The stream has the same grammar as the system export, including for a part
// whose unit is a setting rather than a row: the part_begin marker is what
// creates the container, so a part that was asked for and holds nothing is
// still present and empty.
func (s *Server) ExportUserArchive(req *apiv1.ExportUserArchiveRequest, stream apiv1.ApiService_ExportUserArchiveServer) error {
	ctx := stream.Context()
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return authErr
	}
	// source_instance is left empty for the same reason the system export leaves
	// it empty: nothing keys off it and this build has no configured identity.
	if err := stream.Send(&apiv1.ExportUserArchiveResponse{
		Item: &apiv1.ExportUserArchiveResponse_Envelope{
			Envelope: archive.NewEnvelope("", archivev1.ArchiveKind_USER),
		},
	}); err != nil {
		return err
	}
	parts, err := orderedParts(req.GetParts(), userPartOrder)
	if err != nil {
		return err
	}
	for _, part := range parts {
		if err := stream.Send(&apiv1.ExportUserArchiveResponse{
			Item: &apiv1.ExportUserArchiveResponse_PartBegin{
				PartBegin: &apiv1.ArchivePartBegin{Part: part},
			},
		}); err != nil {
			return err
		}
		var partErr error
		switch part {
		case archivev1.ArchivePart_PREFERENCES:
			partErr = s.sendPreferencePart(ctx, u.ID, stream)
		}
		if partErr != nil {
			return partErr
		}
	}
	return nil
}

// sendPreferencePart sends the user's settings as one whole-part message. The
// part is two settings rather than a list of rows, so there is nothing to
// stream and nothing to group by.
//
// Both settings are always stated, because both are always known: the display
// currency is a NOT NULL column and a user with no ignore rules has an empty
// set rather than an unstated one. The empty IgnoredAssetClasses container is
// what says so -- an absent one would tell an importer to leave the stored
// rules alone, which is the opposite instruction.
func (s *Server) sendPreferencePart(ctx context.Context, userID string, stream apiv1.ApiService_ExportUserArchiveServer) error {
	currency, err := s.db.GetDisplayCurrency(ctx, userID)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	rules, err := s.db.ListIgnoredAssetClasses(ctx, userID)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	part := &archivev1.PreferencePart{
		IgnoredAssetClasses: &archivev1.IgnoredAssetClasses{Rules: archiveIgnoredRules(rules)},
	}
	if currency != "" {
		part.DisplayCurrency = proto.String(currency)
	}
	return stream.Send(&apiv1.ExportUserArchiveResponse{
		Item: &apiv1.ExportUserArchiveResponse_Preferences{Preferences: part},
	})
}

// archiveIgnoredRules turns stored ignore rules into their archive form.
//
// A stored broker or asset class this build has no enum value for is dropped
// rather than written as UNSPECIFIED: no importer could apply the rule, and an
// UNSPECIFIED enum would fail validation on the way back in, taking the whole
// setting with it.
func archiveIgnoredRules(rules []db.IgnoredAssetClass) []*archivev1.IgnoredAssetClassRule {
	out := make([]*archivev1.IgnoredAssetClassRule, 0, len(rules))
	for _, r := range rules {
		broker := db.StrToBroker(r.Broker)
		assetClass := db.StrToAssetClass(r.AssetClass)
		if broker == 0 || assetClass == 0 {
			log.Printf("user archive export: skipping ignore rule (broker %q, asset class %q): not a known value", r.Broker, r.AssetClass)
			continue
		}
		out = append(out, &archivev1.IgnoredAssetClassRule{
			Broker:     broker,
			Account:    r.Account,
			AssetClass: assetClass,
		})
	}
	return out
}
