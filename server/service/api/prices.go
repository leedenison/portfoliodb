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
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ListPrices returns paginated EOD prices with optional search and filters. Admin only.
func (s *Server) ListPrices(ctx context.Context, req *apiv1.ListPricesRequest) (*apiv1.ListPricesResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}

	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = 30
	}
	if pageSize > 100 {
		pageSize = 100
	}

	dateFrom := dateToTime(req.GetDateFrom())
	dateTo := dateToTime(req.GetDateTo())

	rows, totalCount, nextToken, err := s.db.ListPrices(ctx, req.GetSearch(), dateFrom, dateTo, req.GetDataProvider(), pageSize, req.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	prices := make([]*apiv1.EODPriceProto, 0, len(rows))
	for _, r := range rows {
		p := &apiv1.EODPriceProto{
			InstrumentId:          r.InstrumentID,
			InstrumentDisplayName: r.InstrumentDisplayName,
			PriceDate:             r.PriceDate.Format("2006-01-02"),
			Close:                 r.Close,
			DataProvider:          r.DataProvider,
			FetchedAt:             timestamppb.New(r.FetchedAt),
			Synthetic:             r.Synthetic,
		}
		if r.Open != nil {
			p.Open = r.Open
		}
		if r.High != nil {
			p.High = r.High
		}
		if r.Low != nil {
			p.Low = r.Low
		}
		if r.AdjustedClose != nil {
			p.AdjustedClose = r.AdjustedClose
		}
		if r.Volume != nil {
			p.Volume = r.Volume
		}
		prices = append(prices, p)
	}

	return &apiv1.ListPricesResponse{
		Prices:        prices,
		NextPageToken: nextToken,
		TotalCount:    totalCount,
	}, nil
}

// ExportPrices streams all cached EOD prices with the best identifier per instrument. Admin only.
func (s *Server) ExportPrices(req *apiv1.ExportPricesRequest, stream apiv1.ApiService_ExportPricesServer) error {
	ctx := stream.Context()
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return authErr
	}
	rows, err := s.db.ListPricesForExport(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	now := timestamppb.Now()
	for _, r := range rows {
		row := &apiv1.ExportPriceRow{
			ExportedAt:       now,
			IdentifierType:   r.IdentifierType,
			IdentifierValue:  r.IdentifierValue,
			IdentifierDomain: r.IdentifierDomain,
			AssetClass:       db.StrToAssetClass(r.AssetClass),
			PriceDate:        r.PriceDate.Format("2006-01-02"),
			Close:            r.Close,
		}
		if r.Open != nil {
			row.Open = r.Open
		}
		if r.High != nil {
			row.High = r.High
		}
		if r.Low != nil {
			row.Low = r.Low
		}
		if r.AdjustedClose != nil {
			row.AdjustedClose = r.AdjustedClose
		}
		if r.Volume != nil {
			row.Volume = r.Volume
		}
		if err := stream.Send(row); err != nil {
			return err
		}
	}
	return nil
}

// ImportPrices creates an async job to upsert EOD prices. Admin only.
// The serialized request is persisted to the DB and processed by the worker.
func (s *Server) ImportPrices(ctx context.Context, req *apiv1.ImportPricesRequest) (*apiv1.ImportPricesResponse, error) {
	u, authErr := auth.RequireAdmin(ctx)
	if authErr != nil {
		return nil, authErr
	}
	if len(req.GetPrices()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "no prices provided")
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("serialize request: %v", err))
	}
	jobID, err := s.db.CreateJob(ctx, db.CreateJobParams{
		UserID:  u.ID,
		JobType: "price",
		Payload: payload,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := s.enqueueJob(jobID, "price"); err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &apiv1.ImportPricesResponse{JobId: jobID}, nil
}

