package api

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/archive"
	dbpkg "github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
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
	_, err := srv.ListInstruments(ctx, &apiv1.ListInstrumentsRequest{AssetClasses: []typev1.AssetClass{typev1.AssetClass_STOCK, typev1.AssetClass_ETF}})
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
	_, err := srv.ListInstruments(ctx, &apiv1.ListInstrumentsRequest{AssetClasses: []typev1.AssetClass{typev1.AssetClass_UNKNOWN}})
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

func TestExportInstruments_SendsEnvelopeFirst(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().ListInstrumentsForExport(gomock.Any(), "", []string(nil)).Return(nil, nil)
	stream := &exportStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportInstruments(&apiv1.ExportInstrumentsRequest{}, stream); err != nil {
		t.Fatalf("ExportInstruments: %v", err)
	}
	// The envelope goes out even when there is nothing to say: a file with an
	// empty instrument part means the export included it and there was nothing.
	if len(stream.sent) != 1 {
		t.Fatalf("expected exactly the envelope, got %d messages", len(stream.sent))
	}
	env := stream.sent[0].GetEnvelope()
	if env == nil {
		t.Fatal("first message is not the envelope")
	}
	if env.GetFormatVersion() != 1 || env.GetKind() != archivev1.ArchiveKind_SYSTEM {
		t.Fatalf("got format_version=%d kind=%v", env.GetFormatVersion(), env.GetKind())
	}
	if !env.GetExportedAt().IsValid() {
		t.Fatal("envelope carries no exported_at")
	}
}

func TestExportInstruments_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	rows := []*dbpkg.InstrumentRow{
		{ID: "id-1", Name: strPtr("Apple"), AssetClass: strPtr("STOCK"), ExchangeMIC: strPtr("XNAS"), Currency: strPtr("USD"),
			ContractMultiplier: decimal.NewFromInt(1),
			Identifiers: []dbpkg.IdentifierInput{
				{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS", Canonical: true},
				{Type: "BROKER_DESCRIPTION", Value: "APPLE INC", Domain: "IBKR", Canonical: false},
			}},
	}
	db.EXPECT().ListInstrumentsForExport(gomock.Any(), "", []string(nil)).Return(rows, nil)
	stream := &exportStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportInstruments(&apiv1.ExportInstrumentsRequest{}, stream); err != nil {
		t.Fatalf("ExportInstruments: %v", err)
	}
	got := stream.instruments()
	if len(got) != 1 {
		t.Fatalf("expected 1 instrument streamed, got %d", len(got))
	}
	inst := got[0]
	if inst.GetName() != "Apple" || inst.GetCurrency() != "USD" || inst.GetExchangeMic() != "XNAS" {
		t.Fatalf("got %v", inst)
	}
	if inst.GetAssetClass() != typev1.AssetClass_STOCK {
		t.Fatalf("asset_class = %v", inst.GetAssetClass())
	}
	if len(inst.GetIdentifiers()) != 2 {
		t.Fatalf("expected both identifiers, got %v", inst.GetIdentifiers())
	}
	if !inst.GetIdentifiers()[0].GetCanonical() || inst.GetIdentifiers()[1].GetCanonical() {
		t.Fatalf("canonical flags not carried: %v", inst.GetIdentifiers())
	}
	// A file names no server UUID, and an ordinary instrument states no
	// deliverable multiplier: absent means the column default of 1.
	if inst.ContractMultiplier != nil {
		t.Fatalf("expected no contract_multiplier, got %q", inst.GetContractMultiplier())
	}
}

func TestExportInstruments_CarriesWhatNothingRecomputes(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	expiry := time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)
	validFrom := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	identityAsOf := time.Date(2025, 6, 2, 14, 30, 0, 0, time.UTC)
	strike := decimal.RequireFromString("150.5")
	rows := []*dbpkg.InstrumentRow{
		{ID: "id-1", AssetClass: strPtr("OPTION"), Currency: strPtr("USD"),
			CIK: strPtr("0000320193"), SICCode: strPtr("3571"),
			ValidFrom: &validFrom, Expiry: &expiry, Strike: &strike, PutCall: strPtr("C"),
			ContractMultiplier:         decimal.RequireFromString("1.5"),
			IdentityAsOf:               &identityAsOf,
			Identifiers:                []dbpkg.IdentifierInput{{Type: "OCC", Value: "AAPL  260116C00150500", Canonical: true}},
			UnderlyingIdentifierType:   strPtr("MIC_TICKER"),
			UnderlyingIdentifierValue:  strPtr("AAPL"),
			UnderlyingIdentifierDomain: strPtr("XNAS"),
		},
	}
	db.EXPECT().ListInstrumentsForExport(gomock.Any(), "", []string(nil)).Return(rows, nil)
	stream := &exportStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportInstruments(&apiv1.ExportInstrumentsRequest{}, stream); err != nil {
		t.Fatalf("ExportInstruments: %v", err)
	}
	inst := stream.instruments()[0]
	if inst.GetCik() != "0000320193" || inst.GetSicCode() != "3571" {
		t.Fatalf("cik/sic_code dropped: %v", inst)
	}
	if inst.GetValidFrom() != "2024-03-01" || inst.ValidBefore != nil {
		t.Fatalf("validity interval wrong: from=%q before=%v", inst.GetValidFrom(), inst.ValidBefore)
	}
	if inst.GetStrike() != "150.5" || inst.GetExpiry() != "2026-01-16" || inst.GetPutCall() != "C" {
		t.Fatalf("option terms wrong: %v", inst)
	}
	if inst.GetContractMultiplier() != "1.5" {
		t.Fatalf("contract_multiplier = %q", inst.GetContractMultiplier())
	}
	if !inst.GetIdentityAsOf().AsTime().Equal(identityAsOf) {
		t.Fatalf("identity_as_of = %v", inst.GetIdentityAsOf().AsTime())
	}
	// The underlying is named by identifier, not nested and not by UUID.
	u := inst.GetUnderlying()
	if u.GetType() != typev1.IdentifierType_MIC_TICKER || u.GetValue() != "AAPL" || u.GetDomain() != "XNAS" {
		t.Fatalf("underlying ref = %v", u)
	}
}

func TestExportInstruments_WithExchangeFilter(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().ListInstrumentsForExport(gomock.Any(), "XNAS", []string(nil)).Return(nil, nil)
	stream := &exportStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportInstruments(&apiv1.ExportInstrumentsRequest{Exchange: "XNAS"}, stream); err != nil {
		t.Fatalf("ExportInstruments: %v", err)
	}
	if got := stream.instruments(); len(got) != 0 {
		t.Fatalf("expected 0 instruments, got %d", len(got))
	}
}

func TestExportInstruments_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	stream := &exportStreamMock{ctx: authCtx("user-1", "sub|1")}
	err := srv.ExportInstruments(&apiv1.ExportInstrumentsRequest{}, stream)
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

// systemInstrumentPart wraps instruments as a system archive's instrument part,
// with the envelope a file carries in.
func systemInstrumentPart(insts ...*archivev1.Instrument) *apiv1.ImportInstrumentsRequest {
	return &apiv1.ImportInstrumentsRequest{
		Envelope:    archive.NewEnvelope("portfoliodb.example.com", archivev1.ArchiveKind_SYSTEM),
		Instruments: &archivev1.InstrumentPart{Instruments: insts},
	}
}

func TestImportInstruments_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	req := systemInstrumentPart(&archivev1.Instrument{
		Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_ISIN, Value: "x", Canonical: true}},
	})
	_, err := srv.ImportInstruments(authCtx("user-1", "sub|1"), req)
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestImportInstruments_Empty_ReturnsError(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.ImportInstruments(adminCtx("user-1", "sub|1"), systemInstrumentPart())
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestImportInstruments_NewerFormatVersion_Refused(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	req := systemInstrumentPart(&archivev1.Instrument{
		Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_ISIN, Value: "x", Canonical: true}},
	})
	req.Envelope.FormatVersion = archive.FormatVersion + 1
	// The request is well formed and this server is the thing that is out of
	// date, so this is a precondition rather than a bad argument.
	_, err := srv.ImportInstruments(adminCtx("user-1", "sub|1"), req)
	testutil.RequireGRPCCode(t, err, codes.FailedPrecondition)
}

func TestImportInstruments_UserArchive_Refused(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	req := systemInstrumentPart(&archivev1.Instrument{
		Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_ISIN, Value: "x", Canonical: true}},
	})
	req.Envelope.Kind = archivev1.ArchiveKind_USER
	_, err := srv.ImportInstruments(adminCtx("user-1", "sub|1"), req)
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}
