package ingestion

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/archiveimport"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// idTx is a posting that states identifiers, for the within-file check. The
// hints are given as domain triples because that is what the check reads: the
// proto field is decoded once, above, and shared by every pass below it.
func idTx(desc string) *apiv1.Tx {
	return &apiv1.Tx{InstrumentDescription: desc, AssetClassHint: typev1.AssetClass_STOCK}
}

func id(typ, domain, value string) identifier.Identifier {
	return identifier.Identifier{Type: typ, Domain: domain, Value: value}
}

// rows numbers the postings as the source did, which is what an archive window
// reports against.
func rows(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// Every identifier in one upload is stated as of one vintage, so two values for
// one subject under one description cannot both hold and nothing in the file says
// which is right. See adr/0064.
func TestValidateStatedIdentifiers_OneDescriptionCannotBeTwoSecurities(t *testing.T) {
	txs := []*apiv1.Tx{idTx("APPLE INC COM"), idTx("APPLE INC COM")}
	hints := [][]identifier.Identifier{
		{id("ISIN", "", "US0378331005")},
		{id("ISIN", "", "GB0000000001")},
	}
	errs := validateStatedIdentifiers(txs, hints, rows(2))
	if len(errs) != 1 {
		t.Fatalf("errors = %+v, want one", errs)
	}
	if errs[0].RowIndex != 1 || errs[0].Field != "identifier_hints" {
		t.Errorf("error = %+v, want it against row 1's hints", errs[0])
	}
	// Both values named, because the reader has to see what it disagreed with.
	for _, want := range []string{"US0378331005", "GB0000000001"} {
		if !strings.Contains(errs[0].Message, want) {
			t.Errorf("message %q does not name %s", errs[0].Message, want)
		}
	}
}

// One row stating two values for one subject is the same fault, found without
// needing a second row.
func TestValidateStatedIdentifiers_OneRowCanContradictItself(t *testing.T) {
	txs := []*apiv1.Tx{idTx("APPLE INC COM")}
	hints := [][]identifier.Identifier{{
		id("ISIN", "", "US0378331005"),
		id("ISIN", "", "GB0000000001"),
	}}
	if errs := validateStatedIdentifiers(txs, hints, rows(1)); len(errs) != 1 {
		t.Fatalf("errors = %+v, want one", errs)
	}
}

// The row the source numbered, not the position in the batch. An archive window
// is a slice of a file and has to report against the file.
func TestValidateStatedIdentifiers_ReportsTheSourcesOwnRow(t *testing.T) {
	txs := []*apiv1.Tx{idTx("APPLE INC COM"), idTx("APPLE INC COM")}
	hints := [][]identifier.Identifier{
		{id("ISIN", "", "US0378331005")},
		{id("ISIN", "", "GB0000000001")},
	}
	errs := validateStatedIdentifiers(txs, hints, []int{40, 41})
	if len(errs) != 1 {
		t.Fatalf("errors = %+v, want one", errs)
	}
	if errs[0].RowIndex != 41 || !strings.Contains(errs[0].Message, "row 40") {
		t.Errorf("error = %+v, want row 41 disagreeing with row 40", errs[0])
	}
}

// What must not be refused. Each of these is a file saying something ordinary,
// and reading any of them as a contradiction would reject uploads that are fine.
func TestValidateStatedIdentifiers_LeavesLegitimateFilesAlone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		txs   []*apiv1.Tx
		hints [][]identifier.Identifier
	}{
		{
			// A ticker under two domains names two listings, not two securities.
			// Comparing on the type alone would call a dual-listed security a fault.
			name: "one security quoted in two places",
			txs:  []*apiv1.Tx{idTx("VANGUARD S&P 500"), idTx("VANGUARD S&P 500")},
			hints: [][]identifier.Identifier{
				{id("MIC_TICKER", "XNAS", "VOO")},
				{id("MIC_TICKER", "XLON", "VUSA")},
			},
		},
		{
			// A broker writes a security several ways -- a statement, a
			// confirmation, a tax document -- and they resolve to one instrument.
			// That is the point of storing the mapping, not a contradiction.
			name: "two descriptions for one security",
			txs:  []*apiv1.Tx{idTx("APPLE INC COM"), idTx("APPLE INC")},
			hints: [][]identifier.Identifier{
				{id("ISIN", "", "US0378331005")},
				{id("ISIN", "", "US0378331005")},
			},
		},
		{
			name: "different subjects say different things",
			txs:  []*apiv1.Tx{idTx("APPLE INC COM"), idTx("APPLE INC COM")},
			hints: [][]identifier.Identifier{
				{id("ISIN", "", "US0378331005")},
				{id("CUSIP", "", "037833100")},
			},
		},
		{
			name: "the same value stated twice",
			txs:  []*apiv1.Tx{idTx("APPLE INC COM"), idTx("APPLE INC COM")},
			hints: [][]identifier.Identifier{
				{id("ISIN", "", "US0378331005")},
				{id("ISIN", "", "US0378331005")},
			},
		},
		{
			// A segment MIC and the operating MIC it normalises to are one subject
			// to the server and two here, which errs towards accepting. This
			// refuses a whole upload, so it may only fire where the disagreement
			// is plain in the file itself.
			name: "a segment MIC beside its operating MIC",
			txs:  []*apiv1.Tx{idTx("APPLE INC COM"), idTx("APPLE INC COM")},
			hints: [][]identifier.Identifier{
				{id("MIC_TICKER", "XNGS", "AAPL")},
				{id("MIC_TICKER", "XNAS", "AAPL")},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if errs := validateStatedIdentifiers(tc.txs, tc.hints, rows(len(tc.txs))); len(errs) != 0 {
				t.Errorf("refused a legitimate file: %+v", errs)
			}
		})
	}
}

// The check is wired into the pipeline, and it rejects before anything is paid
// for. The registry is nil and the DB mock expects nothing, so reaching a plugin
// or the database would fail the test rather than pass quietly -- which is the
// point: a correct check nobody calls is the state this replaces.
func TestIngestBatch_RejectsAFileThatContradictsItself(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	rep := archiveimport.NewDetachedReporter()

	txs := []*apiv1.Tx{
		hintedTx("APPLE INC COM", &apiv1.InstrumentIdentifier{
			Type: typev1.IdentifierType_ISIN, Value: "US0378331005",
		}),
		hintedTx("APPLE INC COM", &apiv1.InstrumentIdentifier{
			Type: typev1.IdentifierType_ISIN, Value: "GB0000000001",
		}),
	}
	for _, tx := range txs {
		tx.OrderDate = timestamppb.New(time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC))
		tx.TradeDate = tx.OrderDate
		tx.BrokerTxType = []typev1.TxType{typev1.TxType_TRADE_ASSET}
		tx.Quantity = "1"
	}

	_, err := ingestBatch(context.Background(), ingestDeps{DB: database}, ingestParams{
		UserID: "user-1",
		Broker: "IBKR",
		Source: "IBKR:test:statement",
		JobID:  "job-1",
		Txs:    txs,
	}, rep)
	if !errors.Is(err, errBatchRejected) {
		t.Fatalf("err = %v, want the batch rejected", err)
	}
	got := rep.Errors()
	if len(got) != 1 || got[0].Field != "identifier_hints" {
		t.Fatalf("errors = %+v, want one against the stated identifiers", got)
	}
}

// What a person is told about their own rows, and what they are not.
//
// The vocabulary is three because three is how many different things there are
// to do about a row that did not identify: supply an identifier, upload again
// later, or go and look at a disagreement. It used to be five, which named the
// stage that gave up rather than what to do -- a distinction about this system
// rather than about the file, and one telemetry keeps in full.
//
// A fourth member added here without a fourth answer to "and then what" is the
// drift this pins.
func TestIdentificationMessages_AreWhatAPersonCanActOn(t *testing.T) {
	messages := []string{
		MsgNotIdentified,
		MsgIdentificationUnavailable,
		MsgConflictingHints,
	}
	seen := map[string]bool{}
	for _, m := range messages {
		if m == "" {
			t.Error("an identification error with no message says nothing")
		}
		if seen[m] {
			t.Errorf("two outcomes share the message %q, so they cannot be told apart", m)
		}
		seen[m] = true
		// Shown to the uploader verbatim, beside their own row and description.
		// The internal vocabulary these replaced -- "broker description only",
		// "description extraction failed" -- named our stages at them.
		for _, jargon := range []string{"plugin", "extraction", "broker description only", "unconfirmed"} {
			if strings.Contains(strings.ToLower(m), jargon) {
				t.Errorf("message %q names %q, which is this system's word rather than the reader's", m, jargon)
			}
		}
	}
}
