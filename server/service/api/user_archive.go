package api

import (
	"context"
	"errors"
	"log"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
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
	if a.GetTxs() != nil {
		parts = append(parts, archivev1.ArchivePart_TXS)
	}
	if a.GetDeclarations() != nil {
		parts = append(parts, archivev1.ArchivePart_DECLARATIONS)
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
	from, before := req.GetPeriodFrom(), req.GetPeriodBefore()
	if from != nil && before != nil && !before.AsTime().After(from.AsTime()) {
		return status.Error(codes.InvalidArgument, "period_before must be after period_from")
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
		case archivev1.ArchivePart_TXS:
			partErr = s.sendTxPart(ctx, u.ID, from, before, stream)
		case archivev1.ArchivePart_DECLARATIONS:
			partErr = s.sendDeclarationPart(ctx, u.ID, stream)
		}
		if partErr != nil {
			return partErr
		}
	}
	return nil
}

// sendPreferencePart sends the user's settings as one whole-part message. The
// part is a setting rather than a list of rows, so there is nothing to stream
// and nothing to group by.
//
// The display currency is always stated, because it is always known: it is a
// NOT NULL column.
func (s *Server) sendPreferencePart(ctx context.Context, userID string, stream apiv1.ApiService_ExportUserArchiveServer) error {
	currency, err := s.db.GetDisplayCurrency(ctx, userID)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	part := &archivev1.PreferencePart{}
	if currency != "" {
		part.DisplayCurrency = proto.String(currency)
	}
	return stream.Send(&apiv1.ExportUserArchiveResponse{
		Item: &apiv1.ExportUserArchiveResponse_Preferences{Preferences: part},
	})
}

// sendTxPart streams one window per broker, each nesting its groups.
//
// Grouping is what makes this a part rather than a list of rows. It is the
// converter's output and nothing can rebuild it -- the server does not pair rows
// or infer a missing leg -- so an export that flattened it would lose the
// balance invariant, residual attribution and the association between a fee and
// its trade, permanently. See
// docs/adr/0021-converters-own-transaction-grouping.md.
//
// The routed residual postings travel with their groups. They are
// server-generated, but a group exported with its residual already sums to zero
// and the balancer routes nothing for a commodity that does, so re-importing one
// is idempotent rather than doubling.
//
// A requested period is passed through to the windows rather than only filtering
// the rows: a window states its own period, and one holding no groups is a valid
// instruction to clear that period.
func (s *Server) sendTxPart(ctx context.Context, userID string, from, before *timestamppb.Timestamp, stream apiv1.ApiService_ExportUserArchiveServer) error {
	rows, err := s.db.ListTxsForExport(ctx, userID, from, before)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	for _, w := range txWindows(rows, from, before) {
		if err := stream.Send(&apiv1.ExportUserArchiveResponse{
			Item: &apiv1.ExportUserArchiveResponse_TxWindow{TxWindow: w},
		}); err != nil {
			return err
		}
	}
	return nil
}

// sendDeclarationPart streams one statement per account and date, each nesting
// the holdings declared at it.
//
// A declaration is the user's own statement about a holding: unlike a
// transaction nothing can refetch it from a broker, and unlike a price nothing
// can refetch it from anywhere, so it travels or it is lost.
//
// The requested period is not applied. It scopes the transaction part alone, as
// ExportUserArchiveRequest says: a declaration is dated, but by the date it
// speaks about rather than by a period of activity, and a rebuild that dropped
// the checkpoints outside the exported window would silently drop the pads its
// oldest holdings open from.
//
// What is not written: the pad/assert discriminator, which follows from the
// declaration dates and could only ever disagree with them, and the check
// against the computed holding, which is recomputed from the postings on read.
func (s *Server) sendDeclarationPart(ctx context.Context, userID string, stream apiv1.ApiService_ExportUserArchiveServer) error {
	rows, err := s.db.ListHoldingDeclarationsForExport(ctx, userID)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	for _, st := range declarationStatements(rows) {
		if err := stream.Send(&apiv1.ExportUserArchiveResponse{
			Item: &apiv1.ExportUserArchiveResponse_DeclarationStatement{DeclarationStatement: st},
		}); err != nil {
			return err
		}
	}
	return nil
}

// declarationStatements cuts export rows into statements. The rows arrive
// ordered by broker, account and date, so a statement is a run and one pass
// makes them.
//
// A row this build cannot name in a file is dropped with a log line rather than
// written as something an importer would refuse: an UNSPECIFIED broker and an
// empty identifier both fail validation on the way back in, and either would
// take the whole statement down with it. A statement left with no declarations
// is not sent at all, for the same reason.
func declarationStatements(rows []db.ExportDeclaration) []*archivev1.Statement {
	var out []*archivev1.Statement
	var cur *archivev1.Statement
	var curBroker, curAccount string
	var curDate time.Time
	// A statement is emitted once it is complete, so that one left empty by the
	// skips below never reaches the stream.
	closeStatement := func() {
		if cur != nil && len(cur.GetDeclarations()) > 0 {
			out = append(out, cur)
		}
		cur = nil
	}
	for _, r := range rows {
		broker := db.StrToBroker(r.Broker)
		if broker == 0 {
			log.Printf("user archive export: skipping declaration (broker %q, account %q): not a known broker", r.Broker, r.Account)
			continue
		}
		idType := identifierTypeFromString(r.Ref.Type)
		if r.Ref.Value == "" || idType == typev1.IdentifierType_IDENTIFIER_TYPE_UNSPECIFIED {
			log.Printf("user archive export: skipping declaration (broker %q, account %q, as of %s): its instrument has no identifier this build can name it by",
				r.Broker, r.Account, r.AsOfDate.Format("2006-01-02"))
			continue
		}
		if cur == nil || r.Broker != curBroker || r.Account != curAccount || !r.AsOfDate.Equal(curDate) {
			closeStatement()
			cur = &archivev1.Statement{
				Broker:   broker,
				Account:  r.Account,
				AsOfDate: r.AsOfDate.Format("2006-01-02"),
			}
			curBroker, curAccount, curDate = r.Broker, r.Account, r.AsOfDate
		}
		d := &archivev1.Declaration{
			Instrument:  archiveRef(r.Ref),
			DeclaredQty: r.DeclaredQty.String(),
		}
		// Written only where it differs from the statement's own date, which is
		// what an absent one already means.
		if r.ShareCountBasis != nil {
			d.ShareCountBasis = proto.String(r.ShareCountBasis.Format("2006-01-02"))
		}
		cur.Declarations = append(cur.Declarations, d)
	}
	closeStatement()
	return out
}
