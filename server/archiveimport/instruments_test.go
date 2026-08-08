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

func TestInstrumentPart_Success(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple Inc.", gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ string, _, _ interface{}, _ *db.OptionFields) (string, error) {
			if len(idns) < 2 {
				t.Errorf("expected at least 2 identifiers, got %d", len(idns))
			}
			return "inst-1", nil
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
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("underlying-1", nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "OPTION", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "underlying-1", nil, nil, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, _ []db.IdentifierInput, _ string, _, _ interface{}, opts *db.OptionFields) (string, error) {
			if opts == nil {
				t.Fatal("option terms were not restored")
			}
			if !opts.Strike.Equal(decimal.RequireFromString("150.5")) || opts.PutCall != "C" {
				t.Errorf("option fields = %+v", opts)
			}
			if !opts.Expiry.Equal(time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)) {
				t.Errorf("expiry = %v", opts.Expiry)
			}
			return "option-1", nil
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
	// The archive says an underlying appears in the same part, but a partial
	// file whose underlying this instance already knows still imports.
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").Return("known-1", nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "OPTION", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "known-1", nil, nil, gomock.Any()).
		Return("option-1", nil)
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
	// First instrument (ISIN 1) is ensured; second is rejected as duplicate (type, value) in payload.
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "", "", "", gomock.Any(), "", nil, nil, nil).
		Return("inst-1", nil)
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
