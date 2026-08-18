package archiveimport

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db/mock"
)

// startDay is the portfolio start date every test below shares. A declaration
// is padded from and checked against the transactions, so the part reads it
// once and rejects anything the history no longer reaches.
const startDay = "2020-01-01"

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return d
}

// expectStartDate stubs the one read every run makes before any row.
func expectStartDate(t *testing.T, database *mock.MockDB) {
	t.Helper()
	start := day(t, startDay)
	database.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&start, nil)
}

func decl(value, qty string, basis *string) *archivev1.Declaration {
	return &archivev1.Declaration{
		Instrument:      &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: value, Domain: "XNAS"},
		DeclaredQty:     qty,
		ShareCountBasis: basis,
	}
}

func statement(account, asOf string, decls ...*archivev1.Declaration) *archivev1.Statement {
	return &archivev1.Statement{
		Broker:       typev1.Broker_FIDELITY,
		Account:      account,
		AsOfDate:     asOf,
		Declarations: decls,
	}
}

func declarationPart(sts ...*archivev1.Statement) *archivev1.DeclarationPart {
	return &archivev1.DeclarationPart{Statements: sts}
}

// expectResolve stubs the DB-only lookup for one identifier value.
func expectResolve(database *mock.MockDB, value, instrumentID string) {
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", value).
		Return(instrumentID, nil)
}

func applyDeclarations(t *testing.T, database *mock.MockDB, rep *PartReporter, part *archivev1.DeclarationPart) int32 {
	t.Helper()
	n, err := DeclarationPart(context.Background(), database, "user-1", part, rep)
	if err != nil {
		t.Fatalf("DeclarationPart: %v", err)
	}
	return n
}

// Every declaration of every statement is written, with the statement's broker,
// account and date carried down onto each of its rows.
func TestDeclarationPart_WritesEveryRow(t *testing.T) {
	database, rep := newPartTest(t)
	expectStartDate(t, database)
	expectResolve(database, "AAPL", "inst-a")
	expectResolve(database, "MSFT", "inst-m")
	database.EXPECT().
		UpsertHoldingDeclaration(gomock.Any(), "user-1", "FIDELITY", "Z1", "inst-a", "100",
			day(t, "2024-01-31"), day(t, "2024-01-31")).
		Return(nil)
	database.EXPECT().
		UpsertHoldingDeclaration(gomock.Any(), "user-1", "FIDELITY", "Z1", "inst-m", "50",
			day(t, "2024-01-31"), day(t, "2024-01-31")).
		Return(nil)

	n := applyDeclarations(t, database, rep, declarationPart(
		statement("Z1", "2024-01-31", decl("AAPL", "100", nil), decl("MSFT", "50", nil)),
	))
	if n != 2 {
		t.Fatalf("wrote %d declarations, want 2", n)
	}
	if rep.ErrCount() != 0 {
		t.Fatalf("reported %d row errors, want none: %v", rep.ErrCount(), rep.Errors())
	}
}

// A quantity read off a record of the statement's date is in the share count
// current then, so an absent basis is that date. A basis the file states is
// used as stated.
func TestDeclarationPart_DefaultsShareCountBasisToTheStatementDate(t *testing.T) {
	database, rep := newPartTest(t)
	expectStartDate(t, database)
	expectResolve(database, "AAPL", "inst-a")
	database.EXPECT().
		UpsertHoldingDeclaration(gomock.Any(), "user-1", "FIDELITY", "Z1", "inst-a", "100",
			day(t, "2024-01-31"), day(t, "2026-08-13")).
		Return(nil)

	applyDeclarations(t, database, rep, declarationPart(
		statement("Z1", "2024-01-31", decl("AAPL", "100", proto.String("2026-08-13"))),
	))
}

// A declaration the system cannot name has nothing to pad and nothing to check,
// so the row is rejected. Its siblings still land: a rejected row does not fail
// the part.
func TestDeclarationPart_RejectsAnUnresolvableInstrument(t *testing.T) {
	database, rep := newPartTest(t)
	expectStartDate(t, database)
	expectResolve(database, "GONE", "")
	expectResolve(database, "MSFT", "inst-m")
	database.EXPECT().
		UpsertHoldingDeclaration(gomock.Any(), "user-1", "FIDELITY", "Z1", "inst-m", "50",
			day(t, "2024-01-31"), day(t, "2024-01-31")).
		Return(nil)

	n := applyDeclarations(t, database, rep, declarationPart(
		statement("Z1", "2024-01-31", decl("GONE", "100", nil), decl("MSFT", "50", nil)),
	))
	if n != 1 {
		t.Fatalf("wrote %d declarations, want the resolvable one", n)
	}
	if rep.ErrCount() != 1 {
		t.Fatalf("reported %d row errors, want 1: %v", rep.ErrCount(), rep.Errors())
	}
	if got := rep.Errors()[0].GetField(); got != "instrument" {
		t.Fatalf("row error field = %q, want instrument", got)
	}
}

// A declaration the start date has moved past would be written and then deleted
// by the recalculation, so it is rejected here where the user is told about it.
func TestDeclarationPart_RejectsADateBeforeThePortfolioStart(t *testing.T) {
	database, rep := newPartTest(t)
	expectStartDate(t, database)

	n := applyDeclarations(t, database, rep, declarationPart(
		statement("Z1", "2019-12-31", decl("AAPL", "100", nil)),
	))
	if n != 0 {
		t.Fatalf("wrote %d declarations, want none", n)
	}
	if rep.ErrCount() != 1 || rep.Errors()[0].GetField() != "as_of_date" {
		t.Fatalf("reported %v, want one as_of_date error", rep.Errors())
	}
}

// Row indices run across the whole part rather than restarting per statement,
// so a problem points at a declaration in the document.
func TestDeclarationPart_RowIndicesRunAcrossTheWholePart(t *testing.T) {
	database, rep := newPartTest(t)
	expectStartDate(t, database)
	expectResolve(database, "AAPL", "inst-a")
	expectResolve(database, "GONE", "")
	database.EXPECT().
		UpsertHoldingDeclaration(gomock.Any(), "user-1", "FIDELITY", "Z1", "inst-a", "100",
			gomock.Any(), gomock.Any()).
		Return(nil)

	applyDeclarations(t, database, rep, declarationPart(
		statement("Z1", "2024-01-31", decl("AAPL", "100", nil)),
		statement("Z2", "2024-01-31", decl("GONE", "5", nil)),
	))
	if rep.ErrCount() != 1 {
		t.Fatalf("reported %d row errors, want 1", rep.ErrCount())
	}
	if got := rep.Errors()[0].GetRowIndex(); got != 1 {
		t.Fatalf("row index = %d, want 1", got)
	}
}

// A quantity that will not parse is a row error rather than a part failure.
func TestDeclarationPart_RejectsAQuantityThatWillNotParse(t *testing.T) {
	database, rep := newPartTest(t)
	expectStartDate(t, database)

	applyDeclarations(t, database, rep, declarationPart(
		statement("Z1", "2024-01-31", decl("AAPL", "not a number", nil)),
	))
	if rep.ErrCount() != 1 || rep.Errors()[0].GetField() != "declared_qty" {
		t.Fatalf("reported %v, want one declared_qty error", rep.Errors())
	}
}

// Absence is not deletion, which is where this part differs from the
// transaction one. A file assembled from one statement covers one account and
// one date, and treating everything outside it as retracted would delete the
// user's other checkpoints -- so nothing is listed and nothing is deleted. The
// mock is strict, so any such call would fail this test.
func TestDeclarationPart_LeavesWhatTheFileDoesNotNameAlone(t *testing.T) {
	database, rep := newPartTest(t)
	expectStartDate(t, database)
	expectResolve(database, "AAPL", "inst-a")
	database.EXPECT().
		UpsertHoldingDeclaration(gomock.Any(), "user-1", "FIDELITY", "Z1", "inst-a", "100",
			gomock.Any(), gomock.Any()).
		Return(nil)

	applyDeclarations(t, database, rep, declarationPart(
		statement("Z1", "2024-01-31", decl("AAPL", "100", nil)),
	))
}

// A part holding nothing succeeds having done nothing, which is what a present
// but empty section means. It does not even read the start date.
func TestDeclarationPart_EmptyPartDoesNothing(t *testing.T) {
	database, rep := newPartTest(t)

	n := applyDeclarations(t, database, rep, declarationPart())
	if n != 0 {
		t.Fatalf("wrote %d declarations, want none", n)
	}
}

// A portfolio with no transactions has no start date to anchor a pad to. The
// file is fine and the instance is not ready for it, so the part fails rather
// than every row in it.
func TestDeclarationPart_FailsWhenThereAreNoTransactions(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(nil, nil)

	_, err := DeclarationPart(context.Background(), database, "user-1",
		declarationPart(statement("Z1", "2024-01-31", decl("AAPL", "100", nil))), rep)
	if err == nil {
		t.Fatal("DeclarationPart succeeded, want a part failure")
	}
}

// A write that does not land fails the part: what failed is not a row.
func TestDeclarationPart_AWriteFailureFailsThePart(t *testing.T) {
	database, rep := newPartTest(t)
	expectStartDate(t, database)
	expectResolve(database, "AAPL", "inst-a")
	database.EXPECT().
		UpsertHoldingDeclaration(gomock.Any(), "user-1", "FIDELITY", "Z1", "inst-a", "100",
			gomock.Any(), gomock.Any()).
		Return(errors.New("disk on fire"))

	_, err := DeclarationPart(context.Background(), database, "user-1",
		declarationPart(statement("Z1", "2024-01-31", decl("AAPL", "100", nil))), rep)
	if err == nil {
		t.Fatal("DeclarationPart succeeded, want the write failure to fail the part")
	}
}
