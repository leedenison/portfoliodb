package api

import (
	"context"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
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
			Currency:              r.Currency,
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

// listingRef names one listing the way a file has to: an identifier, and the
// currency saying which of the security's lines. A UUID means nothing in another
// instance, and an identifier alone no longer picks a line out. See
// docs/adr/0069-a-listing-is-named-by-a-security-identifier-and-a-currency.md.
//
// It doubles as the scan's grouping key, protobuf messages not being comparable.
type listingRef struct {
	ref      db.InstrumentRef
	currency string
}

func (k listingRef) archive() *archivev1.InstrumentRef {
	out := archiveRef(k.ref)
	out.Currency = k.currency
	return out
}

// priceGroups turns the flat export rows and the per-listing coverage spans into
// archive groups, one per listing.
//
// A listing that was covered and has no rows still gets a group, with empty
// rows. It is the only way a file can say a provider was asked about those
// dates and had nothing, and the coverage row is where such a group's asset
// class and currency come from.
//
// Both inputs arrive ordered by identifier and then currency, so the rows are
// grouped by a scan and the output order follows the query's.
func priceGroups(rows []db.ExportPriceRow, coverage []db.ExportPriceCoverageRow) []*archivev1.PriceGroup {
	spans := make(map[listingRef][]*archivev1.DateInterval, len(coverage))
	for _, c := range coverage {
		k := listingRef{c.Ref, c.Currency}
		spans[k] = append(spans[k], &archivev1.DateInterval{
			From:   c.From.Format("2006-01-02"),
			Before: c.Before.Format("2006-01-02"),
		})
	}

	var out []*archivev1.PriceGroup
	var cur *archivev1.PriceGroup
	var curKey listingRef
	for _, r := range rows {
		k := listingRef{r.Ref, r.Currency}
		if cur == nil || k != curKey {
			cur = &archivev1.PriceGroup{
				Instrument: k.archive(),
				AssetClass: db.StrToAssetClass(r.AssetClass),
				Coverage:   spans[k],
			}
			delete(spans, k)
			curKey = k
			out = append(out, cur)
		}
		cur.Rows = append(cur.Rows, priceRow(r))
	}

	// Whatever coverage is left names a listing with no rows at all.
	for _, c := range coverage {
		k := listingRef{c.Ref, c.Currency}
		cov, ok := spans[k]
		if !ok {
			continue
		}
		delete(spans, k)
		out = append(out, &archivev1.PriceGroup{
			Instrument: k.archive(),
			AssetClass: db.StrToAssetClass(c.AssetClass),
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
