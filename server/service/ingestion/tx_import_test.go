package ingestion

import (
	"context"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/archiveimport"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
)

func archivePosting(desc, qty string, txType typev1.TxType) *archivev1.Posting {
	return &archivev1.Posting{
		Timestamp:             timestamppb.New(mustParseDay("2024-01-15")),
		Account:               "acct",
		Type:                  txType,
		InstrumentDescription: desc,
		Quantity:              qty,
		IdentifierHints: []*archivev1.InstrumentRef{{
			Type: typev1.IdentifierType_BROKER_DESCRIPTION, Value: desc, Domain: "IBKR",
		}},
	}
}

func mustParseDay(s string) time.Time {
	v, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return v
}

// The archive nests its groups; the store rebuilds them from a shared key. The
// key is invented per group and thrown away with the call, which is what
// group_ref always was.
func TestWindowTxs_SynthesisesAGroupRefPerGroup(t *testing.T) {
	w := &archivev1.TxWindow{
		Broker: typev1.Broker_IBKR,
		Source: "IBKR:archive:export",
		Groups: []*archivev1.TxGroup{
			{Postings: []*archivev1.Posting{
				archivePosting("AAPL", "10", typev1.TxType_BUYSTOCK),
				archivePosting("USD", "-1000", typev1.TxType_BUYSTOCK),
			}},
			{Postings: []*archivev1.Posting{
				archivePosting("MSFT", "5", typev1.TxType_BUYSTOCK),
			}},
		},
	}
	txs, basis, rowIdx, err := windowTxs(w, 0, archiveimport.NewDetachedReporter())
	if err != nil {
		t.Fatalf("windowTxs: %v", err)
	}
	if len(txs) != 3 {
		t.Fatalf("txs = %d, want 3", len(txs))
	}
	if txs[0].GetGroupRef() != txs[1].GetGroupRef() {
		t.Fatalf("legs of one group have refs %q and %q", txs[0].GetGroupRef(), txs[1].GetGroupRef())
	}
	if txs[2].GetGroupRef() == txs[0].GetGroupRef() {
		t.Fatalf("two groups share ref %q", txs[2].GetGroupRef())
	}
	// A window that restates nothing carries no basis slice, which is what
	// leaves every posting to the insert trigger and its own date.
	if basis != nil {
		t.Fatalf("basis = %v, want nil for an as-traded window", basis)
	}
	want := []int{0, 1, 2}
	for i := range want {
		if rowIdx[i] != want[i] {
			t.Fatalf("row indices = %v, want %v", rowIdx, want)
		}
	}
}

// Row indices run across the whole part rather than restarting per window, so a
// problem points at a posting in the document.
func TestWindowTxs_RowIndicesContinueAcrossWindows(t *testing.T) {
	w := &archivev1.TxWindow{
		Broker: typev1.Broker_IBKR,
		Groups: []*archivev1.TxGroup{{Postings: []*archivev1.Posting{
			archivePosting("AAPL", "10", typev1.TxType_BUYSTOCK),
		}}},
	}
	_, _, rowIdx, err := windowTxs(w, 7, archiveimport.NewDetachedReporter())
	if err != nil {
		t.Fatalf("windowTxs: %v", err)
	}
	if len(rowIdx) != 1 || rowIdx[0] != 7 {
		t.Fatalf("row indices = %v, want [7]", rowIdx)
	}
}

// Absent means the posting's own timestamp date, so only a restated posting
// gets an entry and a window with one gets a slice.
func TestWindowTxs_CarriesARestatedBasis(t *testing.T) {
	restated := archivePosting("AAPL", "10", typev1.TxType_BUYSTOCK)
	restated.ShareCountBasis = proto.String("2025-07-01")
	w := &archivev1.TxWindow{
		Broker: typev1.Broker_IBKR,
		Groups: []*archivev1.TxGroup{{Postings: []*archivev1.Posting{
			archivePosting("MSFT", "5", typev1.TxType_BUYSTOCK),
			restated,
		}}},
	}
	_, basis, _, err := windowTxs(w, 0, archiveimport.NewDetachedReporter())
	if err != nil {
		t.Fatalf("windowTxs: %v", err)
	}
	if len(basis) != 2 {
		t.Fatalf("basis = %v, want one entry per posting", basis)
	}
	if basis[0] != nil {
		t.Fatalf("as-traded posting carries a basis: %v", basis[0])
	}
	if basis[1] == nil || !basis[1].Equal(mustParseDay("2025-07-01")) {
		t.Fatalf("restated basis = %v, want 2025-07-01", basis[1])
	}
}

// A routed residual travels as an ordinary posting carrying the account type it
// was stored under, so the group it balances still sums to zero on the way in
// and nothing is routed a second time.
func TestArchiveTx_CarriesTheRoutedAccountType(t *testing.T) {
	p := archivePosting("USD", "-3.5", typev1.TxType_BUYSTOCK)
	p.AccountType = typev1.AccountType_ACCOUNT_TYPE_IMBALANCE
	tx := archiveTx(p, "g0")
	if tx.GetAccountType() != typev1.AccountType_ACCOUNT_TYPE_IMBALANCE {
		t.Fatalf("account_type = %s, want IMBALANCE", tx.GetAccountType())
	}
}

// Optional fields that the file does not state stay unset rather than becoming
// an empty string or a zero price.
func TestArchiveTx_UnstatedFieldsStayUnset(t *testing.T) {
	tx := archiveTx(archivePosting("AAPL", "10", typev1.TxType_BUYSTOCK), "g0")
	if tx.UnitPrice != nil {
		t.Fatalf("unit_price = %v, want unset", tx.UnitPrice)
	}
	if tx.GetBrokerRef() != "" || tx.GetCounterpartyAccount() != "" {
		t.Fatalf("refs = %q, %q, want empty", tx.GetBrokerRef(), tx.GetCounterpartyAccount())
	}
	if len(tx.GetIdentifierHints()) != 1 {
		t.Fatalf("identifier_hints = %v, want the one the export chose", tx.GetIdentifierHints())
	}
}

// A window is a replacement scope, so it is stored with the period it states
// rather than one inferred from its groups.
//
// The group here already sums to zero, as an exported one does, and comes back
// with exactly the postings the file stated: the balancer routes nothing for a
// commodity that already balances, so a re-imported group is not given a second
// residual. That is what makes the round trip idempotent.
func TestImportTxPart_StoresEachWindowWithItsOwnPeriod(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().ListIgnoredAssetClasses(gomock.Any(), "user-1").Return(nil, nil).AnyTimes()
	database.EXPECT().FindInstrumentBySourceDescription(gomock.Any(), gomock.Any(), gomock.Any()).Return("inst-1", nil).AnyTimes()
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("inst-1", nil).AnyTimes()
	database.EXPECT().ListInstrumentsByIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	database.EXPECT().AppendIdentificationErrors(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().InstrumentsWithSplits(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	from := timestamppb.New(mustParseDay("2024-01-01"))
	before := timestamppb.New(mustParseDay("2024-02-01"))
	database.EXPECT().
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", "job-tx", from, before,
			gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _ string, _, _ *timestamppb.Timestamp,
			txs []*apiv1.Tx, _ []string, _ []db.Weight, _ []*time.Time) error {
			if len(txs) != 2 {
				t.Errorf("stored %d postings, want the 2 the file stated", len(txs))
			}
			for _, tx := range txs {
				if tx.GetAccountType() == typev1.AccountType_ACCOUNT_TYPE_IMBALANCE {
					t.Errorf("a balanced group was routed a residual: %v", tx)
				}
			}
			return nil
		})

	part := &archivev1.TxPart{Windows: []*archivev1.TxWindow{{
		Broker:       typev1.Broker_IBKR,
		PeriodFrom:   from,
		PeriodBefore: before,
		Source:       "IBKR:archive:export",
		Groups: []*archivev1.TxGroup{{Postings: []*archivev1.Posting{
			archivePosting("AAPL", "10", typev1.TxType_BUYSTOCK),
			archivePosting("AAPL", "-10", typev1.TxType_SELLSTOCK),
		}}},
	}}}
	stored, err := importTxPart(context.Background(), ingestDeps{DB: database}, "user-1", "job-tx", part, archiveimport.NewDetachedReporter())
	if err != nil {
		t.Fatalf("importTxPart: %v", err)
	}
	if !stored {
		t.Fatal("stored = false, want true")
	}
}

// A window holding no groups still runs, and clears the period it names. An
// import that skipped it would leave the period as it found it, which is the
// opposite instruction.
func TestImportTxPart_EmptyWindowClearsItsPeriod(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().ListIgnoredAssetClasses(gomock.Any(), "user-1").Return(nil, nil).AnyTimes()
	database.EXPECT().InstrumentsWithSplits(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	from := timestamppb.New(mustParseDay("2024-01-01"))
	before := timestamppb.New(mustParseDay("2024-02-01"))
	database.EXPECT().
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", "job-tx", from, before,
			gomock.Len(0), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	part := &archivev1.TxPart{Windows: []*archivev1.TxWindow{{
		Broker: typev1.Broker_IBKR, PeriodFrom: from, PeriodBefore: before,
		Source: "IBKR:archive:export",
	}}}
	if _, err := importTxPart(context.Background(), ingestDeps{DB: database}, "user-1", "job-tx", part, archiveimport.NewDetachedReporter()); err != nil {
		t.Fatalf("importTxPart: %v", err)
	}
}

// The part's unit is a posting, which is what a reader watching the job's
// progress expects to see move.
func TestImportTxPart_TotalCountsPostings(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().ListIgnoredAssetClasses(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	database.EXPECT().FindInstrumentBySourceDescription(gomock.Any(), gomock.Any(), gomock.Any()).Return("inst-1", nil).AnyTimes()
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("inst-1", nil).AnyTimes()
	database.EXPECT().ListInstrumentsByIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	database.EXPECT().AppendIdentificationErrors(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().InstrumentsWithSplits(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	database.EXPECT().ReplaceTxsInPeriod(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().AddJobPartProcessedCount(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().
		SetJobPartTotalCount(gomock.Any(), "job-tx", archivev1.ArchivePart_TXS, int32(3)).
		Return(nil)

	part := &archivev1.TxPart{Windows: []*archivev1.TxWindow{{
		Broker:       typev1.Broker_IBKR,
		PeriodFrom:   timestamppb.New(mustParseDay("2024-01-01")),
		PeriodBefore: timestamppb.New(mustParseDay("2024-02-01")),
		Source:       "IBKR:archive:export",
		Groups: []*archivev1.TxGroup{
			{Postings: []*archivev1.Posting{
				archivePosting("AAPL", "10", typev1.TxType_BUYSTOCK),
				archivePosting("USD", "-1000", typev1.TxType_BUYSTOCK),
			}},
			{Postings: []*archivev1.Posting{archivePosting("MSFT", "5", typev1.TxType_BUYSTOCK)}},
		},
	}}}
	rep := archiveimport.NewPartReporter(database, "job-tx", archivev1.ArchivePart_TXS)
	if _, err := importTxPart(context.Background(), ingestDeps{DB: database}, "user-1", "job-tx", part, rep); err != nil {
		t.Fatalf("importTxPart: %v", err)
	}
}
