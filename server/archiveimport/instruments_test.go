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

// usdLine is the single USD line a fixture's security trades in, carrying
// whichever listing-grain names the test needs. Every security has at least one.
func usdLine(idns ...*archivev1.Identifier) []*archivev1.Listing {
	return []*archivev1.Listing{{Currency: "USD", Identifiers: idns}}
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
	database.EXPECT().EnsureArchiveInstrument(
		gomock.Any(), "STOCK", "Apple Inc.", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _, _ string, idns []db.IdentifierInput, set db.ListingSet, claims []db.IdentityClaim, _ string, _ *db.OptionFields, _ string) (string, string, error) {
			if len(idns) < 2 {
				t.Errorf("expected at least 2 security-grain identifiers, got %d", len(idns))
			}
			// The ticker names a line, so it travels on that line rather than
			// beside the ISIN.
			if len(set.Listings) != 1 || len(set.Listings[0].Identifiers) != 1 {
				t.Fatalf("listings = %+v; want one carrying the ticker", set.Listings)
			}
			// The instrument block names its identifiers together, whatever grain
			// each of them is, so it is one claim holding all of them.
			if len(claims) != 1 || len(claims[0].Identifiers) != len(idns)+1 {
				t.Fatalf("claims = %+v; want one holding all %d identifiers", claims, len(idns)+1)
			}
			for _, c := range claims[0].Identifiers {
				if c.Role != db.ClaimRoleReturned {
					t.Errorf("%s role = %q; a file states its names, it does not corroborate them", c.Ref.Type, c.Role)
				}
			}
			return "inst-1", "listing-id", nil
		})
	part := instrumentPart(&archivev1.Instrument{
		AssetClass: typev1.AssetClass_STOCK, Name: proto.String("Apple Inc."),
		Identifiers: []*archivev1.Identifier{
			{Type: typev1.IdentifierType_ISIN, Value: "US0378331005", Canonical: true},
			{Type: typev1.IdentifierType_BROKER_DESCRIPTION, Domain: "IBKR", Value: "AAPL"},
		},
		Listings: []*archivev1.Listing{{
			Currency: "USD",
			Identifiers: []*archivev1.Identifier{
				{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS", Canonical: true},
			},
		}},
	})
	ensured, err := InstrumentPart(context.Background(), database, part, rep, "")
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
	database.EXPECT().EnsureArchiveInstrument(
		gomock.Any(), "STOCK", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, gomock.Any()).
		Return("underlying-1", "listing-id", nil)
	// The option's strike is quoted in the currency the file states, and that
	// names the line of the underlying it delivers.
	database.EXPECT().EnsureListing(gomock.Any(), "underlying-1", "USD").Return("underlying-line-1", nil)
	database.EXPECT().EnsureArchiveInstrument(
		gomock.Any(), "OPTION", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "underlying-line-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _, _ string, _ []db.IdentifierInput, _ db.ListingSet, _ []db.IdentityClaim, _ string, opts *db.OptionFields, _ string) (string, string, error) {
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
			Listings:    usdLine(),
			// The ref names the line the contract delivers, currency included.
			Underlying: &archivev1.InstrumentRef{
				Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS", Currency: "USD",
			},
			Strike:  proto.String("150.5"),
			Expiry:  proto.String("2026-01-16"),
			PutCall: proto.String("C"),

			ContractMultiplier: proto.String("1.5"),
		},
		&archivev1.Instrument{
			AssetClass: typev1.AssetClass_STOCK,
			Listings:   usdLine(&archivev1.Identifier{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS", Canonical: true}),
		},
	)
	ensured, err := InstrumentPart(context.Background(), database, part, rep, "")
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
	// The ref names the line, so nothing is re-derived from the OCC symbol.
	database.EXPECT().EnsureListing(gomock.Any(), "known-1", "USD").Return("known-line-1", nil)
	database.EXPECT().EnsureArchiveInstrument(
		gomock.Any(), "OPTION", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "known-line-1", gomock.Any(), gomock.Any()).
		Return("option-1", "listing-id", nil)
	part := instrumentPart(&archivev1.Instrument{
		AssetClass:  typev1.AssetClass_OPTION,
		Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_OCC, Value: "AAPL  260116C00150500", Canonical: true}},
		Listings:    usdLine(),
		Underlying:  &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS", Currency: "USD"},
		Strike:      proto.String("150.5"),
		Expiry:      proto.String("2026-01-16"),
		PutCall:     proto.String("C"),
	})
	ensured, err := InstrumentPart(context.Background(), database, part, rep, "")
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
		Listings:    usdLine(),
		Underlying:  &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS", Currency: "USD"},
	})
	ensured, err := InstrumentPart(context.Background(), database, part, rep, "")
	if err != nil {
		t.Fatalf("InstrumentPart: %v", err)
	}
	if ensured != 0 || len(rep.Errors()) != 1 {
		t.Fatalf("expected 1 error, 0 ensured; got ensured=%d errors=%v", ensured, rep.Errors())
	}
}

// A security with no lines is ordinary: nobody has named one, and what is known
// about it names no line either. What it cannot lack is a name.
func TestInstrumentPart_NoListings(t *testing.T) {
	database, rep := newPartTest(t)
	expectAnyMerge(database)
	database.EXPECT().EnsureArchiveInstrument(
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
		gomock.Any(), gomock.Any(), gomock.Any(), "", nil, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _, _ string, _ []db.IdentifierInput, set db.ListingSet, _ []db.IdentityClaim, _ string, _ *db.OptionFields, _ string) (string, string, error) {
			if len(set.Listings) != 0 {
				t.Errorf("listings = %+v, want none", set.Listings)
			}
			if len(set.Unplaced) != 1 || set.Unplaced[0].Ref.Value != "AAPL" {
				t.Errorf("unplaced = %+v, want the ticker nobody could place", set.Unplaced)
			}
			return "inst-1", "", nil
		})
	part := instrumentPart(&archivev1.Instrument{
		UnplacedIdentifiers: []*archivev1.Identifier{
			{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS", Canonical: true},
		},
	})
	ensured, err := InstrumentPart(context.Background(), database, part, rep, "")
	if err != nil {
		t.Fatalf("InstrumentPart: %v", err)
	}
	if ensured != 1 || len(rep.Errors()) != 0 {
		t.Fatalf("expected 1 ensured, no errors; got ensured=%d, errors=%v", ensured, rep.Errors())
	}
}

// A security named by nothing at all, at either grain, cannot be stored either:
// the lookup has nothing to ask about and a created row would answer to no name.
func TestInstrumentPart_NoIdentifiersAtEitherGrain(t *testing.T) {
	database, rep := newPartTest(t)
	part := instrumentPart(&archivev1.Instrument{Listings: usdLine()})
	ensured, err := InstrumentPart(context.Background(), database, part, rep, "")
	if err != nil {
		t.Fatalf("InstrumentPart: %v", err)
	}
	if ensured != 0 || len(rep.Errors()) != 1 {
		t.Fatalf("expected 1 error, 0 ensured; got ensured=%d, errors=%d", ensured, len(rep.Errors()))
	}
}

func TestInstrumentPart_DuplicateTypeValueInPayload(t *testing.T) {
	database, rep := newPartTest(t)
	expectAnyMerge(database)
	// First instrument (ISIN 1) is ensured; second is rejected as duplicate (type, value) in payload.
	database.EXPECT().EnsureArchiveInstrument(
		gomock.Any(), "", "", "", "", gomock.Any(), gomock.Any(), gomock.Any(), "", nil, gomock.Any()).
		Return("inst-1", "listing-id", nil)
	part := instrumentPart(
		&archivev1.Instrument{Listings: usdLine(), Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_ISIN, Value: "1", Canonical: true}}},
		&archivev1.Instrument{Listings: usdLine(), Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_ISIN, Value: "1", Canonical: true}}},
	)
	ensured, err := InstrumentPart(context.Background(), database, part, rep, "")
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
	database.EXPECT().EnsureArchiveInstrument(
		gomock.Any(), "STOCK", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, gomock.Any()).
		Return("inst-1", "listing-id", nil)
	database.EXPECT().
		SaveProviderIdentifiers(gomock.Any(), "inst-1", gomock.Any(), []db.ProviderIdentifierInput{
			{Provider: "eodhd", Type: "EODHD_EXCH_CODE", Domain: "", Value: "US"},
			{Provider: "openfigi", Type: "FIGI", Domain: "XNAS", Value: "BBG000B9XRY4"},
		}).
		Return(nil)

	part := instrumentPart(&archivev1.Instrument{
		AssetClass: typev1.AssetClass_STOCK,
		Listings:   usdLine(&archivev1.Identifier{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS", Canonical: true}),
		// Security grain. A line's own provider identifiers travel with the line
		// and are placed by the ensure, not by this write.
		ProviderIdentifiers: []*archivev1.ProviderIdentifier{
			{Provider: "eodhd", IdentifierType: "EODHD_EXCH_CODE", Value: "US"},
			{Provider: "openfigi", IdentifierType: "FIGI", Value: "BBG000B9XRY4", Domain: "XNAS"},
		},
	})
	ensured, err := InstrumentPart(context.Background(), database, part, rep, "")
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
	database.EXPECT().EnsureArchiveInstrument(
		gomock.Any(), "STOCK", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, gomock.Any()).
		Return("inst-1", "listing-id", nil)
	// No SaveProviderIdentifiers expectation: the mock controller fails the test
	// if it is called.
	part := instrumentPart(&archivev1.Instrument{
		AssetClass: typev1.AssetClass_STOCK,
		Listings:   usdLine(&archivev1.Identifier{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS", Canonical: true}),
	})
	if _, err := InstrumentPart(context.Background(), database, part, rep, ""); err != nil {
		t.Fatalf("InstrumentPart: %v", err)
	}
	if len(rep.Errors()) != 0 {
		t.Fatalf("errors=%v", rep.Errors())
	}
}

// TestInstrumentPart_MergesWhatTheFileCarries covers the collision a rebuild
// always hits: the instance already has the instrument (migration 002 seeds
// every currency and FX pair), the ensure matches rather than creates, and the
// merge is what carries the file's identifiers and columns onto it.
//
// The line's own interval travels with the line rather than through the merge,
// which is why only the security-grain columns are checked here.
func TestInstrumentPart_MergesWhatTheFileCarries(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().EnsureArchiveInstrument(
		gomock.Any(), "FX", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, gomock.Any()).
		Return("seeded-eurusd", "listing-id", nil)
	database.EXPECT().
		MergeInstrumentFromArchive(gomock.Any(), "seeded-eurusd", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, in db.InstrumentMerge) error {
			// No currency travels with the merge. A currency is a fact about a
			// line, and every line the file states has already been ensured by
			// EnsureArchiveInstrument above.
			if in.AssetClass != "FX" || in.CIK != "0000320193" {
				t.Errorf("merge carried %+v", in)
			}
			if len(in.Identifiers) != 1 || in.Identifiers[0].Ref.Value != "EURUSD" {
				t.Errorf("identifiers = %+v", in.Identifiers)
			}
			return nil
		})

	part := instrumentPart(&archivev1.Instrument{
		AssetClass:  typev1.AssetClass_FX,
		Cik:         proto.String("0000320193"),
		Identifiers: []*archivev1.Identifier{{Type: typev1.IdentifierType_FX_PAIR, Value: "EURUSD", Canonical: true}},
		Listings:    []*archivev1.Listing{{Currency: "USD", ValidFrom: proto.String("1999-01-04")}},
	})
	ensured, err := InstrumentPart(context.Background(), database, part, rep, "")
	if err != nil {
		t.Fatalf("InstrumentPart: %v", err)
	}
	if ensured != 1 || len(rep.Errors()) != 0 {
		t.Fatalf("ensured=%d errors=%v", ensured, rep.Errors())
	}
}
