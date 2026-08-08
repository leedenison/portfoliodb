package api

import (
	"context"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/archive"
	dbpkg "github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
	"github.com/shopspring/decimal"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"testing"
	"time"
)

func TestExportPrices_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	stream := &exportPriceStreamMock{ctx: authCtx("user-1", "sub|1")}
	err := srv.ExportPrices(&apiv1.ExportPricesRequest{}, stream)
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestExportPrices_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	open := decimal.RequireFromString("185.5")
	vol := int64(50000000)
	basis := time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC)
	rows := []dbpkg.ExportPriceRow{
		{
			IdentifierType:   "MIC_TICKER",
			IdentifierValue:  "AAPL",
			IdentifierDomain: "US",
			AssetClass:       "STOCK",
			Currency:         "USD",
			PriceDate:        time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			Open:             &open,
			Close:            decimal.RequireFromString("185.90"),
			Volume:           &vol,
		},
		{
			IdentifierType:   "MIC_TICKER",
			IdentifierValue:  "AAPL",
			IdentifierDomain: "US",
			AssetClass:       "STOCK",
			Currency:         "USD",
			PriceDate:        time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC),
			ShareCountBasis:  &basis,
			Close:            decimal.RequireFromString("18.59"),
		},
	}
	db.EXPECT().
		ListPriceCoverageForExport(gomock.Any()).
		Return([]dbpkg.ExportPriceCoverageRow{{
			IdentifierType:   "MIC_TICKER",
			IdentifierValue:  "AAPL",
			IdentifierDomain: "US",
			AssetClass:       "STOCK",
			Currency:         "USD",
			From:             time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			Before:           time.Date(2024, 1, 17, 0, 0, 0, 0, time.UTC),
		}}, nil)
	db.EXPECT().
		ListPricesForExport(gomock.Any()).
		Return(rows, nil)
	stream := &exportPriceStreamMock{ctx: adminCtx("user-1", "sub|1")}
	err := srv.ExportPrices(&apiv1.ExportPricesRequest{}, stream)
	if err != nil {
		t.Fatalf("ExportPrices: %v", err)
	}
	if len(stream.groups()) != 1 {
		t.Fatalf("expected 1 group streamed, got %d", len(stream.groups()))
	}
	g := stream.groups()[0]
	if g.GetInstrument().GetType() != typev1.IdentifierType_MIC_TICKER || g.GetInstrument().GetValue() != "AAPL" {
		t.Fatalf("got identifier %s %s", g.GetInstrument().GetType(), g.GetInstrument().GetValue())
	}
	if g.GetInstrument().GetDomain() != "US" {
		t.Fatalf("expected domain US, got %s", g.GetInstrument().GetDomain())
	}
	if g.GetAssetClass() != typev1.AssetClass_STOCK {
		t.Fatalf("expected asset_class=ASSET_CLASS_STOCK, got %s", g.GetAssetClass())
	}
	if g.GetCurrency() != "USD" {
		t.Fatalf("expected currency=USD, got %s", g.GetCurrency())
	}
	if len(g.GetCoverage()) != 1 || g.GetCoverage()[0].GetFrom() != "2024-01-15" || g.GetCoverage()[0].GetBefore() != "2024-01-17" {
		t.Fatalf("got coverage %+v", g.GetCoverage())
	}
	if len(g.GetRows()) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(g.GetRows()))
	}
	row := g.GetRows()[0]
	if row.GetPriceDate() != "2024-01-15" {
		t.Fatalf("expected date 2024-01-15, got %s", row.GetPriceDate())
	}
	// The fixture is 185.90 and the wire carries 185.9: decimal.String() emits
	// the canonical shortest form, so a value read out of a NUMERIC column does
	// not arrive padded with the column's scale.
	if row.GetClose() != "185.9" {
		t.Fatalf("expected close=185.9, got %v", row.GetClose())
	}
	if row.Open == nil || *row.Open != "185.5" {
		t.Fatalf("expected open=185.5, got %v", row.Open)
	}
	if row.Volume == nil || *row.Volume != 50000000 {
		t.Fatalf("expected volume=50000000, got %v", row.Volume)
	}
	// An as-traded bar says nothing; only the restated one carries a basis.
	if row.ShareCountBasis != nil {
		t.Fatalf("expected no share_count_basis on the as-traded row, got %v", row.ShareCountBasis)
	}
	if g.GetRows()[1].GetShareCountBasis() != "2024-06-10" {
		t.Fatalf("expected share_count_basis=2024-06-10, got %v", g.GetRows()[1].ShareCountBasis)
	}
}

// The envelope carries the knowledge time the whole file is stamped with, so it
// has to arrive before anything a reader would attribute to it.
func TestExportPrices_SendsEnvelopeFirst(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		ListPriceCoverageForExport(gomock.Any()).
		Return(nil, nil)
	db.EXPECT().
		ListPricesForExport(gomock.Any()).
		Return([]dbpkg.ExportPriceRow{{
			IdentifierType:  "MIC_TICKER",
			IdentifierValue: "AAPL",
			PriceDate:       time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			Close:           decimal.RequireFromString("185.90"),
		}}, nil)
	stream := &exportPriceStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportPrices(&apiv1.ExportPricesRequest{}, stream); err != nil {
		t.Fatalf("ExportPrices: %v", err)
	}
	if len(stream.sent) != 2 {
		t.Fatalf("expected 2 stream items, got %d", len(stream.sent))
	}
	env := stream.sent[0].GetEnvelope()
	if env == nil {
		t.Fatalf("expected the envelope first, got %+v", stream.sent[0])
	}
	if env.GetFormatVersion() != archive.FormatVersion {
		t.Fatalf("expected format_version=%d, got %d", archive.FormatVersion, env.GetFormatVersion())
	}
	if env.GetKind() != archivev1.ArchiveKind_ADMIN {
		t.Fatalf("expected kind=ADMIN, got %s", env.GetKind())
	}
	if env.GetExportedAt() == nil {
		t.Fatal("expected exported_at to be stamped")
	}
	if stream.sent[1].GetGroup() == nil {
		t.Fatalf("expected a group second, got %+v", stream.sent[1])
	}
}

// A provider asked about a range and reporting nothing is the one fact rows
// cannot carry, so a covered instrument with no bars is still a group.
func TestExportPrices_CoveredInstrumentWithNoRows(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		ListPriceCoverageForExport(gomock.Any()).
		Return([]dbpkg.ExportPriceCoverageRow{{
			IdentifierType:  "MIC_TICKER",
			IdentifierValue: "DELISTED",
			AssetClass:      "STOCK",
			Currency:        "USD",
			From:            time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			Before:          time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		}}, nil)
	db.EXPECT().
		ListPricesForExport(gomock.Any()).
		Return(nil, nil)
	stream := &exportPriceStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportPrices(&apiv1.ExportPricesRequest{}, stream); err != nil {
		t.Fatalf("ExportPrices: %v", err)
	}
	groups := stream.groups()
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(groups[0].GetRows()) != 0 {
		t.Fatalf("expected no rows, got %d", len(groups[0].GetRows()))
	}
	if len(groups[0].GetCoverage()) != 1 {
		t.Fatalf("expected 1 coverage span, got %d", len(groups[0].GetCoverage()))
	}
	if groups[0].GetAssetClass() != typev1.AssetClass_STOCK || groups[0].GetCurrency() != "USD" {
		t.Fatalf("expected the coverage row to supply the plugin hints, got %s %s",
			groups[0].GetAssetClass(), groups[0].GetCurrency())
	}
}

// Two venues can list one ticker, and they are two instruments with two groups.
func TestExportPrices_SplitsGroupsOnDomain(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		ListPriceCoverageForExport(gomock.Any()).
		Return(nil, nil)
	db.EXPECT().
		ListPricesForExport(gomock.Any()).
		Return([]dbpkg.ExportPriceRow{
			{
				IdentifierType: "MIC_TICKER", IdentifierValue: "VOD", IdentifierDomain: "XLON",
				PriceDate: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
				Close:     decimal.RequireFromString("70"),
			},
			{
				IdentifierType: "MIC_TICKER", IdentifierValue: "VOD", IdentifierDomain: "XNAS",
				PriceDate: time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC),
				Close:     decimal.RequireFromString("9"),
			},
		}, nil)
	stream := &exportPriceStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportPrices(&apiv1.ExportPricesRequest{}, stream); err != nil {
		t.Fatalf("ExportPrices: %v", err)
	}
	groups := stream.groups()
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].GetInstrument().GetDomain() != "XLON" || groups[1].GetInstrument().GetDomain() != "XNAS" {
		t.Fatalf("got domains %s and %s",
			groups[0].GetInstrument().GetDomain(), groups[1].GetInstrument().GetDomain())
	}
}

func TestExportPrices_Empty(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		ListPriceCoverageForExport(gomock.Any()).
		Return(nil, nil)
	db.EXPECT().
		ListPricesForExport(gomock.Any()).
		Return(nil, nil)
	stream := &exportPriceStreamMock{ctx: adminCtx("user-1", "sub|1")}
	err := srv.ExportPrices(&apiv1.ExportPricesRequest{}, stream)
	if err != nil {
		t.Fatalf("ExportPrices: %v", err)
	}
	// The envelope goes out even when there is nothing to say: an empty archive
	// is a statement, and a reader still has to see the version and the kind.
	if len(stream.sent) != 1 || stream.sent[0].GetEnvelope() == nil {
		t.Fatalf("expected the envelope alone, got %+v", stream.sent)
	}
}

func TestImportPrices_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ImportPrices(ctx, &apiv1.ImportPricesRequest{
		Prices: []*apiv1.ImportPriceRow{{
			IdentifierType: "ISIN", IdentifierValue: "US0378331005",
			PriceDate: "2024-01-15", Close: "100",
		}},
	})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestImportPrices_Empty_ReturnsError(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := adminCtx("user-1", "sub|1")
	_, err := srv.ImportPrices(ctx, &apiv1.ImportPricesRequest{})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestImportPrices_Success_CreatesJob(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	var enqueued bool
	srv.enqueueJob = func(jobID, jobType string) error {
		enqueued = true
		if jobType != "price" {
			t.Errorf("expected job type price, got %s", jobType)
		}
		return nil
	}
	db.EXPECT().
		CreateJob(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p dbpkg.CreateJobParams) (string, error) {
			if p.JobType != "price" {
				t.Errorf("expected job_type=price, got %s", p.JobType)
			}
			if p.UserID != "user-1" {
				t.Errorf("expected user_id=user-1, got %s", p.UserID)
			}
			if len(p.Payload) == 0 {
				t.Error("expected non-empty payload")
			}
			return "job-456", nil
		})
	ctx := adminCtx("user-1", "sub|1")
	resp, err := srv.ImportPrices(ctx, &apiv1.ImportPricesRequest{
		Prices: []*apiv1.ImportPriceRow{{
			IdentifierType:  "ISIN",
			IdentifierValue: "US0378331005",
			PriceDate:       "2024-01-15",
			Close:           "185.90",
		}},
	})
	if err != nil {
		t.Fatalf("ImportPrices: %v", err)
	}
	if resp.GetJobId() != "job-456" {
		t.Fatalf("expected job_id=job-456, got %s", resp.GetJobId())
	}
	if !enqueued {
		t.Fatal("expected job to be enqueued")
	}
}
