package identifier

import (
	"context"
	"errors"
	"testing"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	"go.uber.org/mock/gomock"
)

func strPtr(s string) *string { return &s }

func TestPlugin_Identify_CurrencyFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	p := NewPlugin(database)

	ctx := context.Background()
	hints := []identifier.Identifier{{Type: "CURRENCY", Value: "USD"}}

	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), gomock.Any(), "CURRENCY", "", "USD").
		Return("inst-uuid-usd", nil)
	database.EXPECT().
		GetInstrument(gomock.Any(), "inst-uuid-usd").
		Return(&db.InstrumentRow{ID: "inst-uuid-usd", AssetClass: strPtr("CASH"), Name: strPtr("US Dollar"),
			// A cash instrument has a listing degenerately, and that line is where
			// the currency it is lives.
			Listings: []*db.Listing{{Currency: "USD"}}}, nil)

	res, err := p.Identify(ctx, nil, "IBKR", "IBKR:test", "USD", identifier.Identity{Stated: hints, Hints: identifier.Hints{}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if res.Instrument == nil {
		t.Fatal("expected instrument")
	}
	if res.Instrument.AssetClass != "CASH" || res.Instrument.Listing.Currency != "USD" || res.Instrument.Name != "US Dollar" {
		t.Errorf("instrument = %+v", res.Instrument)
	}
	if len(res.Identifiers) != 1 || res.Identifiers[0].Type != "CURRENCY" || res.Identifiers[0].Value != "USD" {
		t.Errorf("identifiers = %+v", res.Identifiers)
	}
	if res.Telemetry.Outcome != identifier.OutcomeIdentified {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, identifier.OutcomeIdentified)
	}
}

func TestPlugin_Identify_CurrencyNotFound_ReturnsErrNotIdentified(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	p := NewPlugin(database)

	ctx := context.Background()
	hints := []identifier.Identifier{{Type: "CURRENCY", Value: "XXX"}}

	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), gomock.Any(), "CURRENCY", "", "XXX").
		Return("", nil)

	res, err := p.Identify(ctx, nil, "", "", "", identifier.Identity{Stated: hints, Hints: identifier.Hints{}})
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("err = %v, want ErrNotIdentified", err)
	}
	if res.Instrument != nil || res.Identifiers != nil {
		t.Errorf("expected nil inst and ids on ErrNotIdentified")
	}
	if res.Telemetry.Outcome != identifier.OutcomeNotIdentified {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, identifier.OutcomeNotIdentified)
	}
}

func TestPlugin_Identify_NoCurrency_ReturnsErrNotIdentified(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	p := NewPlugin(database)

	ctx := context.Background()
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}

	res, err := p.Identify(ctx, nil, "", "", "AAPL", identifier.Identity{Stated: hints, Hints: identifier.Hints{}})
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("err = %v, want ErrNotIdentified", err)
	}
	if res.Instrument != nil || res.Identifiers != nil {
		t.Errorf("expected nil inst and ids")
	}
	if res.Telemetry.Outcome != identifier.OutcomeNotIdentified {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, identifier.OutcomeNotIdentified)
	}
}

func TestPlugin_AcceptableSecurityTypes_IncludesCash(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	p := NewPlugin(database)
	set := p.AcceptableSecurityTypes()
	if !set[identifier.SecurityTypeHintCash] {
		t.Errorf("AcceptableSecurityTypes = %v, want to include Cash", set)
	}
}
