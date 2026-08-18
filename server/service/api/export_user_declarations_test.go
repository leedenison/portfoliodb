package api

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	dbpkg "github.com/leedenison/portfoliodb/server/db"
)

func declDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return d
}

// exportDecl builds one stored export row. basis is written only where it
// differs from asOf, which is what the query itself does.
func exportDecl(t *testing.T, broker, account, value, qty, asOf, basis string) dbpkg.ExportDeclaration {
	t.Helper()
	row := dbpkg.ExportDeclaration{
		Broker:           broker,
		Account:          account,
		IdentifierType:   "MIC_TICKER",
		IdentifierValue:  value,
		IdentifierDomain: "XNAS",
		DeclaredQty:      decimal.RequireFromString(qty),
		AsOfDate:         declDate(t, asOf),
	}
	if basis != "" {
		b := declDate(t, basis)
		row.ShareCountBasis = &b
	}
	return row
}

// exportDeclarations runs a declarations-only export over the given stored rows.
func exportDeclarations(t *testing.T, rows []dbpkg.ExportDeclaration) *exportUserStreamMock {
	t.Helper()
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListHoldingDeclarationsForExport(gomock.Any(), "user-1").Return(rows, nil)
	stream := &exportUserStreamMock{ctx: authCtx("user-1", "sub|1")}
	if err := srv.ExportUserArchive(&apiv1.ExportUserArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_DECLARATIONS},
	}, stream); err != nil {
		t.Fatalf("ExportUserArchive: %v", err)
	}
	return stream
}

// statements collects the statements the stream carried.
func (e *exportUserStreamMock) statements() []*archivev1.Statement {
	var out []*archivev1.Statement
	for _, m := range e.sent {
		if v := m.GetDeclarationStatement(); v != nil {
			out = append(out, v)
		}
	}
	return out
}

// The group is the statement -- one account read at one date -- so a run of
// rows sharing those becomes one message and a change in either cuts a new one.
func TestExportUserArchive_CutsStatementsOnAccountAndDate(t *testing.T) {
	stream := exportDeclarations(t, []dbpkg.ExportDeclaration{
		exportDecl(t, "FIDELITY", "Z1", "AAPL", "100", "2024-01-31", ""),
		exportDecl(t, "FIDELITY", "Z1", "MSFT", "50", "2024-01-31", ""),
		exportDecl(t, "FIDELITY", "Z1", "AAPL", "120", "2024-02-29", ""),
		exportDecl(t, "FIDELITY", "Z2", "AAPL", "7", "2024-02-29", ""),
	})

	want := []string{
		"envelope", "begin:DECLARATIONS",
		"statement:Z1@2024-01-31", "statement:Z1@2024-02-29", "statement:Z2@2024-02-29",
	}
	got := stream.shape()
	if len(got) != len(want) {
		t.Fatalf("stream shape = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stream shape = %v, want %v", got, want)
		}
	}
	first := stream.statements()[0]
	if n := len(first.GetDeclarations()); n != 2 {
		t.Fatalf("first statement carries %d declarations, want 2", n)
	}
	if first.GetBroker() != typev1.Broker_FIDELITY {
		t.Fatalf("first statement broker = %v, want FIDELITY", first.GetBroker())
	}
}

// A basis equal to the statement's own date is what an absent one already
// means, so writing it would stamp a redundant date onto every row.
func TestExportUserArchive_WritesShareCountBasisOnlyWhenItDiffers(t *testing.T) {
	stream := exportDeclarations(t, []dbpkg.ExportDeclaration{
		exportDecl(t, "FIDELITY", "Z1", "AAPL", "100", "2024-01-31", ""),
		exportDecl(t, "FIDELITY", "Z1", "MSFT", "50", "2024-01-31", "2026-08-13"),
	})

	decls := stream.statements()[0].GetDeclarations()
	if decls[0].ShareCountBasis != nil {
		t.Fatalf("share_count_basis = %q, want absent", decls[0].GetShareCountBasis())
	}
	if got := decls[1].GetShareCountBasis(); got != "2026-08-13" {
		t.Fatalf("share_count_basis = %q, want 2026-08-13", got)
	}
}

// A row this build cannot name in a file would fail validation on the way back
// in and take its whole statement with it, so it is dropped instead. A
// statement left with nothing is not sent at all.
func TestExportUserArchive_SkipsDeclarationsItCannotName(t *testing.T) {
	noIdentifier := exportDecl(t, "FIDELITY", "Z1", "AAPL", "100", "2024-01-31", "")
	noIdentifier.IdentifierType, noIdentifier.IdentifierValue, noIdentifier.IdentifierDomain = "", "", ""
	unknownBroker := exportDecl(t, "NOT_A_BROKER", "Z9", "MSFT", "5", "2024-01-31", "")

	stream := exportDeclarations(t, []dbpkg.ExportDeclaration{
		noIdentifier,
		unknownBroker,
		exportDecl(t, "FIDELITY", "Z1", "MSFT", "50", "2024-01-31", ""),
	})

	sts := stream.statements()
	if len(sts) != 1 {
		t.Fatalf("sent %d statements, want 1", len(sts))
	}
	decls := sts[0].GetDeclarations()
	if len(decls) != 1 || decls[0].GetInstrument().GetValue() != "MSFT" {
		t.Fatalf("statement carries %v, want the one nameable declaration", decls)
	}
}

// A part asked for and holding nothing is still present and empty: the
// part_begin marker is what creates the container.
func TestExportUserArchive_DeclarationsPartPresentWhenEmpty(t *testing.T) {
	stream := exportDeclarations(t, nil)
	got := stream.shape()
	if len(got) != 2 || got[1] != "begin:DECLARATIONS" {
		t.Fatalf("stream shape = %v, want the marker and no statements", got)
	}
}

// The period scopes the transaction part alone. A declaration is dated by the
// date it speaks about rather than by a period of activity, and dropping the
// checkpoints outside an exported window would lose the pads the oldest
// holdings open from.
func TestExportUserArchive_PeriodDoesNotScopeDeclarations(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	rows := []dbpkg.ExportDeclaration{
		exportDecl(t, "FIDELITY", "Z1", "AAPL", "100", "2019-01-31", ""),
	}
	// Called with the user alone: no bounds are passed through.
	mockDB.EXPECT().ListHoldingDeclarationsForExport(gomock.Any(), "user-1").Return(rows, nil)
	stream := &exportUserStreamMock{ctx: authCtx("user-1", "sub|1")}
	if err := srv.ExportUserArchive(&apiv1.ExportUserArchiveRequest{
		Parts:        []archivev1.ArchivePart{archivev1.ArchivePart_DECLARATIONS},
		PeriodFrom:   timestamppb.New(declDate(t, "2024-01-01")),
		PeriodBefore: timestamppb.New(declDate(t, "2025-01-01")),
	}, stream); err != nil {
		t.Fatalf("ExportUserArchive: %v", err)
	}
	if n := len(stream.statements()); n != 1 {
		t.Fatalf("sent %d statements, want the out-of-period one to survive", n)
	}
}
