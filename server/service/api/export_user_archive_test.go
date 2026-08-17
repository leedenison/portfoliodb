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
	dbpkg "github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
	"google.golang.org/protobuf/types/known/timestamppb"
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
		case m.GetDeclarationStatement() != nil:
			st := m.GetDeclarationStatement()
			out = append(out, "statement:"+st.GetAccount()+"@"+st.GetAsOfDate())
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
func exportPreferences(t *testing.T, currency string) *exportUserStreamMock {
	t.Helper()
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().GetDisplayCurrency(gomock.Any(), "user-1").Return(currency, nil)
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
	stream := exportPreferences(t, "GBP")
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

// The display currency is always stated, because it is always known: it is a
// NOT NULL column.
func TestExportUserArchive_Preferences_CarriesTheDisplayCurrency(t *testing.T) {
	part := exportPreferences(t, "GBP").preferences()
	if part.GetDisplayCurrency() != "GBP" {
		t.Fatalf("display_currency = %q", part.GetDisplayCurrency())
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
// asked for in: preferences before transactions, so a reader applies the file in
// the order the file is written.
func TestExportUserArchive_PartsTravelInRestoreOrder(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().GetDisplayCurrency(gomock.Any(), "user-1").Return("GBP", nil)
	mockDB.EXPECT().ListTxsForExport(gomock.Any(), "user-1", gomock.Any(), gomock.Any()).Return([]dbpkg.ExportPosting{
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
	mockDB.EXPECT().ListTxsForExport(gomock.Any(), "user-1", gomock.Any(), gomock.Any()).Return(nil, nil)
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

// The requested period reaches the read, so the rows are filtered rather than the
// whole history being read and trimmed on the way out.
func TestExportUserArchive_PassesThePeriodToTheRead(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	from := timestamppb.New(mustTime(t, "2024-02-01T00:00:00Z"))
	before := timestamppb.New(mustTime(t, "2024-03-01T00:00:00Z"))
	mockDB.EXPECT().
		ListTxsForExport(gomock.Any(), "user-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, gotFrom, gotBefore *timestamppb.Timestamp) ([]dbpkg.ExportPosting, error) {
			if !gotFrom.AsTime().Equal(from.AsTime()) || !gotBefore.AsTime().Equal(before.AsTime()) {
				t.Errorf("read period = [%s, %s), want [%s, %s)",
					gotFrom.AsTime(), gotBefore.AsTime(), from.AsTime(), before.AsTime())
			}
			return nil, nil
		})
	err := srv.ExportUserArchive(&apiv1.ExportUserArchiveRequest{
		Parts:        []archivev1.ArchivePart{archivev1.ArchivePart_TXS},
		PeriodFrom:   from,
		PeriodBefore: before,
	}, &exportUserStreamMock{ctx: authCtx("user-1", "sub|1")})
	if err != nil {
		t.Fatalf("ExportUserArchive: %v", err)
	}
}

// A period that ends where or before it starts covers nothing, and an export of
// nothing over a stated period is a file that clears it on import. That is worth
// rejecting rather than producing by accident.
func TestExportUserArchive_RejectsAnEmptyPeriod(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	at := timestamppb.New(mustTime(t, "2024-02-01T00:00:00Z"))
	err := srv.ExportUserArchive(&apiv1.ExportUserArchiveRequest{
		Parts:        []archivev1.ArchivePart{archivev1.ArchivePart_TXS},
		PeriodFrom:   at,
		PeriodBefore: at,
	}, &exportUserStreamMock{ctx: authCtx("user-1", "sub|1")})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}
