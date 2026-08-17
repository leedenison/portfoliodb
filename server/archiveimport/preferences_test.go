package archiveimport

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/db/mock"
)

func applyPreferences(t *testing.T, database *mock.MockDB, rep *PartReporter, part *archivev1.PreferencePart) PreferenceResult {
	t.Helper()
	res, err := PreferencePart(context.Background(), database, "user-1", part, rep)
	if err != nil {
		t.Fatalf("PreferencePart: %v", err)
	}
	return res
}

func TestPreferencePart_AppliesTheCurrency(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().SetDisplayCurrency(gomock.Any(), "user-1", "GBP").Return(nil)

	res := applyPreferences(t, database, rep, &archivev1.PreferencePart{
		DisplayCurrency: proto.String("GBP"),
	})
	if res.Applied != 1 || !res.DisplayCurrency {
		t.Fatalf("result = %+v, want the currency applied", res)
	}
	if rep.ErrCount() != 0 {
		t.Fatalf("errors = %v, want none", rep.Errors())
	}
}

// A part present and empty is a part that succeeds having done nothing: the
// export included it and there was nothing to say.
func TestPreferencePart_NoSettingWritesNothing(t *testing.T) {
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
func TestPreferencePart_BadCurrencyRejectedAndStoredValueLeftAlone(t *testing.T) {
	database, rep := newPartTest(t)
	// No SetDisplayCurrency expectation: calling it at all is the failure.

	res := applyPreferences(t, database, rep, &archivev1.PreferencePart{
		DisplayCurrency: proto.String("gbp"),
	})
	if res.Applied != 0 || res.DisplayCurrency {
		t.Fatalf("result = %+v, want nothing applied", res)
	}
	if rep.ErrCount() != 1 {
		t.Fatalf("errors = %v, want one", rep.Errors())
	}
	e := rep.Errors()[0]
	if e.GetRowIndex() != -1 || e.GetField() != "display_currency" {
		t.Fatalf("error = %+v", e)
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
