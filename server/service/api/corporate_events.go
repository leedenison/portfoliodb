package api

import (
	"context"
	"fmt"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// ExportCorporateEvents streams every stored stock split and cash dividend
// with the best identifier per instrument. Splits stream first, then
// dividends; within each block rows are ordered by (identifier_type,
// identifier_value, ex_date). Admin only.
func (s *Server) ExportCorporateEvents(req *apiv1.ExportCorporateEventsRequest, stream apiv1.ApiService_ExportCorporateEventsServer) error {
	ctx := stream.Context()
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return authErr
	}

	splits, err := s.db.ListStockSplitsForExport(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	for _, r := range splits {
		row := &apiv1.ExportCorporateEventRow{
			IdentifierType:   r.IdentifierType,
			IdentifierValue:  r.IdentifierValue,
			IdentifierDomain: r.IdentifierDomain,
			AssetClass:       db.StrToAssetClass(r.AssetClass),
			DataProvider:     r.DataProvider,
			Event: &apiv1.ExportCorporateEventRow_Split{
				Split: &apiv1.SplitRow{
					ExDate:    r.ExDate.Format("2006-01-02"),
					SplitFrom: r.SplitFrom,
					SplitTo:   r.SplitTo,
				},
			},
		}
		if err := stream.Send(row); err != nil {
			return err
		}
	}

	dividends, err := s.db.ListCashDividendsForExport(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	for _, r := range dividends {
		div := &apiv1.CashDividendRow{
			ExDate:    r.ExDate.Format("2006-01-02"),
			Amount:    r.Amount,
			Currency:  r.Currency,
			Frequency: r.Frequency,
		}
		if r.PayDate != nil {
			div.PayDate = r.PayDate.Format("2006-01-02")
		}
		if r.RecordDate != nil {
			div.RecordDate = r.RecordDate.Format("2006-01-02")
		}
		if r.DeclarationDate != nil {
			div.DeclarationDate = r.DeclarationDate.Format("2006-01-02")
		}
		row := &apiv1.ExportCorporateEventRow{
			IdentifierType:   r.IdentifierType,
			IdentifierValue:  r.IdentifierValue,
			IdentifierDomain: r.IdentifierDomain,
			AssetClass:       db.StrToAssetClass(r.AssetClass),
			DataProvider:     r.DataProvider,
			Event:            &apiv1.ExportCorporateEventRow_Dividend{Dividend: div},
		}
		if err := stream.Send(row); err != nil {
			return err
		}
	}
	return nil
}

// ImportCorporateEvents creates an async job to upsert stock splits and cash
// dividends. The serialized request is persisted to the DB and processed by
// the ingestion worker. Admin only.
func (s *Server) ImportCorporateEvents(ctx context.Context, req *apiv1.ImportCorporateEventsRequest) (*apiv1.ImportCorporateEventsResponse, error) {
	u, authErr := auth.RequireAdmin(ctx)
	if authErr != nil {
		return nil, authErr
	}
	if len(req.GetEvents()) == 0 && len(req.GetCoverage()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "no events or coverage provided")
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("serialize request: %v", err))
	}
	jobID, err := s.db.CreateJob(ctx, db.CreateJobParams{
		UserID:  u.ID,
		JobType: db.JobTypeCorporateEvent,
		Payload: payload,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := s.enqueueJob(jobID, db.JobTypeCorporateEvent); err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &apiv1.ImportCorporateEventsResponse{JobId: jobID}, nil
}
