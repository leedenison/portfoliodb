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
		FindInstrumentByIdentifier(gomock.Any(), "CURRENCY", "", "USD").
		Return("inst-uuid-usd", nil)
	database.EXPECT().
		GetInstrument(gomock.Any(), "inst-uuid-usd").
		Return(&db.InstrumentRow{ID: "inst-uuid-usd", AssetClass: strPtr("CASH"), Currency: strPtr("USD"), Name: strPtr("US Dollar")}, nil)

	inst, ids, err := p.Identify(ctx, nil, "IBKR", "IBKR:test", "USD", identifier.Hints{}, hints)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if inst == nil {
		t.Fatal("expected instrument")
	}
	if inst.AssetClass != "CASH" || inst.Currency != "USD" || inst.Name != "US Dollar" {
		t.Errorf("instrument = %+v", inst)
	}
	if len(ids) != 1 || ids[0].Type != "CURRENCY" || ids[0].Value != "USD" {
		t.Errorf("identifiers = %+v", ids)
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
		FindInstrumentByIdentifier(gomock.Any(), "CURRENCY", "", "XXX").
		Return("", nil)

	inst, ids, err := p.Identify(ctx, nil, "", "", "", identifier.Hints{}, hints)
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("err = %v, want ErrNotIdentified", err)
	}
	if inst != nil || ids != nil {
		t.Errorf("expected nil inst and ids on ErrNotIdentified")
	}
}

func TestPlugin_Identify_NoCurrency_ReturnsErrNotIdentified(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	p := NewPlugin(database)

	ctx := context.Background()
	hints := []identifier.Identifier{{Type: "TICKER", Value: "AAPL"}}

	inst, ids, err := p.Identify(ctx, nil, "", "", "AAPL", identifier.Hints{}, hints)
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("err = %v, want ErrNotIdentified", err)
	}
	if inst != nil || ids != nil {
		t.Errorf("expected nil inst and ids")
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
