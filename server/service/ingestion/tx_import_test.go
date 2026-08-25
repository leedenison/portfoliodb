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
	"github.com/leedenison/portfoliodb/server/identifier"
)

func archivePosting(desc, qty string, txType typev1.TxType) *archivev1.Posting {
	return &archivev1.Posting{
		OrderDate:             timestamppb.New(mustParseDay("2024-01-15")),
		TradeDate:             timestamppb.New(mustParseDay("2024-01-15")),
		Account:               "acct",
		BrokerTxType:          []typev1.TxType{txType},
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

// The file carries postings and no grouping, so what comes out is what went in, in
// order. The partition is the store's, derived from the evidence each posting
// carries. See docs/adr/0043-grouping-does-not-travel-in-the-archive.md.
func TestWindowTxs_CarriesThePostingsInOrder(t *testing.T) {
	w := &archivev1.TxWindow{
		Broker: typev1.Broker_IBKR,
		Source: "IBKR:archive:export",
		Postings: []*archivev1.Posting{
			archivePosting("AAPL", "10", typev1.TxType_TRADE_ASSET),
			archivePosting("USD", "-1000", typev1.TxType_TRADE_ASSET),
			archivePosting("MSFT", "5", typev1.TxType_TRADE_ASSET),
		},
	}
	txs, basis, rowIdx, err := windowTxs(w, 0, archiveimport.NewDetachedReporter())
	if err != nil {
		t.Fatalf("windowTxs: %v", err)
	}
	if len(txs) != 3 {
		t.Fatalf("txs = %d, want 3", len(txs))
	}
	for i, want := range []string{"AAPL", "USD", "MSFT"} {
		if txs[i].GetInstrumentDescription() != want {
			t.Errorf("tx %d is %q, want %q", i, txs[i].GetInstrumentDescription(), want)
		}
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
		Postings: []*archivev1.Posting{
			archivePosting("AAPL", "10", typev1.TxType_TRADE_ASSET),
		},
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
	restated := archivePosting("AAPL", "10", typev1.TxType_TRADE_ASSET)
	restated.ShareCountBasis = proto.String("2025-07-01")
	w := &archivev1.TxWindow{
		Broker: typev1.Broker_IBKR,
		Postings: []*archivev1.Posting{
			archivePosting("MSFT", "5", typev1.TxType_TRADE_ASSET),
			restated,
		},
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
	p := archivePosting("USD", "-3.5", typev1.TxType_TRADE_ASSET)
	p.AccountType = typev1.AccountType_ACCOUNT_TYPE_IMBALANCE
	tx := archiveTx(p)
	if tx.GetAccountType() != typev1.AccountType_ACCOUNT_TYPE_IMBALANCE {
		t.Fatalf("account_type = %s, want IMBALANCE", tx.GetAccountType())
	}
}

// A posting's evidence travels into the ingestion form untouched. It says why
// the posting might belong with another one, and a rebuild from an archive would
// have nothing left to group on without it.
func TestArchiveTx_CarriesCorrelations(t *testing.T) {
	p := archivePosting("GBP", "20000", typev1.TxType_TRANSFER)
	ordinal := int64(971613414)
	p.Correlations = []*archivev1.Correlation{{
		Token: "971613414", Ordinal: &ordinal,
		Scope: typev1.Scope_SCOPE_FILE,
		Match: []typev1.Match{typev1.Match_MATCH_EXACT, typev1.Match_MATCH_ORDINAL},
	}}
	got := archiveTx(p).GetCorrelations()
	if len(got) != 1 {
		t.Fatalf("correlations = %v, want 1", got)
	}
	if got[0].GetToken() != "971613414" || got[0].GetOrdinal() != ordinal {
		t.Errorf("correlation = %q/%d", got[0].GetToken(), got[0].GetOrdinal())
	}
	if got[0].GetScope() != typev1.Scope_SCOPE_FILE || len(got[0].GetMatch()) != 2 {
		t.Errorf("scope/match = %v/%v", got[0].GetScope(), got[0].GetMatch())
	}
}

// Optional fields that the file does not state stay unset rather than becoming
// an empty string or a zero price.
func TestArchiveTx_UnstatedFieldsStayUnset(t *testing.T) {
	tx := archiveTx(archivePosting("AAPL", "10", typev1.TxType_TRADE_ASSET))
	if tx.UnitPrice != nil {
		t.Fatalf("unit_price = %v, want unset", tx.UnitPrice)
	}
	if len(tx.GetCorrelations()) != 0 {
		t.Fatalf("correlations = %v, want none", tx.GetCorrelations())
	}
	if len(tx.GetIdentifierHints()) != 1 {
		t.Fatalf("identifier_hints = %v, want the one the export chose", tx.GetIdentifierHints())
	}
}

// A window is a replacement scope, so it is stored with the period it states
// rather than one inferred from its postings.
//
// The group here already sums to zero, as an exported one does, and comes back
// with exactly the postings the file stated: the balancer routes nothing for a
// commodity that already balances, so a re-imported group is not given a second
// residual. That is what makes the round trip idempotent.
func TestImportTxPart_StoresEachWindowWithItsOwnPeriod(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
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
			txs []*apiv1.Tx, _ []db.Resolution, _ []db.Weight, _ []*time.Time) error {
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
		Postings: []*archivev1.Posting{
			archivePosting("AAPL", "10", typev1.TxType_TRADE_ASSET),
			archivePosting("AAPL", "-10", typev1.TxType_TRADE_ASSET),
		},
	}}}
	stored, err := importTxPart(context.Background(), ingestDeps{DB: database}, "user-1", "job-tx", part, nil, archiveimport.NewDetachedReporter())
	if err != nil {
		t.Fatalf("importTxPart: %v", err)
	}
	if !stored {
		t.Fatal("stored = false, want true")
	}
}

// A window holding no postings still runs, and clears the period it names. An
// import that skipped it would leave the period as it found it, which is the
// opposite instruction.
func TestImportTxPart_EmptyWindowClearsItsPeriod(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
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
	if _, err := importTxPart(context.Background(), ingestDeps{DB: database}, "user-1", "job-tx", part, nil, archiveimport.NewDetachedReporter()); err != nil {
		t.Fatalf("importTxPart: %v", err)
	}
}

// The part's unit is a posting, which is what a reader watching the job's
// progress expects to see move.
func TestImportTxPart_TotalCountsPostings(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
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
		Postings: []*archivev1.Posting{
			archivePosting("AAPL", "10", typev1.TxType_TRADE_ASSET),
			archivePosting("USD", "-1000", typev1.TxType_TRADE_ASSET),
			archivePosting("MSFT", "5", typev1.TxType_TRADE_ASSET),
		},
	}}}
	rep := archiveimport.NewPartReporter(database, "job-tx", archivev1.ArchivePart_TXS)
	if _, err := importTxPart(context.Background(), ingestDeps{DB: database}, "user-1", "job-tx", part, nil, rep); err != nil {
		t.Fatalf("importTxPart: %v", err)
	}
}

// A period-scoped export can split a group at a window bound, so a window can
// carry a group that does not sum to zero. That is legal: the import weighs what it
// was given and hands it over, and the store routes the counterparty from the
// weights. What is checked here is the half the store cannot do for itself -- the
// weight has to name what the group is short.
func TestImportTxPart_WeighsASplitGroupsShortfall(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
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
			txs []*apiv1.Tx, _ []db.Resolution, weights []db.Weight, _ []*time.Time) error {
			// The stated leg alone. Nothing is invented above the store.
			if len(txs) != 1 {
				t.Fatalf("stored %d postings, want the stated leg alone", len(txs))
			}
			if len(weights) != 1 {
				t.Fatalf("passed %d weights, want one per posting", len(weights))
			}
			// No price, so nothing to convert at: the leg weighs its own quantity in
			// the security, and 10 of it is what the group is short.
			if got := weights[0].Amount.String(); got != "10" {
				t.Errorf("weight = %s, want 10", got)
			}
			if got := weights[0].Commodity; got != "inst:inst-1" {
				t.Errorf("weight commodity = %s, want the security itself", got)
			}
			return nil
		})

	// Half a trade: the counter-leg fell the other side of the window bound.
	part := &archivev1.TxPart{Windows: []*archivev1.TxWindow{{
		Broker:       typev1.Broker_IBKR,
		PeriodFrom:   from,
		PeriodBefore: before,
		Source:       "IBKR:archive:export",
		Postings: []*archivev1.Posting{
			archivePosting("AAPL", "10", typev1.TxType_TRADE_ASSET),
		},
	}}}
	if _, err := importTxPart(context.Background(), ingestDeps{DB: database}, "user-1", "job-tx", part, nil, archiveimport.NewDetachedReporter()); err != nil {
		t.Fatalf("importTxPart: %v", err)
	}
}

// The document's envelope says when its data was current, and its transaction
// part's identifiers are stated as of that moment. Every window is resolved
// against the one vintage: knowledge time that differs between one file's own
// windows is not knowledge time. The observable is the date the resolved name
// is written under -- the envelope's, never the posting's own trade date.
func TestImportTxPart_ResolvesAgainstTheEnvelopeVintage(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().FindInstrumentBySourceDescription(gomock.Any(), gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	database.EXPECT().FindDescriptionOnlyInstrument(gomock.Any(), gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", "", nil, nil).AnyTimes()
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	database.EXPECT().ListInstrumentsByIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	database.EXPECT().AppendIdentificationErrors(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().InstrumentsWithSplits(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	database.EXPECT().ReplaceTxsInPeriod(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	registry := identifier.NewRegistry()
	registry.Register("local", &fakePlugin{
		inst: &identifier.Instrument{
			AssetClass: "OPTION",
			Listing:    identifier.Listing{Currency: "USD"},
			// A contract is written on a line of its underlying, so a stored
			// option needs one; without it the resolution degrades to a
			// broker-description-only instrument (adr/0074).
			UnderlyingIdentifiers: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}},
		},
		ids: []identifier.Identifier{{Type: "OCC", Value: "AAPL250117C00760000"}},
	})
	// The underlying short-circuits out of the instrument table, and the
	// contract's USD strike names the line of it the option delivers.
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("underlying-id", "STOCK", []string{"USD"}, nil).AnyTimes()
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").Return("underlying-id", nil).AnyTimes()
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "AAPL").Return("underlying-id", nil).AnyTimes()
	database.EXPECT().EnsureListing(gomock.Any(), "underlying-id", "USD").Return("underlying-line-id", nil).AnyTimes()
	database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "local", Precedence: 10}}, nil).AnyTimes()

	var validFrom []*time.Time
	database.EXPECT().EnsureInstrument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _, _, _ string, idns []db.IdentifierInput, _ []db.IdentityClaim, _ string, _ *db.OptionFields) (string, string, error) {
			for _, idn := range idns {
				validFrom = append(validFrom, idn.ValidFrom)
			}
			return "inst-1", "listing-id", nil
		}).AnyTimes()

	option := archivePosting("AAPL 250117C00760000", "1", typev1.TxType_TRADE_ASSET)
	option.IdentifierHints = []*archivev1.InstrumentRef{{
		Type: typev1.IdentifierType_OCC, Value: "AAPL250117C00760000",
	}}
	part := &archivev1.TxPart{Windows: []*archivev1.TxWindow{{
		Broker:       typev1.Broker_IBKR,
		PeriodFrom:   timestamppb.New(mustParseDay("2024-01-01")),
		PeriodBefore: timestamppb.New(mustParseDay("2025-01-01")),
		Source:       "IBKR:archive:export",
		Postings:     []*archivev1.Posting{option},
	}}}

	asOf := mustParseDay("2024-09-01")
	if _, err := importTxPart(context.Background(), ingestDeps{DB: database, Registry: registry}, "user-1", "job-tx", part, &asOf, archiveimport.NewDetachedReporter()); err != nil {
		t.Fatalf("importTxPart: %v", err)
	}
	if len(validFrom) == 0 {
		t.Fatal("no identifier was written")
	}
	for _, got := range validFrom {
		if got == nil || !got.Equal(asOf) {
			t.Errorf("valid_from = %v, want the envelope vintage %v", got, asOf)
		}
	}
}
