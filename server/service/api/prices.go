package api

import (
	"context"
	"fmt"
	"time"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

	var dateFrom, dateTo time.Time
	if req.GetDateFrom() != "" {
		parsed, err := time.Parse("2006-01-02", req.GetDateFrom())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid date_from: %v", err)
		}
		dateFrom = parsed
	}
	if req.GetDateTo() != "" {
		parsed, err := time.Parse("2006-01-02", req.GetDateTo())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid date_to: %v", err)
		}
		dateTo = parsed
	}

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
	for _, r := range rows {
		row := &apiv1.ExportPriceRow{
			IdentifierType:   r.IdentifierType,
			IdentifierValue:  r.IdentifierValue,
			IdentifierDomain: r.IdentifierDomain,
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

// ImportPrices upserts EOD prices resolved by instrument identifier. Admin only.
func (s *Server) ImportPrices(ctx context.Context, req *apiv1.ImportPricesRequest) (*apiv1.ImportPricesResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}

	var prices []db.EODPrice
	var errors []*apiv1.ImportPriceError

	for i, row := range req.GetPrices() {
		priceDate, err := time.Parse("2006-01-02", row.GetPriceDate())
		if err != nil {
			errors = append(errors, &apiv1.ImportPriceError{
				Index:   int32(i),
				Message: fmt.Sprintf("invalid price_date %q: %v", row.GetPriceDate(), err),
			})
			continue
		}

		instID, err := s.resolveInstrument(ctx, row.GetIdentifierType(), row.GetIdentifierDomain(), row.GetIdentifierValue())
		if err != nil {
			errors = append(errors, &apiv1.ImportPriceError{
				Index:   int32(i),
				Message: err.Error(),
			})
			continue
		}

		p := db.EODPrice{
			InstrumentID: instID,
			PriceDate:    priceDate,
			Close:        row.GetClose(),
			DataProvider: "import",
		}
		if row.Open != nil {
			p.Open = row.Open
		}
		if row.High != nil {
			p.High = row.High
		}
		if row.Low != nil {
			p.Low = row.Low
		}
		if row.Volume != nil {
			p.Volume = row.Volume
		}
		prices = append(prices, p)
	}

	if len(prices) > 0 {
		if err := s.db.UpsertPrices(ctx, prices); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	return &apiv1.ImportPricesResponse{
		UpsertedCount: int32(len(prices)),
		Errors:        errors,
	}, nil
}

// resolveInstrument finds instrument ID by identifier type, domain, and value.
func (s *Server) resolveInstrument(ctx context.Context, idType, domain, value string) (string, error) {
	var instID string
	var err error

	switch idType {
	case "BROKER_DESCRIPTION":
		instID, err = s.db.FindInstrumentBySourceDescription(ctx, domain, value)
	default:
		if domain != "" {
			instID, err = s.db.FindInstrumentByIdentifier(ctx, idType, domain, value)
		} else {
			instID, err = s.db.FindInstrumentByTypeAndValue(ctx, idType, value)
		}
	}

	if err != nil {
		return "", fmt.Errorf("lookup error for %s %q: %v", idType, value, err)
	}
	if instID == "" {
		return "", fmt.Errorf("instrument not found for %s %q", idType, value)
	}
	return instID, nil
}
