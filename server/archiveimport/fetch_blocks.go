package archiveimport

import (
	"context"
	"fmt"
	"time"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// FetchBlockPart restores an archive's fetch blocks and reports through rep. It
// returns how many blocks were written.
//
// An instrument is looked up by its identifier and never created, which is the
// one place this part differs from prices and corporate events. Those resolve an
// unknown instrument through the identifier plugins because their rows are worth
// having either way; a block is a record that a plugin should not be called
// about an instrument, so paying a plugin to invent one to hang it off would be
// self-defeating. A block naming an instrument the instance does not have is
// rejected instead.
//
// asOf is the envelope's knowledge time and stands in for a block whose file
// does not state when it was first blocked.
func FetchBlockPart(ctx context.Context, database db.DB, part *archivev1.FetchBlockPart, asOf *time.Time, rep *PartReporter) (int32, error) {
	groups := part.GetGroups()
	rep.Total(ctx, len(groups))
	if len(groups) == 0 {
		return 0, nil
	}

	fallback := time.Now()
	if asOf != nil {
		fallback = *asOf
	}

	var price, events []db.FetchBlockInput
	for i, g := range groups {
		instrumentID := findArchiveInstrument(ctx, database, g.GetInstrument(), i, rep)
		rep.Advance(ctx, 1)
		if instrumentID == "" {
			continue
		}
		for _, b := range g.GetBlocks() {
			in := db.FetchBlockInput{
				InstrumentID:   instrumentID,
				PluginID:       b.GetPluginId(),
				Reason:         b.GetReason(),
				FirstBlockedAt: fallback,
			}
			if ts := b.GetFirstBlockedAt(); ts != nil {
				in.FirstBlockedAt = ts.AsTime()
			}
			switch b.GetCategory() {
			case typev1.PluginCategory_PRICE:
				price = append(price, in)
			case typev1.PluginCategory_CORPORATE_EVENT:
				events = append(events, in)
			default:
				rep.Errf(i, "category", fmt.Sprintf("%s has no fetch block table", b.GetCategory()))
			}
		}
	}

	if err := database.UpsertPriceFetchBlocks(ctx, price); err != nil {
		return 0, fmt.Errorf("upsert price fetch blocks: %w", err)
	}
	if err := database.UpsertCorporateEventFetchBlocks(ctx, events); err != nil {
		return int32(len(price)), fmt.Errorf("upsert corporate event fetch blocks: %w", err)
	}
	return int32(len(price) + len(events)), nil
}

// findArchiveInstrument maps a file's instrument reference to an instrument this
// instance already has, reporting a miss against the group rather than creating
// one. It returns "" when there is nothing to attach the group's rows to.
func findArchiveInstrument(ctx context.Context, database db.DB, ref *archivev1.InstrumentRef, idx int, rep *PartReporter) string {
	if ref.GetValue() == "" {
		rep.Errf(idx, "instrument", "group names no instrument")
		return ""
	}
	id, err := database.FindInstrumentByIdentifier(ctx, ref.GetType().String(), ref.GetDomain(), ref.GetValue())
	if err != nil {
		rep.Errf(idx, "instrument", fmt.Sprintf("%s %s: %v", ref.GetType(), ref.GetValue(), err))
		return ""
	}
	if id == "" {
		rep.Errf(idx, "instrument", fmt.Sprintf("%s %s is not an instrument this instance has", ref.GetType(), ref.GetValue()))
	}
	return id
}
