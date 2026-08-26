package ingestion

import (
	"context"
	"testing"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Ownership governs what arrives through a transaction upload, and the postings
// of a user archive are one: they state identifier hints and carry no instrument
// data, exactly as a broker file does. So a name learned from them is owned by
// whoever the archive belongs to, and the lookups that place it are scoped to
// them. See docs/adr/0063-identity-claims-are-owned-until-users-corroborate-them.md.
//
// The instrument part of a system archive is the other side of this, and it needs
// no test: importing one is admin-only and EnsureArchiveInstrument takes no owner
// at all, so there is no call that could pass one.
func TestProcessUserImport_ThePostingsAreResolvedAsTheArchivesOwner(t *testing.T) {
	database := userImportMock(t)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().ListInstrumentsByIDs(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	database.EXPECT().AppendIdentificationErrors(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().InstrumentsWithSplits(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	database.EXPECT().ReplaceTxsInPeriod(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().SetJobPartStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().ListHoldingDeclarations(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	// Every lookup the resolution takes is scoped to the archive's owner. The
	// description names nothing, so the posting degrades to an instrument that
	// is only that description.
	database.EXPECT().FindInstrumentBySourceDescription(gomock.Any(), "user-7", gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	database.EXPECT().FindDescriptionOnlyInstrument(gomock.Any(), gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()

	var written []db.IdentifierInput
	var lookupOwner string
	database.EXPECT().EnsureInstrument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, owner, _, _, _, _, _ string, idns []db.IdentifierInput, _ []db.IdentityClaim, _ string, _ *db.OptionFields, _ string) (string, string, error) {
			lookupOwner, written = owner, idns
			return "inst-1", "listing-id", nil
		}).AnyTimes()

	payload, err := proto.Marshal(&archivev1.UserArchive{
		Envelope: &archivev1.Envelope{
			FormatVersion: 1,
			ExportedAt:    timestamppb.New(mustParseDay("2024-09-01")),
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
				InstrumentDescription: "SOMETHING NOBODY KNOWS",
				Quantity:              "1",
			}},
		}}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	j := &JobRequest{JobID: "job-ua-owner", JobType: db.JobTypeUserArchive}
	database.EXPECT().LoadJobPayload(gomock.Any(), j.JobID).Return(payload, nil).AnyTimes()
	database.EXPECT().GetJob(gomock.Any(), j.JobID).Return(&db.JobDetail{
		UserID: "user-7",
		Parts:  partRows(archivev1.ArchivePart_TXS),
	}, nil).AnyTimes()

	processJob(context.Background(), WorkerOptions{DB: database, IdentifierRegistry: identifier.NewRegistry()}, j)

	if lookupOwner != "user-7" {
		t.Errorf("resolution was carried out for %q, want the archive's owner", lookupOwner)
	}
	if len(written) == 0 {
		t.Fatal("no identifier was written")
	}
	for _, idn := range written {
		if idn.Owner != "user-7" {
			t.Errorf("%s was written owned by %q, want the archive's owner", idn.Ref.Type, idn.Owner)
		}
	}
}
