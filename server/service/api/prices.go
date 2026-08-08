package api

import (
	"context"
	"fmt"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/archive"
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
	dateBefore := dateToTime(req.GetDateBefore())

	rows, totalCount, nextToken, err := s.db.ListPrices(ctx, req.GetSearch(), dateFrom, dateBefore, req.GetDataProvider(), pageSize, req.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	prices := make([]*apiv1.EODPriceProto, 0, len(rows))
	for _, r := range rows {
		p := &apiv1.EODPriceProto{
			InstrumentId:          r.InstrumentID,
			InstrumentDisplayName: r.InstrumentDisplayName,
			PriceDate:             r.PriceDate.Format("2006-01-02"),
			Close:                 decStr(r.Close),
			DataProvider:          r.DataProvider,
			LastFetchedAt:         timestamppb.New(r.LastFetchedAt),
			ShareCountBasis:       r.ShareCountBasis.Format("2006-01-02"),
		}
		p.Open = decStrPtr(r.Open)
		p.High = decStrPtr(r.High)
		p.Low = decStrPtr(r.Low)
		p.AdjustedClose = decStrPtr(r.AdjustedClose)
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

// ExportPrices streams the price part of an admin archive: the envelope first,
// then one group per instrument. Admin only.
//
// The coverage nested in each group is not derivable from its rows: a span
// covering dates with no rows records that a provider was asked and had
// nothing.
func (s *Server) ExportPrices(req *apiv1.ExportPricesRequest, stream apiv1.ApiService_ExportPricesServer) error {
	ctx := stream.Context()
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return authErr
	}
	coverage, err := s.db.ListPriceCoverageForExport(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	rows, err := s.db.ListPricesForExport(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	// source_instance is left empty: nothing keys off it, and this build has no
	// configured identity to put there.
	if err := stream.Send(&apiv1.ExportPricesResponse{
		Item: &apiv1.ExportPricesResponse_Envelope{
			Envelope: archive.NewEnvelope("", archivev1.ArchiveKind_ADMIN),
		},
	}); err != nil {
		return err
	}
	for _, g := range priceGroups(rows, coverage) {
		if err := stream.Send(&apiv1.ExportPricesResponse{
			Item: &apiv1.ExportPricesResponse_Group{Group: g},
		}); err != nil {
			return err
		}
	}
	return nil
}

// instKey is an instrument named the way a file names it: the identifier triple
// bestIdentifierJoin picked, and no server UUID.
type instKey struct{ typ, value, domain string }

// priceGroups turns the flat export rows and the per-instrument coverage spans
// into archive groups, one per instrument.
//
// An instrument that was covered and has no rows still gets a group, with empty
// rows. It is the only way a file can say a provider was asked about those
// dates and had nothing, and the coverage row is where such a group's asset
// class and currency come from.
//
// Both inputs arrive ordered by identifier, so the rows are grouped by a scan
// and the output order follows the query's.
func priceGroups(rows []db.ExportPriceRow, coverage []db.ExportPriceCoverageRow) []*archivev1.PriceGroup {
	spans := make(map[instKey][]*archivev1.DateInterval, len(coverage))
	for _, c := range coverage {
		k := instKey{c.IdentifierType, c.IdentifierValue, c.IdentifierDomain}
		spans[k] = append(spans[k], &archivev1.DateInterval{
			From:   c.From.Format("2006-01-02"),
			Before: c.Before.Format("2006-01-02"),
		})
	}

	var out []*archivev1.PriceGroup
	var cur *archivev1.PriceGroup
	var curKey instKey
	for _, r := range rows {
		k := instKey{r.IdentifierType, r.IdentifierValue, r.IdentifierDomain}
		if cur == nil || k != curKey {
			cur = &archivev1.PriceGroup{
				Instrument: &archivev1.InstrumentRef{
					Type:   identifierTypeFromString(r.IdentifierType),
					Value:  r.IdentifierValue,
					Domain: r.IdentifierDomain,
				},
				AssetClass: db.StrToAssetClass(r.AssetClass),
				Currency:   r.Currency,
				Coverage:   spans[k],
			}
			delete(spans, k)
			curKey = k
			out = append(out, cur)
		}
		cur.Rows = append(cur.Rows, priceRow(r))
	}

	// Whatever coverage is left names an instrument with no rows at all.
	for _, c := range coverage {
		k := instKey{c.IdentifierType, c.IdentifierValue, c.IdentifierDomain}
		cov, ok := spans[k]
		if !ok {
			continue
		}
		delete(spans, k)
		out = append(out, &archivev1.PriceGroup{
			Instrument: &archivev1.InstrumentRef{
				Type:   identifierTypeFromString(c.IdentifierType),
				Value:  c.IdentifierValue,
				Domain: c.IdentifierDomain,
			},
			AssetClass: db.StrToAssetClass(c.AssetClass),
			Currency:   c.Currency,
			Coverage:   cov,
		})
	}
	return out
}

// priceRow converts one export row to its archive form. The share count basis
// is written only when the row has one: absent means the bar's own date, which
// is the as-traded convention and what the query reports as nil.
func priceRow(r db.ExportPriceRow) *archivev1.PriceRow {
	row := &archivev1.PriceRow{
		PriceDate: r.PriceDate.Format("2006-01-02"),
		Close:     decStr(r.Close),
	}
	if r.ShareCountBasis != nil {
		row.ShareCountBasis = proto.String(r.ShareCountBasis.Format("2006-01-02"))
	}
	row.Open = decStrPtr(r.Open)
	row.High = decStrPtr(r.High)
	row.Low = decStrPtr(r.Low)
	row.AdjustedClose = decStrPtr(r.AdjustedClose)
	if r.Volume != nil {
		row.Volume = r.Volume
	}
	return row
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
