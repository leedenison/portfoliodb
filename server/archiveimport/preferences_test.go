package archiveimport

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
)

func rule(broker typev1.Broker, account string, ac typev1.AssetClass) *archivev1.IgnoredAssetClassRule {
	return &archivev1.IgnoredAssetClassRule{Broker: broker, Account: account, AssetClass: ac}
}

func ignored(rules ...*archivev1.IgnoredAssetClassRule) *archivev1.IgnoredAssetClasses {
	return &archivev1.IgnoredAssetClasses{Rules: rules}
}

func applyPreferences(t *testing.T, database *mock.MockDB, rep *PartReporter, part *archivev1.PreferencePart) PreferenceResult {
	t.Helper()
	res, err := PreferencePart(context.Background(), database, "user-1", part, rep)
	if err != nil {
		t.Fatalf("PreferencePart: %v", err)
	}
	return res
}

func TestPreferencePart_AppliesBothSettings(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().SetDisplayCurrency(gomock.Any(), "user-1", "GBP").Return(nil)
	database.EXPECT().
		SetIgnoredAssetClasses(gomock.Any(), "user-1",
			[]db.IgnoredAssetClass{{Broker: "IBKR", Account: "U123", AssetClass: "OPTION"}},
			gomock.Any()).
		Return(nil)

	res := applyPreferences(t, database, rep, &archivev1.PreferencePart{
		DisplayCurrency:     proto.String("GBP"),
		IgnoredAssetClasses: ignored(rule(typev1.Broker_IBKR, "U123", typev1.AssetClass_OPTION)),
	})
	if res.Applied != 2 || !res.DisplayCurrency {
		t.Fatalf("result = %+v, want both settings applied", res)
	}
	if rep.ErrCount() != 0 {
		t.Fatalf("errors = %v, want none", rep.Errors())
	}
}

// The two settings are independent, so a file stating one says nothing about
// the other and the other's setter is never called.
func TestPreferencePart_CurrencyOnly(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().SetDisplayCurrency(gomock.Any(), "user-1", "USD").Return(nil)

	res := applyPreferences(t, database, rep, &archivev1.PreferencePart{DisplayCurrency: proto.String("USD")})
	if res.Applied != 1 || !res.DisplayCurrency {
		t.Fatalf("result = %+v", res)
	}
}

func TestPreferencePart_RulesOnly(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().SetIgnoredAssetClasses(gomock.Any(), "user-1", gomock.Any(), gomock.Any()).Return(nil)

	res := applyPreferences(t, database, rep, &archivev1.PreferencePart{
		IgnoredAssetClasses: ignored(rule(typev1.Broker_FIDELITY, "", typev1.AssetClass_STOCK)),
	})
	if res.Applied != 1 || res.DisplayCurrency {
		t.Fatalf("result = %+v, want no display currency", res)
	}
}

// An empty rules list is applied, which clears the user's rules. Telling that
// apart from a file that does not state them is what the wrapper exists for.
func TestPreferencePart_EmptyRulesClearsThem(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().
		SetIgnoredAssetClasses(gomock.Any(), "user-1", []db.IgnoredAssetClass{}, gomock.Any()).
		Return(nil)

	res := applyPreferences(t, database, rep, &archivev1.PreferencePart{IgnoredAssetClasses: ignored()})
	if res.Applied != 1 {
		t.Fatalf("result = %+v, want the empty set applied", res)
	}
}

// A part present and empty is a part that succeeds having done nothing: the
// export included it and there was nothing to say.
func TestPreferencePart_NeitherSettingWritesNothing(t *testing.T) {
	database, rep := newPartTest(t)
	res := applyPreferences(t, database, rep, &archivev1.PreferencePart{})
	if res.Applied != 0 || res.DisplayCurrency {
		t.Fatalf("result = %+v, want nothing applied", res)
	}
	if rep.ErrCount() != 0 {
		t.Fatalf("errors = %v, want none", rep.Errors())
	}
}

// A rejected setting is a validation error, not a failed part, and it carries a
// row index of -1 because there is no row to point at.
func TestPreferencePart_BadCurrencyRejectedAloneAndRulesStillLand(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().SetIgnoredAssetClasses(gomock.Any(), "user-1", gomock.Any(), gomock.Any()).Return(nil)

	res := applyPreferences(t, database, rep, &archivev1.PreferencePart{
		DisplayCurrency:     proto.String("gbp"),
		IgnoredAssetClasses: ignored(rule(typev1.Broker_IBKR, "", typev1.AssetClass_STOCK)),
	})
	if res.DisplayCurrency {
		t.Fatal("display currency reported as applied")
	}
	if res.Applied != 1 {
		t.Fatalf("applied = %d, want only the rules", res.Applied)
	}
	if rep.ErrCount() != 1 {
		t.Fatalf("errors = %v, want one", rep.Errors())
	}
	e := rep.Errors()[0]
	if e.GetRowIndex() != -1 || e.GetField() != "display_currency" {
		t.Fatalf("error = %+v", e)
	}
}

// All or nothing: the setter replaces the whole set and deletes the txs that set
// covers, so applying the rules the reader could read would delete on the
// strength of a file it could not read.
func TestPreferencePart_OneBadRuleRejectsTheWholeSetting(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().SetDisplayCurrency(gomock.Any(), "user-1", "GBP").Return(nil)
	// No SetIgnoredAssetClasses expectation: calling it at all is the failure.

	res := applyPreferences(t, database, rep, &archivev1.PreferencePart{
		DisplayCurrency: proto.String("GBP"),
		IgnoredAssetClasses: ignored(
			rule(typev1.Broker_IBKR, "", typev1.AssetClass_STOCK),
			rule(typev1.Broker_FIDELITY, "", typev1.AssetClass_ASSET_CLASS_UNSPECIFIED),
			rule(typev1.Broker_IBKR, "", typev1.AssetClass_OPTION),
		),
	})
	if res.Applied != 1 || !res.DisplayCurrency {
		t.Fatalf("result = %+v, want only the currency applied", res)
	}
	if rep.ErrCount() != 1 {
		t.Fatalf("errors = %v, want one", rep.Errors())
	}
	if f := rep.Errors()[0].GetField(); f != "ignored_asset_classes" {
		t.Fatalf("field = %q", f)
	}
}

func TestPreferencePart_UnspecifiedBrokerRejectsTheSetting(t *testing.T) {
	database, rep := newPartTest(t)
	res := applyPreferences(t, database, rep, &archivev1.PreferencePart{
		IgnoredAssetClasses: ignored(rule(typev1.Broker_BROKER_UNSPECIFIED, "", typev1.AssetClass_STOCK)),
	})
	if res.Applied != 0 {
		t.Fatalf("result = %+v, want nothing applied", res)
	}
	if rep.ErrCount() != 1 {
		t.Fatalf("errors = %v, want one", rep.Errors())
	}
}

// Every problem is named on one pass, rather than the first stopping the read.
func TestPreferencePart_ReportsEveryBadRule(t *testing.T) {
	database, rep := newPartTest(t)
	applyPreferences(t, database, rep, &archivev1.PreferencePart{
		IgnoredAssetClasses: ignored(
			rule(typev1.Broker_BROKER_UNSPECIFIED, "", typev1.AssetClass_STOCK),
			rule(typev1.Broker_IBKR, "", typev1.AssetClass_ASSET_CLASS_UNSPECIFIED),
		),
	})
	if rep.ErrCount() != 2 {
		t.Fatalf("errors = %v, want two", rep.Errors())
	}
}

// A write that does not land fails the part, as it does in every other part.
func TestPreferencePart_SetterErrorFailsThePart(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().SetDisplayCurrency(gomock.Any(), "user-1", "GBP").Return(errors.New("boom"))

	_, err := PreferencePart(context.Background(), database, "user-1",
		&archivev1.PreferencePart{DisplayCurrency: proto.String("GBP")}, rep)
	if err == nil {
		t.Fatal("expected a hard failure")
	}
}
