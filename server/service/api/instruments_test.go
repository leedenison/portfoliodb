package api

import (
	"context"
	"testing"

	dbpkg "github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

func strPtr(s string) *string { return &s }

func TestListInstruments_Unauthenticated(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.ListInstruments(context.Background(), &apiv1.ListInstrumentsRequest{})
	testutil.RequireGRPCCode(t, err, codes.Unauthenticated)
}

func TestListInstruments_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	rows := []*dbpkg.InstrumentRow{
		{ID: "id-1", Name: strPtr("Apple"), AssetClass: strPtr("STOCK"), ExchangeMIC: strPtr("XNAS"), Currency: strPtr("USD"),
			Identifiers: []dbpkg.IdentifierInput{
				{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS", Canonical: true},
				{Type: "ISIN", Value: "US0378331005", Canonical: true},
			}},
	}
	db.EXPECT().
		ListInstruments(gomock.Any(), "", []string(nil), int32(30), "").
		Return(rows, int32(1), "", nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.ListInstruments(ctx, &apiv1.ListInstrumentsRequest{})
	if err != nil {
		t.Fatalf("ListInstruments: %v", err)
	}
	if len(resp.GetInstruments()) != 1 {
		t.Fatalf("expected 1 instrument, got %d", len(resp.GetInstruments()))
	}
	inst := resp.GetInstruments()[0]
	if inst.GetId() != "id-1" || inst.GetName() != "Apple" {
		t.Fatalf("got %v", inst)
	}
	if resp.GetTotalCount() != 1 {
		t.Fatalf("expected total_count=1, got %d", resp.GetTotalCount())
	}
}

func TestListInstruments_WithSearch(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		ListInstruments(gomock.Any(), "AAPL", []string(nil), int32(30), "").
		Return(nil, int32(0), "", nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.ListInstruments(ctx, &apiv1.ListInstrumentsRequest{Search: "AAPL"})
	if err != nil {
		t.Fatalf("ListInstruments: %v", err)
	}
	if len(resp.GetInstruments()) != 0 {
		t.Fatalf("expected 0 instruments, got %d", len(resp.GetInstruments()))
	}
}

func TestListInstruments_PageSizeClamping(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		ListInstruments(gomock.Any(), "", []string(nil), int32(100), "").
		Return(nil, int32(0), "", nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ListInstruments(ctx, &apiv1.ListInstrumentsRequest{PageSize: 200})
	if err != nil {
		t.Fatalf("ListInstruments: %v", err)
	}
}

func TestListInstruments_AssetClassFilter(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		ListInstruments(gomock.Any(), "", []string{"STOCK", "ETF"}, int32(30), "").
		Return(nil, int32(0), "", nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ListInstruments(ctx, &apiv1.ListInstrumentsRequest{AssetClasses: []string{"STOCK", "ETF"}})
	if err != nil {
		t.Fatalf("ListInstruments: %v", err)
	}
}

func TestListInstruments_UnknownAssetClassFilter(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		ListInstruments(gomock.Any(), "", []string{"UNKNOWN"}, int32(30), "").
		Return(nil, int32(0), "", nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ListInstruments(ctx, &apiv1.ListInstrumentsRequest{AssetClasses: []string{"UNKNOWN"}})
	if err != nil {
		t.Fatalf("ListInstruments: %v", err)
	}
}

func TestListInstruments_DBError(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		ListInstruments(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, int32(0), "", context.DeadlineExceeded)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ListInstruments(ctx, &apiv1.ListInstrumentsRequest{})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

func TestExportInstruments_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	rows := []*dbpkg.InstrumentRow{
		{ID: "id-1", Name: strPtr("Apple"), Identifiers: []dbpkg.IdentifierInput{{Type: "ISIN", Value: "US0378331005", Canonical: true}}},
	}
	db.EXPECT().
		ListInstrumentsForExport(gomock.Any(), "").
		Return(rows, nil)
	stream := &exportStreamMock{ctx: adminCtx("user-1", "sub|1")}
	err := srv.ExportInstruments(&apiv1.ExportInstrumentsRequest{}, stream)
	if err != nil {
		t.Fatalf("ExportInstruments: %v", err)
	}
	if len(stream.sent) != 1 {
		t.Fatalf("expected 1 instrument streamed, got %d", len(stream.sent))
	}
	if stream.sent[0].GetId() != "id-1" || stream.sent[0].GetName() != "Apple" {
		t.Fatalf("got %v", stream.sent[0])
	}
	if len(stream.sent[0].GetIdentifiers()) != 1 || !stream.sent[0].GetIdentifiers()[0].GetCanonical() {
		t.Fatalf("expected one canonical identifier, got %v", stream.sent[0].GetIdentifiers())
	}
}

func TestExportInstruments_WithExchangeFilter(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		ListInstrumentsForExport(gomock.Any(), "XNAS").
		Return(nil, nil)
	stream := &exportStreamMock{ctx: adminCtx("user-1", "sub|1")}
	err := srv.ExportInstruments(&apiv1.ExportInstrumentsRequest{Exchange: "XNAS"}, stream)
	if err != nil {
		t.Fatalf("ExportInstruments: %v", err)
	}
	if len(stream.sent) != 0 {
		t.Fatalf("expected 0 instruments, got %d", len(stream.sent))
	}
}

func TestExportInstruments_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	stream := &exportStreamMock{ctx: authCtx("user-1", "sub|1")}
	err := srv.ExportInstruments(&apiv1.ExportInstrumentsRequest{}, stream)
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestImportInstruments_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ImportInstruments(ctx, &apiv1.ImportInstrumentsRequest{Instruments: []*apiv1.Instrument{{Identifiers: []*apiv1.InstrumentIdentifier{{Type: apiv1.IdentifierType_ISIN, Value: "x", Canonical: true}}}}})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestImportInstruments_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple Inc.", gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []dbpkg.IdentifierInput, _ string, _, _ interface{}) (string, error) {
			if len(idns) < 2 {
				t.Errorf("expected at least 2 identifiers, got %d", len(idns))
			}
			return "inst-1", nil
		})
	ctx := adminCtx("user-1", "sub|1")
	req := &apiv1.ImportInstrumentsRequest{
		Instruments: []*apiv1.Instrument{{
			AssetClass: "STOCK", Exchange: "XNAS", Currency: "USD", Name: "Apple Inc.",
			Identifiers: []*apiv1.InstrumentIdentifier{
				{Type: apiv1.IdentifierType_ISIN, Value: "US0378331005", Canonical: true},
				{Type: apiv1.IdentifierType_BROKER_DESCRIPTION, Domain: "IBKR", Value: "AAPL", Canonical: false},
			},
		}},
	}
	resp, err := srv.ImportInstruments(ctx, req)
	if err != nil {
		t.Fatalf("ImportInstruments: %v", err)
	}
	if resp.GetEnsuredCount() != 1 || len(resp.GetErrors()) != 0 {
		t.Fatalf("ensured_count=1, errors empty; got ensured_count=%d, errors=%d", resp.GetEnsuredCount(), len(resp.GetErrors()))
	}
}

func TestImportInstruments_EmptyIdentifiers(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := adminCtx("user-1", "sub|1")
	req := &apiv1.ImportInstrumentsRequest{
		Instruments: []*apiv1.Instrument{{Id: "x", Identifiers: nil}},
	}
	resp, err := srv.ImportInstruments(ctx, req)
	if err != nil {
		t.Fatalf("ImportInstruments: %v", err)
	}
	if resp.GetEnsuredCount() != 0 || len(resp.GetErrors()) != 1 {
		t.Fatalf("expected 1 error, 0 ensured; got ensured=%d, errors=%d", resp.GetEnsuredCount(), len(resp.GetErrors()))
	}
	if resp.GetErrors()[0].GetMessage() != "at least one identifier required" {
		t.Fatalf("got error %q", resp.GetErrors()[0].GetMessage())
	}
}

func TestImportInstruments_DuplicateTypeValueInPayload(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	// First instrument (ISIN 1) is ensured; second is rejected as duplicate (type, value) in payload.
	db.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "", "", "", gomock.Any(), "", nil, nil).
		Return("inst-1", nil)
	ctx := adminCtx("user-1", "sub|1")
	req := &apiv1.ImportInstrumentsRequest{
		Instruments: []*apiv1.Instrument{
			{Identifiers: []*apiv1.InstrumentIdentifier{{Type: apiv1.IdentifierType_ISIN, Value: "1", Canonical: true}}},
			{Identifiers: []*apiv1.InstrumentIdentifier{{Type: apiv1.IdentifierType_ISIN, Value: "1", Canonical: true}}},
		},
	}
	resp, err := srv.ImportInstruments(ctx, req)
	if err != nil {
		t.Fatalf("ImportInstruments: %v", err)
	}
	if resp.GetEnsuredCount() != 1 || len(resp.GetErrors()) != 1 {
		t.Fatalf("expected 1 ensured and 1 error (duplicate); got ensured=%d, errors=%d", resp.GetEnsuredCount(), len(resp.GetErrors()))
	}
}
