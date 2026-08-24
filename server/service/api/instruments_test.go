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
				{
					Ref:       dbpkg.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
					Canonical: true,
				},
				{
					Ref:       dbpkg.InstrumentRef{Type: "ISIN", Value: "US0378331005"},
					Canonical: true,
				}}},
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

func TestExportSystemArchive_Instruments_SendsEnvelopeFirst(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().ListInstrumentsForExport(gomock.Any(), "", []string(nil)).Return(nil, nil)
	stream := &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportSystemArchive(instrumentExportReq(), stream); err != nil {
		t.Fatalf("ExportInstruments: %v", err)
	}
	// The envelope goes out even when there is nothing to say: a file with an
	// empty instrument part means the export included it and there was nothing.
	// The envelope and the part marker go out even when there is nothing to
	// say: a part present and empty means the export included it and there
	// was nothing, which a reader has to tell apart from a part left out.
	if len(stream.sent) != 2 {
		t.Fatalf("expected the envelope and the part marker, got %d messages", len(stream.sent))
	}
	if stream.sent[1].GetPartBegin().GetPart() != archivev1.ArchivePart_INSTRUMENTS {
		t.Fatalf("expected the part marker second, got %+v", stream.sent[1])
	}
	env := stream.sent[0].GetEnvelope()
	if env == nil {
		t.Fatal("first message is not the envelope")
	}
	if env.GetFormatVersion() != archive.FormatVersion || env.GetKind() != archivev1.ArchiveKind_SYSTEM {
		t.Fatalf("got format_version=%d kind=%v", env.GetFormatVersion(), env.GetKind())
	}
	if !env.GetExportedAt().IsValid() {
		t.Fatal("envelope carries no exported_at")
	}
}

func TestExportSystemArchive_Instruments_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	rows := []*dbpkg.InstrumentRow{
		{ID: "id-1", Name: strPtr("Apple"), AssetClass: strPtr("STOCK"),
			ContractMultiplier: decimal.NewFromInt(1),
			Identifiers: []dbpkg.IdentifierInput{
				{
					Ref:       dbpkg.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "APPLE INC", Domain: "IBKR"},
					Canonical: false,
				}},
			// The ticker names a line, so it is written on the line rather than
			// beside the description.
			Listings: []*dbpkg.Listing{{
				ID: "line-1", Currency: "USD",
				Identifiers: []dbpkg.IdentifierInput{{
					Ref:       dbpkg.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
					Canonical: true,
				}},
			}}},
	}
	db.EXPECT().ListInstrumentsForExport(gomock.Any(), "", []string(nil)).Return(rows, nil)
	stream := &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportSystemArchive(instrumentExportReq(), stream); err != nil {
		t.Fatalf("ExportInstruments: %v", err)
	}
	got := stream.instruments()
	if len(got) != 1 {
		t.Fatalf("expected 1 instrument streamed, got %d", len(got))
	}
	inst := got[0]
	if inst.GetName() != "Apple" {
		t.Fatalf("got %v", inst)
	}
	if inst.GetAssetClass() != typev1.AssetClass_STOCK {
		t.Fatalf("asset_class = %v", inst.GetAssetClass())
	}
	// The currency is a fact about a line, so it is on the line and there is no
	// instrument-level one to read.
	if len(inst.GetListings()) != 1 || inst.GetListings()[0].GetCurrency() != "USD" {
		t.Fatalf("listings = %v", inst.GetListings())
	}
	// Each name at its own grain: the description on the security, the ticker on
	// the line it names.
	if len(inst.GetIdentifiers()) != 1 || inst.GetIdentifiers()[0].GetCanonical() {
		t.Fatalf("security identifiers = %v, want the description alone", inst.GetIdentifiers())
	}
	lineIdns := inst.GetListings()[0].GetIdentifiers()
	if len(lineIdns) != 1 || lineIdns[0].GetValue() != "AAPL" || !lineIdns[0].GetCanonical() {
		t.Fatalf("listing identifiers = %v, want the ticker", lineIdns)
	}
	// A file names no server UUID, and an ordinary instrument states no
	// deliverable multiplier: absent means the column default of 1.
	if inst.ContractMultiplier != nil {
		t.Fatalf("expected no contract_multiplier, got %q", inst.GetContractMultiplier())
	}
}

func TestExportSystemArchive_Instruments_CarriesWhatNothingRecomputes(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	expiry := time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)
	validFrom := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	namedFrom := time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC)
	strike := decimal.RequireFromString("150.5")
	rows := []*dbpkg.InstrumentRow{
		{ID: "id-1", AssetClass: strPtr("OPTION"),
			CIK: strPtr("0000320193"), SICCode: strPtr("3571"),
			Expiry: &expiry, Strike: &strike, PutCall: strPtr("C"),
			ContractMultiplier: decimal.RequireFromString("1.5"),
			// The tradability window is a fact about the line, and the security's
			// is the hull of its lines'.
			Listings: []*dbpkg.Listing{{ID: "line-1", Currency: "USD", ValidFrom: &validFrom}},
			// The symbol the contract traded under before a split, and the one
			// it wears now. Both travel, or a file exported before the split
			// would name a symbol the importing instance has never heard of.
			Identifiers: []dbpkg.IdentifierInput{
				{
					Ref:         dbpkg.InstrumentRef{Type: "OCC", Value: "AAPL260116C00301000"},
					Canonical:   true,
					ValidBefore: &namedFrom,
				},
				{
					Ref:       dbpkg.InstrumentRef{Type: "OCC", Value: "AAPL260116C00150500"},
					Canonical: true,
					ValidFrom: &namedFrom,
				}},
			Underlying:         &dbpkg.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			UnderlyingCurrency: "USD",
		},
	}
	db.EXPECT().ListInstrumentsForExport(gomock.Any(), "", []string(nil)).Return(rows, nil)
	stream := &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportSystemArchive(instrumentExportReq(), stream); err != nil {
		t.Fatalf("ExportInstruments: %v", err)
	}
	inst := stream.instruments()[0]
	if inst.GetCik() != "0000320193" || inst.GetSicCode() != "3571" {
		t.Fatalf("cik/sic_code dropped: %v", inst)
	}
	if len(inst.GetListings()) != 1 {
		t.Fatalf("listings = %v", inst.GetListings())
	}
	line := inst.GetListings()[0]
	if line.GetValidFrom() != "2024-03-01" || line.ValidBefore != nil {
		t.Fatalf("validity interval wrong: from=%q before=%v", line.GetValidFrom(), line.ValidBefore)
	}
	if inst.GetStrike() != "150.5" || inst.GetExpiry() != "2026-01-16" || inst.GetPutCall() != "C" {
		t.Fatalf("option terms wrong: %v", inst)
	}
	if inst.GetContractMultiplier() != "1.5" {
		t.Fatalf("contract_multiplier = %q", inst.GetContractMultiplier())
	}
	idns := inst.GetIdentifiers()
	if len(idns) != 2 {
		t.Fatalf("identifiers = %v, want both the name given up and the one in force", idns)
	}
	if idns[0].GetValidBefore() != "2024-06-10" || idns[0].ValidFrom != nil {
		t.Fatalf("the given-up name = %v", idns[0])
	}
	if idns[1].GetValidFrom() != "2024-06-10" || idns[1].ValidBefore != nil {
		t.Fatalf("the name in force = %v", idns[1])
	}
	// The underlying is named by identifier, not nested and not by UUID -- and by
	// the line the contract delivers, which is the identifier plus the currency.
	u := inst.GetUnderlying()
	if u.GetType() != typev1.IdentifierType_MIC_TICKER || u.GetValue() != "AAPL" || u.GetDomain() != "XNAS" {
		t.Fatalf("underlying ref = %v", u)
	}
	if u.GetCurrency() != "USD" {
		t.Fatalf("underlying names no line: %v", u)
	}
}

// The recorded output of the identifier lookups is the reason the archive
// exists, so an export that dropped it would make a rebuild pay for those
// lookups a second time.
func TestExportSystemArchive_Instruments_CarriesProviderIdentifiers(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	rows := []*dbpkg.InstrumentRow{
		{ID: "id-1", AssetClass: strPtr("STOCK"), Currency: strPtr("USD"),
			ContractMultiplier: decimal.NewFromInt(1),
			Identifiers: []dbpkg.IdentifierInput{{
				Ref:       dbpkg.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
				Canonical: true,
			}},
			ProviderIdentifiers: []dbpkg.ProviderIdentifierInput{
				{Provider: "eodhd", Type: "EODHD_EXCH_CODE", Value: "US"},
				{Provider: "openfigi", Type: "FIGI", Domain: "XNAS", Value: "BBG000B9XRY4"},
			}},
	}
	db.EXPECT().ListInstrumentsForExport(gomock.Any(), "", []string(nil)).Return(rows, nil)
	stream := &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportSystemArchive(instrumentExportReq(), stream); err != nil {
		t.Fatalf("ExportSystemArchive: %v", err)
	}
	pis := stream.instruments()[0].GetProviderIdentifiers()
	if len(pis) != 2 {
		t.Fatalf("expected both provider identifiers, got %v", pis)
	}
	if pis[0].GetProvider() != "eodhd" || pis[0].GetIdentifierType() != "EODHD_EXCH_CODE" || pis[0].GetValue() != "US" {
		t.Fatalf("first provider identifier = %v", pis[0])
	}
	// The provider's own vocabulary, and the domain that scopes it: an
	// unscoped FIGI addresses a different instrument from a scoped one.
	if pis[1].GetDomain() != "XNAS" || pis[1].GetValue() != "BBG000B9XRY4" {
		t.Fatalf("second provider identifier = %v", pis[1])
	}
}

func TestExportSystemArchive_Instruments_WithExchangeFilter(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().ListInstrumentsForExport(gomock.Any(), "XNAS", []string(nil)).Return(nil, nil)
	stream := &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportSystemArchive(&apiv1.ExportSystemArchiveRequest{Parts: []archivev1.ArchivePart{archivev1.ArchivePart_INSTRUMENTS}, Exchange: "XNAS"}, stream); err != nil {
		t.Fatalf("ExportInstruments: %v", err)
	}
	if got := stream.instruments(); len(got) != 0 {
		t.Fatalf("expected 0 instruments, got %d", len(got))
	}
}

func TestExportSystemArchive_Instruments_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	stream := &exportArchiveStreamMock{ctx: authCtx("user-1", "sub|1")}
	err := srv.ExportSystemArchive(instrumentExportReq(), stream)
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

// instrumentExportReq asks for the instrument part only, which is what these
// tests are about.
func instrumentExportReq() *apiv1.ExportSystemArchiveRequest {
	return &apiv1.ExportSystemArchiveRequest{Parts: []archivev1.ArchivePart{archivev1.ArchivePart_INSTRUMENTS}}
}

// A security's currency lines reach the wire, each with the interval it was
// tradeable in. A line carries a currency or does not exist, so the absence of a
// listing rather than a listing without a currency is how "nothing has named a
// line for this security" arrives.
func TestInstrumentRowToProto_CarriesListings(t *testing.T) {
	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	row := &dbpkg.InstrumentRow{
		ID: "id-1",
		Listings: []*dbpkg.Listing{
			{ID: "lst-1", InstrumentID: "id-1", Currency: "GBX", ValidFrom: &from, ValidBefore: &before},
			{ID: "lst-2", InstrumentID: "id-1", Currency: "USD"},
		},
	}

	got := instrumentRowToProto(row)
	if len(got.GetListings()) != 2 {
		t.Fatalf("listings = %+v, want 2", got.GetListings())
	}
	first := got.GetListings()[0]
	if first.GetId() != "lst-1" || first.GetCurrency() != "GBX" {
		t.Errorf("first listing = %+v, want lst-1 in GBX", first)
	}
	if first.GetValidFrom() != "2020-01-01" || first.GetValidBefore() != "2024-06-01" {
		t.Errorf("first listing interval = [%s, %s), want [2020-01-01, 2024-06-01)", first.GetValidFrom(), first.GetValidBefore())
	}
	second := got.GetListings()[1]
	if second.GetCurrency() != "USD" {
		t.Errorf("second listing currency = %q, want USD", second.GetCurrency())
	}
	if second.ValidFrom != nil || second.ValidBefore != nil {
		t.Errorf("second listing interval = [%v, %v), want both unset", second.ValidFrom, second.ValidBefore)
	}
}

// A listing-grain name the instance could not place rides on the security, in a
// field of its own: a file has to be able to say "this ticker names a line of
// this security and nothing said which", which is what the instance records. See
// docs/adr/0075-a-name-that-could-not-be-placed-names-no-line.md.
func TestArchiveInstrument_CarriesNamesThatNameNoLine(t *testing.T) {
	row := &dbpkg.InstrumentRow{
		ID: "id-1",
		Identifiers: []dbpkg.IdentifierInput{{
			Ref: dbpkg.InstrumentRef{Type: "ISIN", Value: "GB00UNPLACED1"}, Canonical: true,
		}},
		UnplacedIdentifiers: []dbpkg.IdentifierInput{{
			Ref: dbpkg.InstrumentRef{Type: "MIC_TICKER", Value: "UNPL", Domain: "XLON"}, Canonical: true,
		}},
		UnplacedProviderIdentifiers: []dbpkg.ProviderIdentifierInput{{
			Provider: "eodhd", Type: "EODHD_EXCH_CODE", Value: "LSE",
		}},
	}
	got := archiveInstrument(row)
	if len(got.GetListings()) != 0 {
		t.Errorf("listings = %+v, want none", got.GetListings())
	}
	if len(got.GetIdentifiers()) != 1 || got.GetIdentifiers()[0].GetValue() != "GB00UNPLACED1" {
		t.Errorf("identifiers = %+v, want the ISIN alone", got.GetIdentifiers())
	}
	if len(got.GetUnplacedIdentifiers()) != 1 || got.GetUnplacedIdentifiers()[0].GetValue() != "UNPL" {
		t.Errorf("unplaced identifiers = %+v, want the ticker", got.GetUnplacedIdentifiers())
	}
	if len(got.GetUnplacedProviderIdentifiers()) != 1 {
		t.Errorf("unplaced provider identifiers = %+v, want the exchange code", got.GetUnplacedProviderIdentifiers())
	}
}

// A security with no listings sends none rather than an empty message.
func TestInstrumentRowToProto_NoListings(t *testing.T) {
	got := instrumentRowToProto(&dbpkg.InstrumentRow{ID: "id-1"})
	if got.GetListings() != nil {
		t.Errorf("listings = %+v, want nil", got.GetListings())
	}
}

// Each name reaches the wire at the grain it names, rather than flattened into
// one list per instrument. A surface showing a name has picked a grain, and the
// flat list could not tell it which a row was at: a ticker naming the GBP line
// and one naming the USD line arrived indistinguishable.
func TestInstrumentRowToProto_IdentifiersAtTheirGrain(t *testing.T) {
	row := &dbpkg.InstrumentRow{
		ID: "id-1",
		Identifiers: []dbpkg.IdentifierInput{{
			Ref: dbpkg.InstrumentRef{Type: "ISIN", Value: "GB0000000001"}, Canonical: true,
		}},
		Listings: []*dbpkg.Listing{
			{ID: "lst-1", InstrumentID: "id-1", Currency: "GBP", Identifiers: []dbpkg.IdentifierInput{{
				Ref: dbpkg.InstrumentRef{Type: "MIC_TICKER", Value: "VOD", Domain: "XLON"}, Canonical: true,
			}}},
			{ID: "lst-2", InstrumentID: "id-1", Currency: "USD", Identifiers: []dbpkg.IdentifierInput{{
				Ref: dbpkg.InstrumentRef{Type: "MIC_TICKER", Value: "VOD.US", Domain: "XNAS"}, Canonical: true,
			}}},
		},
		UnplacedIdentifiers: []dbpkg.IdentifierInput{{
			Ref: dbpkg.InstrumentRef{Type: "SEDOL", Value: "B16GWD5"}, Canonical: true,
		}},
	}

	got := instrumentRowToProto(row)
	if len(got.GetIdentifiers()) != 1 || got.GetIdentifiers()[0].GetValue() != "GB0000000001" {
		t.Errorf("security identifiers = %+v, want the ISIN alone", got.GetIdentifiers())
	}
	for i, want := range []string{"VOD", "VOD.US"} {
		idns := got.GetListings()[i].GetIdentifiers()
		if len(idns) != 1 || idns[0].GetValue() != want {
			t.Errorf("listing %d identifiers = %+v, want %s", i, idns, want)
		}
	}
	// A name nobody could place names the security and no line of it, which is
	// neither of the other two claims.
	if len(got.GetUnplacedIdentifiers()) != 1 || got.GetUnplacedIdentifiers()[0].GetValue() != "B16GWD5" {
		t.Errorf("unplaced identifiers = %+v, want the SEDOL", got.GetUnplacedIdentifiers())
	}
}
