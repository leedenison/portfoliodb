package archiveimport

import (
	"context"
	"fmt"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// PreferenceResult says what the part actually applied. DisplayCurrency is
// carried out so the worker can nudge the price fetcher once for the whole
// import, as SetDisplayCurrency does once per call: a restored instance with no
// FX rates for its display currency shows nothing until the next fetch cycle.
type PreferenceResult struct {
	Applied         int32
	DisplayCurrency bool
}

// PreferencePart applies a user archive's settings and reports through rep.
//
// The unit here is a setting rather than a row, so the part's total is how many
// settings the file states -- nought or one -- and a rejected setting is a
// validation error with a row index of -1, because there is no row to point at.
// A file stating none is a part that succeeds having done nothing, which is
// what a present but empty section means.
func PreferencePart(ctx context.Context, database db.DB, userID string, part *archivev1.PreferencePart, rep *PartReporter) (PreferenceResult, error) {
	var out PreferenceResult
	total := 0
	if part.DisplayCurrency != nil {
		total++
	}
	rep.Total(ctx, total)
	if total == 0 {
		return out, nil
	}

	if part.DisplayCurrency != nil {
		cc := part.GetDisplayCurrency()
		if !db.ValidCurrencyCode(cc) {
			rep.Errf(settingRow, "display_currency", fmt.Sprintf("%q is not a 3-letter ISO 4217 code; the stored display currency was left alone", cc))
		} else {
			if err := database.SetDisplayCurrency(ctx, userID, cc); err != nil {
				return out, fmt.Errorf("set display currency: %w", err)
			}
			out.Applied++
			out.DisplayCurrency = true
		}
		rep.Advance(ctx, 1)
	}
	return out, nil
}

// settingRow is the row index a problem with a whole setting is reported
// against. The reporter's other users point at a row or a group; a setting has
// neither, and -1 is already what a problem belonging to no row carries.
const settingRow = -1
