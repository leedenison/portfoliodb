package api

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	dbpkg "github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
)

// exportUserStreamMock captures a whole user archive export.
type exportUserStreamMock struct {
	ctx  context.Context
	sent []*apiv1.ExportUserArchiveResponse
}

func (e *exportUserStreamMock) Context() context.Context    { return e.ctx }
func (e *exportUserStreamMock) RecvMsg(m interface{}) error { return nil }
func (e *exportUserStreamMock) Send(m *apiv1.ExportUserArchiveResponse) error {
	e.sent = append(e.sent, m)
	return nil
}
func (e *exportUserStreamMock) SendHeader(m metadata.MD) error { return nil }
func (e *exportUserStreamMock) SetHeader(m metadata.MD) error  { return nil }
func (e *exportUserStreamMock) SetTrailer(m metadata.MD)       {}
func (e *exportUserStreamMock) SendMsg(m interface{}) error {
	if r, ok := m.(*apiv1.ExportUserArchiveResponse); ok {
		e.sent = append(e.sent, r)
	}
	return nil
}

// shape names the stream's messages in order, so a test can assert on the
// grammar rather than on the contents.
func (e *exportUserStreamMock) shape() []string {
	var out []string
	for _, m := range e.sent {
		switch {
		case m.GetEnvelope() != nil:
			out = append(out, "envelope")
		case m.GetPartBegin() != nil:
			out = append(out, "begin:"+m.GetPartBegin().GetPart().String())
		case m.GetPreferences() != nil:
			out = append(out, "preferences")
		case m.GetTxWindow() != nil:
			out = append(out, "tx_window:"+m.GetTxWindow().GetBroker().String())
		}
	}
	return out
}

func (e *exportUserStreamMock) preferences() *archivev1.PreferencePart {
	for _, m := range e.sent {
		if v := m.GetPreferences(); v != nil {
			return v
		}
	}
	return nil
}

// exportPreferences runs a preferences-only export over the given stored state.
func exportPreferences(t *testing.T, currency string, rules []dbpkg.IgnoredAssetClass) *exportUserStreamMock {
	t.Helper()
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().GetDisplayCurrency(gomock.Any(), "user-1").Return(currency, nil)
	mockDB.EXPECT().ListIgnoredAssetClasses(gomock.Any(), "user-1").Return(rules, nil)
	stream := &exportUserStreamMock{ctx: authCtx("user-1", "sub|1")}
	if err := srv.ExportUserArchive(&apiv1.ExportUserArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_PREFERENCES},
	}, stream); err != nil {
		t.Fatalf("ExportUserArchive: %v", err)
	}
	return stream
}

// The envelope is sent once and first, so exported_at is one reading of the
// clock for the whole document, and the part_begin marker precedes the part
// even though the part travels as a single message.
func TestExportUserArchive_StreamGrammar(t *testing.T) {
	stream := exportPreferences(t, "GBP", nil)
	want := []string{"envelope", "begin:PREFERENCES", "preferences"}
	if got := stream.shape(); !equalStrings(got, want) {
		t.Fatalf("stream = %v, want %v", got, want)
	}
	env := stream.sent[0].GetEnvelope()
	if env.GetKind() != archivev1.ArchiveKind_USER {
		t.Fatalf("kind = %s, want USER", env.GetKind())
	}
	if env.GetExportedAt() == nil {
		t.Fatal("envelope carries no exported_at")
	}
}

// Broker and asset class are enums in the file and strings in the column, so
// the export has to spell them the archive's way.
func TestExportUserArchive_Preferences_SpellsTheEnums(t *testing.T) {
	stream := exportPreferences(t, "GBP", []dbpkg.IgnoredAssetClass{
		{Broker: "IBKR", Account: "U123", AssetClass: "OPTION"},
	})
	part := stream.preferences()
	if part.GetDisplayCurrency() != "GBP" {
		t.Fatalf("display_currency = %q", part.GetDisplayCurrency())
	}
	rules := part.GetIgnoredAssetClasses().GetRules()
	if len(rules) != 1 {
		t.Fatalf("rules = %v, want 1", rules)
	}
	if rules[0].GetBroker() != typev1.Broker_IBKR {
		t.Fatalf("broker = %s", rules[0].GetBroker())
	}
	if rules[0].GetAssetClass() != typev1.AssetClass_OPTION {
		t.Fatalf("asset_class = %s", rules[0].GetAssetClass())
	}
	if rules[0].GetAccount() != "U123" {
		t.Fatalf("account = %q", rules[0].GetAccount())
	}
}

// A user with no rules has an empty set rather than an unstated one. The empty
// container is what says so: an absent one would tell an importer to leave the
// stored rules alone, which is the opposite instruction.
func TestExportUserArchive_Preferences_NoRulesIsPresentAndEmpty(t *testing.T) {
	part := exportPreferences(t, "USD", nil).preferences()
	if part.IgnoredAssetClasses == nil {
		t.Fatal("ignored_asset_classes absent, want present and empty")
	}
	if len(part.GetIgnoredAssetClasses().GetRules()) != 0 {
		t.Fatalf("rules = %v, want none", part.GetIgnoredAssetClasses().GetRules())
	}
}

// A rule naming a broker this build has no enum value for is dropped: no
// importer could apply it, and BROKER_UNSPECIFIED would fail validation on the
// way back in and take the whole setting with it.
func TestExportUserArchive_Preferences_SkipsUnmappableRules(t *testing.T) {
	part := exportPreferences(t, "GBP", []dbpkg.IgnoredAssetClass{
		{Broker: "DEFUNCTBROKER", Account: "", AssetClass: "STOCK"},
		{Broker: "FIDELITY", Account: "", AssetClass: "OPTION"},
	}).preferences()
	rules := part.GetIgnoredAssetClasses().GetRules()
	if len(rules) != 1 || rules[0].GetBroker() != typev1.Broker_FIDELITY {
		t.Fatalf("rules = %v, want only the FIDELITY rule", rules)
	}
}

func TestExportUserArchive_Unauthenticated(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	err := srv.ExportUserArchive(&apiv1.ExportUserArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_PREFERENCES},
	}, &exportUserStreamMock{ctx: ctxNoAuth()})
	testutil.RequireGRPCCode(t, err, codes.Unauthenticated)
}

// The two menus are one enum, so a request can name a part from either. Asking
// the user export for a system part is refused rather than quietly dropped.
func TestExportUserArchive_SystemPart_Refused(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	err := srv.ExportUserArchive(&apiv1.ExportUserArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_PRICES},
	}, &exportUserStreamMock{ctx: authCtx("user-1", "sub|1")})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

// The mirror of the above: the system export refuses a user part.
func TestExportSystemArchive_UserPart_Refused(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	err := srv.ExportSystemArchive(&apiv1.ExportSystemArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_PREFERENCES},
	}, &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestExportUserArchive_DBError_Internal(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().GetDisplayCurrency(gomock.Any(), "user-1").Return("", errors.New("boom"))
	err := srv.ExportUserArchive(&apiv1.ExportUserArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_PREFERENCES},
	}, &exportUserStreamMock{ctx: authCtx("user-1", "sub|1")})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

// Both parts in one request travel in restore order, whatever order they were
// asked for in: preferences before transactions, because which asset classes are
// ignored changes what a transaction import keeps.
func TestExportUserArchive_PartsTravelInRestoreOrder(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().GetDisplayCurrency(gomock.Any(), "user-1").Return("GBP", nil)
	mockDB.EXPECT().ListIgnoredAssetClasses(gomock.Any(), "user-1").Return(nil, nil)
	mockDB.EXPECT().ListTxsForExport(gomock.Any(), "user-1").Return([]dbpkg.ExportPosting{
		exportPostingFixture("FIDELITY", "g1", "2024-01-15T10:00:00Z"),
	}, nil)
	stream := &exportUserStreamMock{ctx: authCtx("user-1", "sub|1")}
	if err := srv.ExportUserArchive(&apiv1.ExportUserArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_TXS, archivev1.ArchivePart_PREFERENCES},
	}, stream); err != nil {
		t.Fatalf("ExportUserArchive: %v", err)
	}
	want := []string{"envelope", "begin:PREFERENCES", "preferences", "begin:TXS", "tx_window:FIDELITY"}
	if got := stream.shape(); !equalStrings(got, want) {
		t.Fatalf("stream = %v, want %v", got, want)
	}
}

// A part that was asked for and holds nothing is still present: the part_begin
// marker is what creates the container, so an export over no transactions says
// it asked and there were none.
func TestExportUserArchive_TxPart_AskedForAndEmpty(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListTxsForExport(gomock.Any(), "user-1").Return(nil, nil)
	stream := &exportUserStreamMock{ctx: authCtx("user-1", "sub|1")}
	if err := srv.ExportUserArchive(&apiv1.ExportUserArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_TXS},
	}, stream); err != nil {
		t.Fatalf("ExportUserArchive: %v", err)
	}
	want := []string{"envelope", "begin:TXS"}
	if got := stream.shape(); !equalStrings(got, want) {
		t.Fatalf("stream = %v, want %v", got, want)
	}
}
