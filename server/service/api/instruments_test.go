package api

import (
	"context"
	"testing"

	dbpkg "github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

func TestExportInstruments_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	rows := []*dbpkg.InstrumentRow{
		{ID: "id-1", Name: "Apple", Identifiers: []dbpkg.IdentifierInput{{Type: "ISIN", Value: "US0378331005", Canonical: true}}},
	}
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		ListInstrumentsForExport(gomock.Any(), "").
		Return(rows, nil)
	srv := NewServer(db)
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
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		ListInstrumentsForExport(gomock.Any(), "XNAS").
		Return(nil, nil)
	srv := NewServer(db)
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
	srv := NewServer(mock.NewMockDB(gomock.NewController(t)))
	stream := &exportStreamMock{ctx: authCtx("user-1", "sub|1")}
	err := srv.ExportInstruments(&apiv1.ExportInstrumentsRequest{}, stream)
	requireGRPCCode(t, err, codes.PermissionDenied)
}

func TestImportInstruments_NonAdmin_PermissionDenied(t *testing.T) {
	srv := NewServer(mock.NewMockDB(gomock.NewController(t)))
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ImportInstruments(ctx, &apiv1.ImportInstrumentsRequest{Instruments: []*apiv1.Instrument{{Identifiers: []*apiv1.InstrumentIdentifier{{Type: "ISIN", Value: "x", Canonical: true}}}}})
	requireGRPCCode(t, err, codes.PermissionDenied)
}

func TestImportInstruments_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		EnsureInstrument(gomock.Any(), "equity", "XNAS", "USD", "Apple Inc.", gomock.Any(), "", nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _ string, idns []dbpkg.IdentifierInput, _ string, _, _ interface{}) (string, error) {
			if len(idns) < 2 {
				t.Errorf("expected at least 2 identifiers, got %d", len(idns))
			}
			return "inst-1", nil
		})
	srv := NewServer(db)
	ctx := adminCtx("user-1", "sub|1")
	req := &apiv1.ImportInstrumentsRequest{
		Instruments: []*apiv1.Instrument{{
			AssetClass: "equity", Exchange: "XNAS", Currency: "USD", Name: "Apple Inc.",
			Identifiers: []*apiv1.InstrumentIdentifier{
				{Type: "ISIN", Value: "US0378331005", Canonical: true},
				{Type: "IBKR", Value: "AAPL", Canonical: false},
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
	srv := NewServer(mock.NewMockDB(gomock.NewController(t)))
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
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	// First instrument (ISIN 1) is ensured; second is rejected as duplicate (type, value) in payload.
	db.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "", gomock.Any(), "", nil, nil).
		Return("inst-1", nil)
	srv := NewServer(db)
	ctx := adminCtx("user-1", "sub|1")
	req := &apiv1.ImportInstrumentsRequest{
		Instruments: []*apiv1.Instrument{
			{Identifiers: []*apiv1.InstrumentIdentifier{{Type: "ISIN", Value: "1", Canonical: true}}},
			{Identifiers: []*apiv1.InstrumentIdentifier{{Type: "ISIN", Value: "1", Canonical: true}}},
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
