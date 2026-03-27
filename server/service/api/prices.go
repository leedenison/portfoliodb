package api

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/service/identification"
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
			AssetClass:       r.AssetClass,
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

// ImportPrices upserts EOD prices resolved by instrument identifier. When an
// instrument is unknown, identifier plugins are called (if asset_class is set)
// or the instrument is created with just the supplied identifier. Admin only.
func (s *Server) ImportPrices(ctx context.Context, req *apiv1.ImportPricesRequest) (*apiv1.ImportPricesResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}

	var prices []db.EODPrice
	var errors []*apiv1.ImportPriceError

	// Dedup cache: avoid calling plugins N times for the same identifier across different price dates.
	type resolveEntry struct {
		instID string
		err    error
	}
	resolveCache := make(map[string]*resolveEntry)

	for i, row := range req.GetPrices() {
		priceDate, err := time.Parse("2006-01-02", row.GetPriceDate())
		if err != nil {
			errors = append(errors, &apiv1.ImportPriceError{
				Index:   int32(i),
				Message: fmt.Sprintf("invalid price_date %q: %v", row.GetPriceDate(), err),
			})
			continue
		}

		cacheKey := row.GetIdentifierType() + "\x00" + row.GetIdentifierDomain() + "\x00" + row.GetIdentifierValue()
		entry, cached := resolveCache[cacheKey]
		if !cached {
			instID, resolveErr := s.resolveOrIdentifyInstrument(ctx, row.GetIdentifierType(), row.GetIdentifierDomain(), row.GetIdentifierValue(), row.GetAssetClass())
			entry = &resolveEntry{instID: instID, err: resolveErr}
			resolveCache[cacheKey] = entry
		}
		if entry.err != nil {
			errors = append(errors, &apiv1.ImportPriceError{
				Index:   int32(i),
				Message: entry.err.Error(),
			})
			continue
		}

		p := db.EODPrice{
			InstrumentID: entry.instID,
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

// resolveOrIdentifyInstrument finds an instrument by identifier, or creates one.
// When assetClass is set and the instrument is unknown, identifier plugins are called
// (ResolveWithPlugins handles the DB lookup internally). When assetClass is empty,
// plugins are skipped and the instrument is created with just the supplied identifier.
func (s *Server) resolveOrIdentifyInstrument(ctx context.Context, idType, domain, value, assetClass string) (string, error) {
	hint := identifier.Identifier{Type: idType, Domain: domain, Value: value}

	// When asset_class is set and plugins are available, delegate entirely to
	// ResolveWithPlugins which does DB lookup -> plugin calls -> fallback.
	if assetClass != "" && s.pluginRegistry != nil {
		fallback := func(ctx context.Context, database db.DB) (string, error) {
			return s.ensureWithSuppliedIdentifier(ctx, idType, domain, value)
		}
		hints := identifier.Hints{SecurityTypeHint: assetClass}
		result, err := identification.ResolveWithPlugins(ctx, s.db, s.pluginRegistry,
			"", "", "", hints,
			[]identifier.Identifier{hint},
			false, fallback, nil, nil, 0)
		if err != nil {
			return "", fmt.Errorf("identification error for %s %q: %v", idType, value, err)
		}
		return result.InstrumentID, nil
	}

	// No plugins: DB lookup, then create with just the supplied identifier.
	ids, err := identification.ResolveByHintsDBOnly(ctx, s.db, []identifier.Identifier{hint})
	if err != nil {
		return "", fmt.Errorf("lookup error for %s %q: %v", idType, value, err)
	}
	if len(ids) > 1 {
		return "", fmt.Errorf("ambiguous: multiple instruments match %s %q", idType, value)
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	return s.ensureWithSuppliedIdentifier(ctx, idType, domain, value)
}

// ensureWithSuppliedIdentifier creates an instrument with just the given identifier.
func (s *Server) ensureWithSuppliedIdentifier(ctx context.Context, idType, domain, value string) (string, error) {
	slog.Debug("creating instrument from price import with supplied identifier only",
		"identifier_type", idType, "identifier_domain", domain, "identifier_value", value)
	return s.db.EnsureInstrument(ctx, "", "", "", "", "", "",
		[]db.IdentifierInput{{Type: idType, Domain: domain, Value: value, Canonical: true}},
		"", nil, nil)
}
