package ingestion

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// userArchivePayload is a document stating the display currency, which is
// enough to watch the part run.
func userArchivePayload(t *testing.T) []byte {
	t.Helper()
	b, err := proto.Marshal(&archivev1.UserArchive{
		Envelope: &archivev1.Envelope{
			FormatVersion: 1,
			ExportedAt:    timestamppb.Now(),
			Kind:          archivev1.ArchiveKind_USER,
		},
		Preferences: &archivev1.PreferencePart{
			DisplayCurrency: proto.String("GBP"),
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// userImportMock is a database that accepts every bookkeeping write, so a test
// can expect only the calls it is about.
func userImportMock(t *testing.T) *mock.MockDB {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	database := mock.NewMockDB(ctrl)
	database.EXPECT().SetJobPartTotalCount(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().AddJobPartProcessedCount(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().AppendValidationErrors(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobTotalCount(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobProcessedCount(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().ClearJobPayload(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	return database
}

// The part applies against the job's own user: a user archive does not name
// one, so the account it restores into is whoever asked.
func TestProcessUserImport_AppliesPreferencesForTheJobsUser(t *testing.T) {
	database := userImportMock(t)
	j := &JobRequest{JobID: "job-ua-1", JobType: db.JobTypeUserArchive}
	database.EXPECT().LoadJobPayload(gomock.Any(), j.JobID).Return(userArchivePayload(t), nil)
	database.EXPECT().GetJob(gomock.Any(), j.JobID).Return(&db.JobDetail{
		UserID: "user-7",
		Parts:  partRows(archivev1.ArchivePart_PREFERENCES),
	}, nil).AnyTimes()
	database.EXPECT().SetDisplayCurrency(gomock.Any(), "user-7", "GBP").Return(nil)

	var transitions []string
	database.EXPECT().SetJobPartStatus(gomock.Any(), j.JobID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, part archivev1.ArchivePart, st apiv1.JobStatus) error {
			transitions = append(transitions, part.String()+":"+st.String())
			return nil
		}).AnyTimes()

	res := processUserImport(context.Background(), ingestDeps{DB: database}, j)

	if !res.displayCurrencySet {
		t.Fatal("display currency not reported as set, so the price fetcher is never nudged")
	}
	want := []string{"PREFERENCES:RUNNING", "PREFERENCES:SUCCESS"}
	if len(transitions) != 2 || transitions[0] != want[0] || transitions[1] != want[1] {
		t.Fatalf("transitions = %v, want %v", transitions, want)
	}
}

// A file that states no display currency earns no nudge: there is no new FX gap
// to fill.
func TestProcessUserImport_NoCurrencyEarnsNoNudge(t *testing.T) {
	database := userImportMock(t)
	j := &JobRequest{JobID: "job-ua-2", JobType: db.JobTypeUserArchive}
	payload, err := proto.Marshal(&archivev1.UserArchive{
		Envelope:    &archivev1.Envelope{FormatVersion: 1, ExportedAt: timestamppb.Now(), Kind: archivev1.ArchiveKind_USER},
		Preferences: &archivev1.PreferencePart{},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	database.EXPECT().LoadJobPayload(gomock.Any(), j.JobID).Return(payload, nil)
	database.EXPECT().GetJob(gomock.Any(), j.JobID).Return(&db.JobDetail{
		UserID: "user-7", Parts: partRows(archivev1.ArchivePart_PREFERENCES),
	}, nil).AnyTimes()
	database.EXPECT().SetJobPartStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	if res := processUserImport(context.Background(), ingestDeps{DB: database}, j); res.displayCurrencySet {
		t.Fatal("nudge earned by a file that set no display currency")
	}
}

// A payload that cannot be read belongs to the job rather than to a part, so
// there is no part to attribute it to and the whole job fails.
func TestProcessUserImport_UnreadablePayloadFailsTheJob(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	j := &JobRequest{JobID: "job-ua-3", JobType: db.JobTypeUserArchive}
	database.EXPECT().LoadJobPayload(gomock.Any(), j.JobID).Return(nil, errors.New("gone"))
	database.EXPECT().AppendValidationErrors(gomock.Any(), j.JobID, archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED, gomock.Any()).Return(nil)
	database.EXPECT().SetJobStatus(gomock.Any(), j.JobID, apiv1.JobStatus_FAILED).Return(nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), j.JobID).Return(nil)

	processUserImport(context.Background(), ingestDeps{DB: database}, j)
}

// A job resumed after a restart skips what already finished rather than
// applying it twice.
func TestProcessUserImport_SkipsAPartAlreadyDone(t *testing.T) {
	database := userImportMock(t)
	j := &JobRequest{JobID: "job-ua-4", JobType: db.JobTypeUserArchive}
	database.EXPECT().LoadJobPayload(gomock.Any(), j.JobID).Return(userArchivePayload(t), nil)
	database.EXPECT().GetJob(gomock.Any(), j.JobID).Return(&db.JobDetail{
		UserID: "user-7",
		Parts:  []db.JobPartResult{{Part: archivev1.ArchivePart_PREFERENCES, Status: apiv1.JobStatus_SUCCESS}},
	}, nil).AnyTimes()
	// No setter expectations: applying the part again is the failure.
	database.EXPECT().SetJobPartStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	processUserImport(context.Background(), ingestDeps{DB: database}, j)
}

// A part re-run starts its progress over rather than counting on from where the
// interrupted run left off.
func TestProcessUserImport_ResetsPartialProgressOnRerun(t *testing.T) {
	database := userImportMock(t)
	j := &JobRequest{JobID: "job-ua-5", JobType: db.JobTypeUserArchive}
	database.EXPECT().LoadJobPayload(gomock.Any(), j.JobID).Return(userArchivePayload(t), nil)
	database.EXPECT().GetJob(gomock.Any(), j.JobID).Return(&db.JobDetail{
		UserID: "user-7",
		Parts: []db.JobPartResult{{
			Part: archivev1.ArchivePart_PREFERENCES, Status: apiv1.JobStatus_RUNNING, ProcessedCount: 1,
		}},
	}, nil).AnyTimes()
	database.EXPECT().ResetJobPartProgress(gomock.Any(), j.JobID, archivev1.ArchivePart_PREFERENCES).Return(nil)
	database.EXPECT().SetDisplayCurrency(gomock.Any(), "user-7", "GBP").Return(nil)
	database.EXPECT().SetJobPartStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	processUserImport(context.Background(), ingestDeps{DB: database}, j)
}

// A part row this build has no arm for -- a part from a later build, or one
// belonging to the other archive -- fails that part rather than the whole
// import.
func TestProcessUserImport_UnknownPartFailsOnlyItself(t *testing.T) {
	database := userImportMock(t)
	j := &JobRequest{JobID: "job-ua-6", JobType: db.JobTypeUserArchive}
	database.EXPECT().LoadJobPayload(gomock.Any(), j.JobID).Return(userArchivePayload(t), nil)
	database.EXPECT().GetJob(gomock.Any(), j.JobID).Return(&db.JobDetail{
		UserID: "user-7", Parts: partRows(archivev1.ArchivePart_INSTRUMENTS),
	}, nil).AnyTimes()
	database.EXPECT().SetJobPartStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().
		SetJobPartFailed(gomock.Any(), j.JobID, archivev1.ArchivePart_INSTRUMENTS, errUnknownPart.Error()).
		Return(nil)

	processUserImport(context.Background(), ingestDeps{DB: database}, j)
}

// The job's own counters are the sum over its parts, so a caller that only
// knows about jobs still sees sensible progress.
func TestProcessUserImport_JobCountersAreTheSumOverParts(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().SetJobPartTotalCount(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().AddJobPartProcessedCount(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobPartStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobStatus(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().ClearJobPayload(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	j := &JobRequest{JobID: "job-ua-7", JobType: db.JobTypeUserArchive}
	database.EXPECT().LoadJobPayload(gomock.Any(), j.JobID).Return(userArchivePayload(t), nil)
	// Read twice: once to decide what to run, and once afterwards to total up
	// what the parts recorded.
	gomock.InOrder(
		database.EXPECT().GetJob(gomock.Any(), j.JobID).Return(&db.JobDetail{
			UserID: "user-7", Parts: partRows(archivev1.ArchivePart_PREFERENCES),
		}, nil),
		database.EXPECT().GetJob(gomock.Any(), j.JobID).Return(&db.JobDetail{
			UserID: "user-7",
			Parts: []db.JobPartResult{{
				Part: archivev1.ArchivePart_PREFERENCES, Status: apiv1.JobStatus_SUCCESS,
				TotalCount: 2, ProcessedCount: 2,
			}},
		}, nil),
	)
	database.EXPECT().SetDisplayCurrency(gomock.Any(), "user-7", "GBP").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), j.JobID, int32(2)).Return(nil)
	database.EXPECT().SetJobProcessedCount(gomock.Any(), j.JobID, int32(2)).Return(nil)

	processUserImport(context.Background(), ingestDeps{DB: database}, j)
}

// The dispatch case, end to end: a user archive job that restored a display
// currency earns the same price-fetcher nudge that setting one through the API
// does, and one that did not earns nothing.
func TestProcessJob_UserArchive_NudgesThePriceFetcherOnlyForACurrency(t *testing.T) {
	tests := []struct {
		name       string
		part       *archivev1.PreferencePart
		wantNudged bool
	}{
		{
			name:       "display currency restored",
			part:       &archivev1.PreferencePart{DisplayCurrency: proto.String("GBP")},
			wantNudged: true,
		},
		{
			name:       "nothing stated",
			part:       &archivev1.PreferencePart{},
			wantNudged: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			database := userImportMock(t)
			j := &JobRequest{JobID: "job-ua-d", JobType: db.JobTypeUserArchive}
			payload, err := proto.Marshal(&archivev1.UserArchive{
				Envelope:    &archivev1.Envelope{FormatVersion: 1, ExportedAt: timestamppb.Now(), Kind: archivev1.ArchiveKind_USER},
				Preferences: tc.part,
			})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			database.EXPECT().LoadJobPayload(gomock.Any(), j.JobID).Return(payload, nil)
			database.EXPECT().GetJob(gomock.Any(), j.JobID).Return(&db.JobDetail{
				UserID: "user-7", Parts: partRows(archivev1.ArchivePart_PREFERENCES),
			}, nil).AnyTimes()
			database.EXPECT().SetDisplayCurrency(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			database.EXPECT().SetJobPartStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

			trigger := make(chan struct{}, 1)
			processJob(context.Background(), WorkerOptions{DB: database, PriceTrigger: trigger}, j)

			select {
			case <-trigger:
				if !tc.wantNudged {
					t.Fatal("price fetcher nudged with no display currency restored")
				}
			default:
				if tc.wantNudged {
					t.Fatal("price fetcher not nudged after a display currency was restored")
				}
			}
		})
	}
}

// declarationArchivePayload is a document stating one declaration, which is
// enough to watch the part run.
func declarationArchivePayload(t *testing.T) []byte {
	t.Helper()
	b, err := proto.Marshal(&archivev1.UserArchive{
		Envelope: &archivev1.Envelope{
			FormatVersion: 1,
			ExportedAt:    timestamppb.Now(),
			Kind:          archivev1.ArchiveKind_USER,
		},
		Declarations: &archivev1.DeclarationPart{
			Statements: []*archivev1.Statement{{
				Broker:   typev1.Broker_FIDELITY,
				Account:  "Z1",
				AsOfDate: "2024-01-31",
				Declarations: []*archivev1.Declaration{{
					// The currency names which of the security's lines the
					// declaration is about, and is what the import resolves it to.
					Instrument: &archivev1.InstrumentRef{
						Type:     typev1.IdentifierType_MIC_TICKER,
						Value:    "AAPL",
						Domain:   "XNAS",
						Currency: "USD",
					},
					DeclaredQty: "100",
				}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// The declarations part dispatches, and a declaration that landed is what earns
// the recalculation: a declaration carries no pad, and the pad is what makes the
// declared quantity true.
func TestProcessUserImport_DeclarationsPartStoresAndEarnsTheRecalc(t *testing.T) {
	database := userImportMock(t)
	j := &JobRequest{JobID: "job-ua-8", JobType: db.JobTypeUserArchive}
	database.EXPECT().LoadJobPayload(gomock.Any(), j.JobID).Return(declarationArchivePayload(t), nil)
	database.EXPECT().GetJob(gomock.Any(), j.JobID).Return(&db.JobDetail{
		UserID: "user-7", Parts: partRows(archivev1.ArchivePart_DECLARATIONS),
	}, nil).AnyTimes()
	database.EXPECT().SetJobPartStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	database.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-7").Return(&start, nil)
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-a", nil)
	database.EXPECT().FindListing(gomock.Any(), "inst-a", "USD").Return("line-a", nil)
	database.EXPECT().
		UpsertHoldingDeclaration(gomock.Any(), db.Holding{
			UserID: "user-7", Broker: "FIDELITY", Account: "Z1",
			InstrumentID: "inst-a", ListingID: "line-a",
		}, "100", gomock.Any(), gomock.Any()).
		Return(nil)

	res := processUserImport(context.Background(), ingestDeps{DB: database}, j)
	if !res.declarationsStored {
		t.Fatal("declarationsStored = false, want the stored declaration to earn the recalc")
	}
}

// The transaction part reads its vintage from the envelope, exactly as the
// system import's parts do. Without it the postings would be resolved against
// their own trade dates, and the name written for a contract would claim to
// have become correct before the file that stated it was written.
func TestProcessUserImport_TxPartResolvesAgainstTheEnvelopeVintage(t *testing.T) {
	database := userImportMock(t)
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
	database.EXPECT().SetJobPartStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

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

	exportedAt := mustParseDay("2024-09-01")
	payload, err := proto.Marshal(&archivev1.UserArchive{
		Envelope: &archivev1.Envelope{
			FormatVersion: 1,
			ExportedAt:    timestamppb.New(exportedAt),
			Kind:          archivev1.ArchiveKind_USER,
		},
		Txs: &archivev1.TxPart{Windows: []*archivev1.TxWindow{{
			Broker:       typev1.Broker_IBKR,
			PeriodFrom:   timestamppb.New(mustParseDay("2024-01-01")),
			PeriodBefore: timestamppb.New(mustParseDay("2025-01-01")),
			Source:       "IBKR:archive:export",
			Postings: []*archivev1.Posting{{
				OrderDate:             timestamppb.New(mustParseDay("2024-06-15")),
				TradeDate:             timestamppb.New(mustParseDay("2024-06-15")),
				Account:               "acct",
				BrokerTxType:          []typev1.TxType{typev1.TxType_TRADE_ASSET},
				InstrumentDescription: "AAPL 250117C00760000",
				Quantity:              "1",
				IdentifierHints: []*archivev1.InstrumentRef{
					{Type: typev1.IdentifierType_OCC, Value: "AAPL250117C00760000"},
				},
			}},
		}}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	j := &JobRequest{JobID: "job-ua-tx", JobType: db.JobTypeUserArchive}
	database.EXPECT().LoadJobPayload(gomock.Any(), j.JobID).Return(payload, nil)
	database.EXPECT().GetJob(gomock.Any(), j.JobID).Return(&db.JobDetail{
		UserID: "user-7",
		Parts:  partRows(archivev1.ArchivePart_TXS),
	}, nil).AnyTimes()

	processUserImport(context.Background(), ingestDeps{DB: database, Registry: registry}, j)

	if len(validFrom) == 0 {
		t.Fatal("no identifier was written")
	}
	for _, got := range validFrom {
		if got == nil || !got.Equal(exportedAt) {
			t.Errorf("valid_from = %v, want the envelope vintage %v", got, exportedAt)
		}
	}
}
