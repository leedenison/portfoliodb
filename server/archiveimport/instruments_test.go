package archiveimport

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
)

// newPartTest gives a mock database and a detached reporter, which is what a
// part reports through when there is no job behind it.
func newPartTest(t *testing.T) (*mock.MockDB, *PartReporter) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	return mock.NewMockDB(ctrl), NewDetachedReporter()
}

func instrumentPart(insts ...*archivev1.Instrument) *archivev1.InstrumentPart {
	return &archivev1.InstrumentPart{Instruments: insts}
}

// expectAnyMerge lets a test that is not about the gap-filling merge ignore it.
// Every ensured instrument gets one, because EnsureInstrument does not say
// whether it created the row or matched one the instance already had.
func expectAnyMerge(database *mock.MockDB) {
	database.EXPECT().MergeInstrumentFromArchive(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
}

func TestInstrumentPart_Success(t *testing.T) {
	database, rep := newPartTest(t)
	expectAnyMerge(database)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple Inc.", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, claims []db.IdentityClaim, _ string, _, _ interface{}, _ *db.OptionFields) (string, string, error) {
			if len(idns) < 2 {
				t.Errorf("expected at least 2 identifiers, got %d", len(idns))
			}
			// The instrument block names its identifiers together, so it is one
			// claim holding all of them rather than one claim each.
			if len(claims) != 1 || len(claims[0].Identifiers) != len(idns) {
				t.Fatalf("claims = %+v; want one holding all %d identifiers", claims, len(idns))
			}
			for _, c := range claims[0].Identifiers {
				if c.Role != db.ClaimRoleReturned {
					t.Errorf("%s role = %q; a file states its names, it does not corroborate them", c.Ref.Type, c.Role)
				}
			}
			return "inst-1", "listing-id", nil
		})
	part := instrumentPart(&archivev1.Instrument{
		AssetClass: typev1.AssetClass_STOCK, ExchangeMic: proto.String("XNAS"),
		Currency: "USD", Name: proto.String("Apple Inc."),
		Identifiers: []*archivev1.Identifier{
			{Type: typev1.IdentifierType_ISIN, Value: "US0378331005", Canonical: true},
			{Type: typev1.IdentifierType_BROKER_DESCRIPTION, Domain: "IBKR", Value: "AAPL"},
		},
	})
	ensured, err := InstrumentPart(context.Background(), database, part, rep)
	if err != nil {
		t.Fatalf("InstrumentPart: %v", err)
	}
	if ensured != 1 || len(rep.Errors()) != 0 {
		t.Fatalf("ensured_count=1, errors empty; got ensured_count=%d, errors=%v", ensured, rep.Errors())
	}
}

func TestInstrumentPart_RestoresOptionTermsAndMultiplier(t *testing.T) {
	database, rep := newPartTest(t)
	expectAnyMerge(database)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("underlying-1", "listing-id", nil)
	// The option's strike is quoted in the currency the file states, and that
	// names the line of the underlying it delivers.
	database.EXPECT().EnsureListing(gomock.Any(), "underlying-1", "USD").Return("underlying-line-1", nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "OPTION", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "underlying-line-1", nil, nil, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, _ []db.IdentifierInput, _ []db.IdentityClaim, _ string, _, _ interface{}, opts *db.OptionFields) (string, string, error) {
			if opts == nil {
				t.Fatal("option terms were not restored")
			}
			if !opts.Strike.Equal(decimal.RequireFromString("150.5")) || opts.PutCall != "C" {
				t.Errorf("option fields = %+v", opts)
			}
			if !opts.Expiry.Equal(time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)) {
				t.Errorf("expiry = %v", opts.Expiry)
			}
			return "option-1", "listing-id", nil
		})
	database.EXPECT().SetContractMultiplier(gomock.Any(), "option-1", decimal.RequireFromString("1.5")).Return(nil)

	part := instrumentPart(
		&archivev1.Instrument{
			AssetClass:  typev1.AssetClass_OPTION,
			Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_OCC, Value: "AAPL  260116C00150500", Canonical: true}},
			Underlying:  &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS"},
			Strike:      proto.String("150.5"),
			Expiry:      proto.String("2026-01-16"),
			PutCall:     proto.String("C"),

			ContractMultiplier: proto.String("1.5"),
		},
		&archivev1.Instrument{
			AssetClass:  typev1.AssetClass_STOCK,
			Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS", Canonical: true}},
		},
	)
	ensured, err := InstrumentPart(context.Background(), database, part, rep)
	if err != nil {
		t.Fatalf("InstrumentPart: %v", err)
	}
	if ensured != 2 || len(rep.Errors()) != 0 {
		t.Fatalf("ensured=%d errors=%v", ensured, rep.Errors())
	}
}

func TestInstrumentPart_UnderlyingRefNotInArchive_FallsBackToInstance(t *testing.T) {
	database, rep := newPartTest(t)
	expectAnyMerge(database)
	// The archive says an underlying appears in the same part, but a partial
	// file whose underlying this instance already knows still imports.
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").Return("known-1", nil)
	// The file states no currency, so the OCC symbol implies USD and that names
	// the underlying's line.
	database.EXPECT().EnsureListing(gomock.Any(), "known-1", "USD").Return("known-line-1", nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "OPTION", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "known-line-1", nil, nil, gomock.Any()).
		Return("option-1", "listing-id", nil)
	part := instrumentPart(&archivev1.Instrument{
		AssetClass:  typev1.AssetClass_OPTION,
		Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_OCC, Value: "AAPL  260116C00150500", Canonical: true}},
		Underlying:  &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS"},
		Strike:      proto.String("150.5"),
		Expiry:      proto.String("2026-01-16"),
		PutCall:     proto.String("C"),
	})
	ensured, err := InstrumentPart(context.Background(), database, part, rep)
	if err != nil {
		t.Fatalf("InstrumentPart: %v", err)
	}
	if ensured != 1 || len(rep.Errors()) != 0 {
		t.Fatalf("ensured=%d errors=%v", ensured, rep.Errors())
	}
}

func TestInstrumentPart_DanglingUnderlyingRef(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").Return("", nil)
	part := instrumentPart(&archivev1.Instrument{
		AssetClass:  typev1.AssetClass_OPTION,
		Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_OCC, Value: "AAPL  260116C00150500", Canonical: true}},
		Underlying:  &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS"},
	})
	ensured, err := InstrumentPart(context.Background(), database, part, rep)
	if err != nil {
		t.Fatalf("InstrumentPart: %v", err)
	}
	if ensured != 0 || len(rep.Errors()) != 1 {
		t.Fatalf("expected 1 error, 0 ensured; got ensured=%d errors=%v", ensured, rep.Errors())
	}
}

func TestInstrumentPart_EmptyIdentifiers(t *testing.T) {
	database, rep := newPartTest(t)
	ensured, err := InstrumentPart(context.Background(), database, instrumentPart(&archivev1.Instrument{}), rep)
	if err != nil {
		t.Fatalf("InstrumentPart: %v", err)
	}
	if ensured != 0 || len(rep.Errors()) != 1 {
		t.Fatalf("expected 1 error, 0 ensured; got ensured=%d, errors=%d", ensured, len(rep.Errors()))
	}
	if rep.Errors()[0].GetMessage() != "at least one identifier required" {
		t.Fatalf("got error %q", rep.Errors()[0].GetMessage())
	}
}

func TestInstrumentPart_DuplicateTypeValueInPayload(t *testing.T) {
	database, rep := newPartTest(t)
	expectAnyMerge(database)
	// First instrument (ISIN 1) is ensured; second is rejected as duplicate (type, value) in payload.
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "", "", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("inst-1", "listing-id", nil)
	part := instrumentPart(
		&archivev1.Instrument{Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_ISIN, Value: "1", Canonical: true}}},
		&archivev1.Instrument{Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_ISIN, Value: "1", Canonical: true}}},
	)
	ensured, err := InstrumentPart(context.Background(), database, part, rep)
	if err != nil {
		t.Fatalf("InstrumentPart: %v", err)
	}
	if ensured != 1 || len(rep.Errors()) != 1 {
		t.Fatalf("expected 1 ensured and 1 error (duplicate); got ensured=%d, errors=%d", ensured, len(rep.Errors()))
	}
}

// TestInstrumentPart_RestoresProviderIdentifiers covers the reason the archive
// exists: the recorded output of the paid identifier lookups is written straight
// back, so no plugin is called for a restored instrument.
func TestInstrumentPart_RestoresProviderIdentifiers(t *testing.T) {
	database, rep := newPartTest(t)
	expectAnyMerge(database)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("inst-1", "listing-id", nil)
	database.EXPECT().
		SaveProviderIdentifiers(gomock.Any(), "inst-1", gomock.Any(), []db.ProviderIdentifierInput{
			{Provider: "eodhd", Type: "EODHD_EXCH_CODE", Domain: "", Value: "US"},
			{Provider: "openfigi", Type: "FIGI", Domain: "XNAS", Value: "BBG000B9XRY4"},
		}).
		Return(nil)

	part := instrumentPart(&archivev1.Instrument{
		AssetClass:  typev1.AssetClass_STOCK,
		Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS", Canonical: true}},
		ProviderIdentifiers: []*archivev1.ProviderIdentifier{
			{Provider: "eodhd", IdentifierType: "EODHD_EXCH_CODE", Value: "US"},
			{Provider: "openfigi", IdentifierType: "FIGI", Value: "BBG000B9XRY4", Domain: "XNAS"},
		},
	})
	ensured, err := InstrumentPart(context.Background(), database, part, rep)
	if err != nil {
		t.Fatalf("InstrumentPart: %v", err)
	}
	if ensured != 1 || len(rep.Errors()) != 0 {
		t.Fatalf("ensured=%d errors=%v", ensured, rep.Errors())
	}
}

// An instrument stating no provider identifiers must not reach the database at
// all: an empty write is not the same statement as no write.
func TestInstrumentPart_NoProviderIdentifiers_NoWrite(t *testing.T) {
	database, rep := newPartTest(t)
	expectAnyMerge(database)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("inst-1", "listing-id", nil)
	// No SaveProviderIdentifiers expectation: the mock controller fails the test
	// if it is called.
	part := instrumentPart(&archivev1.Instrument{
		AssetClass:  typev1.AssetClass_STOCK,
		Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS", Canonical: true}},
	})
	if _, err := InstrumentPart(context.Background(), database, part, rep); err != nil {
		t.Fatalf("InstrumentPart: %v", err)
	}
	if len(rep.Errors()) != 0 {
		t.Fatalf("errors=%v", rep.Errors())
	}
}

// TestInstrumentPart_MergesWhatTheFileCarries covers the collision a rebuild
// always hits: the instance already has the instrument (migration 002 seeds
// every currency and FX pair), EnsureInstrument matches rather than creates, and
// the merge is what carries the file's identifiers and columns onto it.
func TestInstrumentPart_MergesWhatTheFileCarries(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "FX", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", gomock.Any(), nil, nil).
		Return("seeded-eurusd", "listing-id", nil)
	database.EXPECT().
		MergeInstrumentFromArchive(gomock.Any(), "seeded-eurusd", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, in db.InstrumentMerge) error {
			if in.AssetClass != "FX" || in.Currency != "USD" || in.CIK != "0000320193" {
				t.Errorf("merge carried %+v", in)
			}
			if in.ValidFrom == nil || !in.ValidFrom.Equal(time.Date(1999, 1, 4, 0, 0, 0, 0, time.UTC)) {
				t.Errorf("valid_from = %v", in.ValidFrom)
			}
			if len(in.Identifiers) != 1 || in.Identifiers[0].Ref.Value != "EURUSD" {
				t.Errorf("identifiers = %+v", in.Identifiers)
			}
			return nil
		})

	part := instrumentPart(&archivev1.Instrument{
		AssetClass:  typev1.AssetClass_FX,
		Currency:    "USD",
		Cik:         proto.String("0000320193"),
		ValidFrom:   proto.String("1999-01-04"),
		Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_FX_PAIR, Value: "EURUSD", Canonical: true}},
	})
	ensured, err := InstrumentPart(context.Background(), database, part, rep)
	if err != nil {
		t.Fatalf("InstrumentPart: %v", err)
	}
	if ensured != 1 || len(rep.Errors()) != 0 {
		t.Fatalf("ensured=%d errors=%v", ensured, rep.Errors())
	}
}
