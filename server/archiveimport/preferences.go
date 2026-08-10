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
// settings the file states -- nought, one or two -- and a rejected setting is a
// validation error with a row index of -1, because there is no row to point at.
// A file stating neither is a part that succeeds having done nothing, which is
// what a present but empty section means.
//
// The two settings apply independently: a currency the file spells wrongly does
// not stop the ignore rules landing, and vice versa.
func PreferencePart(ctx context.Context, database db.DB, userID string, part *archivev1.PreferencePart, rep *PartReporter) (PreferenceResult, error) {
	var out PreferenceResult
	total := 0
	if part.DisplayCurrency != nil {
		total++
	}
	if part.IgnoredAssetClasses != nil {
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

	if part.IgnoredAssetClasses != nil {
		rules, ok := ignoredRules(part.GetIgnoredAssetClasses().GetRules(), rep)
		if ok {
			// An empty set is applied rather than skipped: it clears the user's
			// rules, and telling that apart from a file that does not state them
			// is what the wrapper message exists for.
			if err := database.SetIgnoredAssetClasses(ctx, userID, rules); err != nil {
				return out, fmt.Errorf("set ignored asset classes: %w", err)
			}
			out.Applied++
		}
		rep.Advance(ctx, 1)
	}
	return out, nil
}

// settingRow is the row index a problem with a whole setting is reported
// against. The reporter's other users point at a row or a group; a setting has
// neither, and -1 is already what a problem belonging to no row carries.
const settingRow = -1

// ignoredRules converts the file's rules, rejecting the setting as a whole if
// any one of them is unusable.
//
// All or nothing, because SetIgnoredAssetClasses replaces the set and deletes
// the transactions and declarations that set covers. Applying the rules the
// reader could read would delete on the strength of a file it could not read,
// and leave the user with a set they never wrote. Every problem is reported, not
// just the first, so one pass over the file names all of them.
func ignoredRules(rules []*archivev1.IgnoredAssetClassRule, rep *PartReporter) ([]db.IgnoredAssetClass, bool) {
	out := make([]db.IgnoredAssetClass, 0, len(rules))
	ok := true
	for i, r := range rules {
		broker := db.BrokerToStr(r.GetBroker())
		if broker == "" {
			rep.Errf(settingRow, "ignored_asset_classes", fmt.Sprintf("rule %d names no broker; the stored ignore rules were left alone", i))
			ok = false
			continue
		}
		assetClass := db.AssetClassToStr(r.GetAssetClass())
		if !db.ValidAssetClasses[assetClass] {
			rep.Errf(settingRow, "ignored_asset_classes", fmt.Sprintf("rule %d (%s) names no known asset class; the stored ignore rules were left alone", i, broker))
			ok = false
			continue
		}
		out = append(out, db.IgnoredAssetClass{
			Broker:     broker,
			Account:    r.GetAccount(),
			AssetClass: assetClass,
		})
	}
	return out, ok
}
