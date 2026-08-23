package pricefetcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/pluginutil"
	"github.com/leedenison/portfoliodb/server/worker"
)

const DefaultPricePluginTimeout = 60 * time.Second

// RunWorker processes price fetch cycles triggered via the trigger channel.
// It blocks until ctx is cancelled. Each signal on trigger runs one cycle;
// rapid signals are debounced (buffered channel of size 1).
func RunWorker(ctx context.Context, database db.DB, registry *Registry, tel db.TelemetryDB, log *slog.Logger, trigger <-chan struct{}, workers *worker.Registry) {
	if tel == nil {
		tel = db.NopTelemetry{}
	}
	const name = "price_fetcher"
	if workers != nil {
		workers.SetIdle(name)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-trigger:
			if !ok {
				return
			}
			runID := tel.StartRun(ctx, db.TelemetryRun{Kind: db.TelemetryRunPriceFetchCycle})
			outcome := db.TelemetryOutcomeSuccess
			if err := runCycle(ctx, database, registry, log, workers, tel, runID); err != nil {
				outcome = db.TelemetryOutcomeFailed
			}
			tel.EndRun(ctx, runID, outcome)
		}
	}
}

// pluginEntry pairs a registered plugin with its config row.
type pluginEntry struct {
	id     string
	plugin Plugin
	config []byte
	// precedence is the configured order the plugins are tried in, higher first.
	// Carried for telemetry: a plugin skipped by a filter writes no row, so a gap
	// in the recorded sequence is what says it was skipped.
	precedence  int
	maxHistDays *int
}

func runCycle(ctx context.Context, database db.DB, registry *Registry, log *slog.Logger, workers *worker.Registry, tel db.TelemetryDB, runID string) error {
	const name = "price_fetcher"
	defer func() {
		if workers != nil {
			workers.SetIdle(name)
			workers.CycleDone(name)
		}
	}()

	opts := db.HeldRangesOpts{ExtendToToday: true}

	gaps, err := database.PriceGaps(ctx, opts)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "price fetch: gaps", "err", err)
		}
		return err
	}

	fxGaps, err := database.FXGaps(ctx, opts)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "price fetch: fx gaps", "err", err)
		}
		return err
	}

	allGaps := make([]db.ListingDateRanges, 0, len(gaps)+len(fxGaps))
	allGaps = append(allGaps, gaps...)
	allGaps = append(allGaps, fxGaps...)
	if len(allGaps) == 0 {
		return nil
	}
	// Where the FX gaps start, which is the only thing that tells the two apart
	// once they are one list.
	fxFrom := len(gaps)

	if workers != nil {
		workers.SetRunning(name, fmt.Sprintf("Fetching prices for %d listings", len(allGaps)))
	}

	configs, err := database.ListEnabledPluginConfigs(ctx, db.PluginCategoryPrice)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "price fetch: list configs", "err", err)
		}
		return err
	}
	if len(configs) == 0 {
		// A cycle with work and nowhere to send it looks identical to a quiet one
		// unless it says so. The instruments are deliberately not loaded for this:
		// their attributes explain plugin filtering, and no filtering happened.
		newPriceGaps(ctx, tel, runID, allGaps, fxFrom, nil, nil).
			endAll(ctx, db.TelemetryGapNoEligiblePlugin)
		return nil
	}

	var plugins []pluginEntry
	for _, cfg := range configs {
		p := registry.Get(cfg.PluginID)
		if p == nil {
			continue
		}
		plugins = append(plugins, pluginEntry{
			id:          cfg.PluginID,
			plugin:      p,
			config:      cfg.Config,
			precedence:  cfg.Precedence,
			maxHistDays: cfg.MaxHistoryDays,
		})
	}
	if len(plugins) == 0 {
		newPriceGaps(ctx, tel, runID, allGaps, fxFrom, nil, nil).
			endAll(ctx, db.TelemetryGapNoEligiblePlugin)
		return nil
	}

	// The lines the gaps are on, and the securities above them. A gap names a
	// listing and so does its block; the asset class is the security's, a
	// security being one kind of thing whichever currency it trades in.
	listingIDs := extractListingIDs(allGaps)
	listingByID, err := database.ListingsByIDs(ctx, listingIDs)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "price fetch: load listings", "err", err)
		}
		return err
	}
	instIDs := make([]string, 0, len(listingByID))
	seen := make(map[string]bool, len(listingByID))
	for _, l := range listingByID {
		if !seen[l.InstrumentID] {
			seen[l.InstrumentID] = true
			instIDs = append(instIDs, l.InstrumentID)
		}
	}

	// Batch-load blocked (listing, plugin) pairs.
	blocked, err := database.BlockedPluginsForListings(ctx, listingIDs)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "price fetch: load blocks", "err", err)
		}
		return err
	}

	// Batch-load all instruments for the gaps
	instRows, err := database.ListInstrumentsByIDs(ctx, instIDs)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "price fetch: load instruments", "err", err)
		}
		return err
	}
	instByID := make(map[string]*db.InstrumentRow, len(instRows))
	for _, r := range instRows {
		instByID[r.ID] = r
	}

	// Per-plugin coverage, so a range one plugin has already answered for is not
	// put to it again -- including ranges it answered with nothing.
	coverage, err := database.PriceCoverageByPlugin(ctx, listingIDs)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "price fetch: load coverage", "err", err)
		}
		return err
	}

	gapsTel := newPriceGaps(ctx, tel, runID, allGaps, fxFrom, listingByID, instByID)
	return processGaps(ctx, database, plugins, allGaps, listingByID, instByID, blocked, coverage, log, gapsTel)
}

// processGaps iterates listing gaps and fetches prices from matching plugins.
//
// It returns ctx.Err() when the cycle is cancelled part way. The gaps it never
// reached stay unstamped, so where it stopped is readable; returning nil instead
// would stamp the run success and report a cycle that covered three listings of
// five hundred as a clean one.
func processGaps(ctx context.Context, database db.DB, plugins []pluginEntry, gaps []db.ListingDateRanges, listingByID map[string]*db.Listing, instByID map[string]*db.InstrumentRow, blocked map[string]map[string]bool, coverage map[string]map[string][]db.DateRange, log *slog.Logger, gapsTel *priceGaps) error {
	// One fetch time for the whole cycle, so every row a back-adjusting plugin
	// returns shares the same share count basis.
	now := time.Now()
	for i, ig := range gaps {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lst := listingByID[ig.ListingID]
		var inst *db.InstrumentRow
		if lst != nil {
			inst = instByID[lst.InstrumentID]
		}
		if inst == nil {
			if log != nil {
				log.WarnContext(ctx, "price fetch: listing or instrument not found", "listing", ig.ListingID)
			}
			gapsTel.end(ctx, i, db.TelemetryGapListingMissing)
			continue
		}

		fetchedByPlugin := false
		// Whether any plugin got past the filters, and how many rows arrived.
		// Together with whether anything was actually called they are what
		// gapOutcome reads, and each distinguishes a case the others cannot.
		reached := false
		bars := 0
		for _, pe := range plugins {
			if !pluginutil.PluginAcceptsListing(pe.plugin.AcceptableAssetClasses(), pe.plugin.AcceptableExchanges(), pe.plugin.AcceptableCurrencies(), inst.AssetClass, lst) {
				continue
			}
			if blocked[ig.ListingID][pe.id] {
				continue
			}
			// This line's identifiers and its security's, not a sibling line's:
			// a price request is keyed on a ticker as often as on an ISIN, and
			// the GBP and USD lines of one security carry different tickers.
			ids := pluginutil.FilterIdentifiers(pe.plugin.SupportedIdentifierTypes(), inst.IdentifiersFor(ig.ListingID))
			// Merge provider-specific identifiers for this plugin.
			for _, pi := range inst.ProviderIdentifiersFor(ig.ListingID) {
				if pi.Provider == pe.id {
					ids = append(ids, db.IdentifierInput{
						Ref: db.InstrumentRef{Type: pi.Type, Value: pi.Value, Domain: pi.Domain},
					})
				}
			}
			ids = pluginutil.FilterIdentifiers(pe.plugin.SupportedIdentifierTypes(), ids)
			if len(ids) == 0 {
				continue
			}
			// Past every filter, so this plugin was a candidate whatever becomes
			// of it below. None of the four skips above writes a row: no call was
			// made, and a gap in the recorded precedence is how they read.
			reached = true

			// Ranges this plugin has already answered for are not put to it
			// again, whether it returned a series or nothing at all.
			outstanding := db.SubtractRanges(ig.Ranges, coverage[ig.ListingID][pe.id])
			if len(outstanding) == 0 {
				// Nothing left to ask this one. It has not handled the
				// instrument, so the next plugin still gets its turn.
				continue
			}

			pfIDs := pluginutil.ToIdentifiers(ids)
			allOK := true
			for _, gap := range outstanding {
				gap := gap // copy for truncation
				if pe.maxHistDays != nil && *pe.maxHistDays > 0 {
					cutoff := time.Now().UTC().Truncate(db.Day).AddDate(0, 0, -*pe.maxHistDays)
					if !gap.Before.After(cutoff) {
						// The plugin cannot reach this far back and never will,
						// so record it as covered by this plugin rather than
						// rediscovering the same gap every cycle. Other plugins
						// keep their own coverage and are still offered it.
						coverRange(ctx, database, ig.ListingID, pe.id, gap, "history limit", log)
						gapsTel.call(ctx, i, priceCall(pe, gap, 0, db.TelemetryPriceCallHistoryLimit, nil))
						continue
					}
					if gap.From.Before(cutoff) {
						// The unreachable head is settled for this plugin even
						// though the tail is about to be fetched.
						head := db.DateRange{From: gap.From, Before: cutoff}
						coverRange(ctx, database, ig.ListingID, pe.id, head, "history limit", log)
						// Two rows for two ranges. The head was settled without
						// asking and the tail is about to be fetched, and a grain
						// of one range per row is what lets them differ.
						gapsTel.call(ctx, i, priceCall(pe, head, 0, db.TelemetryPriceCallHistoryLimit, nil))
						gap.From = cutoff
					}
				}
				var assetClass string
				if inst.AssetClass != nil {
					assetClass = *inst.AssetClass
				}
				callCtx, callCancel := context.WithTimeout(ctx, pluginutil.TimeoutFromConfig(pe.config, DefaultPricePluginTimeout))
				started := time.Now()
				result, err := pe.plugin.FetchPrices(callCtx, pe.config, pfIDs, assetClass, gap.From, gap.Before)
				took := time.Since(started)
				callCancel()
				if err != nil {
					var permErr *ErrPermanent
					if errors.As(err, &permErr) {
						_ = database.CreatePriceFetchBlock(ctx, ig.ListingID, pe.id, permErr.Reason)
						if log != nil {
							log.WarnContext(ctx, "price fetch: permanent block",
								"plugin", pe.id, "listing", ig.ListingID, "reason", permErr.Reason)
						}
						gapsTel.call(ctx, i, priceCall(pe, gap, 0, db.TelemetryPriceCallPermanentBlock, &took))
						allOK = false
						break // skip remaining ranges, try next plugin
					}
					if err == ErrNoData {
						// "Nothing here" is an answer, not a failure to answer:
						// record it so a pre-IPO, delisted or untraded range is
						// settled for this plugin instead of being asked about
						// on every cycle forever.
						coverRange(ctx, database, ig.ListingID, pe.id, gap, "no data", log)
						gapsTel.call(ctx, i, priceCall(pe, gap, 0, db.TelemetryPriceCallNoData, &took))
						continue
					}
					if log != nil {
						log.WarnContext(ctx, "price fetch: plugin error",
							"plugin", pe.id, "listing", ig.ListingID, "err", err)
					}
					// The provider running out of time and the provider answering
					// badly need different fixes, and only the deadline can say
					// which this was. errors.Is rather than a comparison, since a
					// plugin is free to wrap what its transport returned.
					outcome := db.TelemetryPriceCallError
					if errors.Is(err, context.DeadlineExceeded) {
						outcome = db.TelemetryPriceCallTimeout
					}
					gapsTel.call(ctx, i, priceCall(pe, gap, 0, outcome, &took))
					allOK = false
					break
				}

				prices := barsToEODPrices(ig.ListingID, pe.id, result.Bars, result.ShareCountBasis, now)
				if err := database.UpsertPricesForRange(ctx, ig.ListingID, pe.id, prices, gap.From, gap.Before, nil); err != nil {
					if log != nil {
						log.ErrorContext(ctx, "price fetch: upsert", "listing", ig.ListingID, "err", err)
					}
					// Ours rather than theirs. Recorded apart from the transport
					// outcomes so a panel watching provider health is not moved by
					// our own write failing.
					gapsTel.call(ctx, i, priceCall(pe, gap, 0, db.TelemetryPriceCallUpsertFailed, &took))
					allOK = false
					break
				}
				bars += len(result.Bars)
				gapsTel.call(ctx, i, priceCall(pe, gap, len(result.Bars), db.TelemetryPriceCallBarsReturned, &took))
			}
			if allOK {
				fetchedByPlugin = true
				break
			}
			// On error, try next plugin for this instrument.
		}
		gapsTel.end(ctx, i, gapOutcome(fetchedByPlugin, bars, reached, gapsTel.called(i)))
	}
	return nil
}

// priceCall assembles the telemetry row for one range put to one plugin. The gap
// and run ids are the ledger's to fill in, since the fetch loop does not hold them.
func priceCall(pe pluginEntry, r db.DateRange, bars int, outcome string, took *time.Duration) db.TelemetryPricePluginCall {
	return db.TelemetryPricePluginCall{
		PluginID:   pe.id,
		Precedence: pe.precedence,
		From:       r.From,
		Before:     r.Before,
		Bars:       bars,
		Outcome:    outcome,
		Duration:   took,
	}
}

// coverRange records that a plugin has settled a range without supplying bars.
// A failure to record is logged rather than propagated: the fetch itself
// succeeded, and the only cost is asking again next cycle.
func coverRange(ctx context.Context, database db.DB, listingID, pluginID string, r db.DateRange, reason string, log *slog.Logger) {
	if !r.Before.After(r.From) {
		return
	}
	if err := database.UpsertPricesForRange(ctx, listingID, pluginID, nil, r.From, r.Before, nil); err != nil && log != nil {
		log.WarnContext(ctx, "price fetch: record empty coverage",
			"plugin", pluginID, "listing", listingID, "reason", reason, "err", err)
	}
}

// extractListingIDs returns unique listing IDs from gaps.
func extractListingIDs(gaps []db.ListingDateRanges) []string {
	out := make([]string, len(gaps))
	for i, g := range gaps {
		out[i] = g.ListingID
	}
	return out
}

// barsToEODPrices converts plugin bars to price rows, resolving the plugin's
// declared denomination to a per-row share count basis. An as-traded bar is
// denominated in the share count current on its own date; a back-adjusted
// series is denominated in the share count current when we fetched it.
func barsToEODPrices(listingID, provider string, bars []DailyBar, basis ShareCountBasis, fetchedAt time.Time) []db.EODPrice {
	out := make([]db.EODPrice, len(bars))
	for i, b := range bars {
		scb := b.Date
		if basis == AsOfFetch {
			scb = fetchedAt
		}
		out[i] = db.EODPrice{
			ListingID:       listingID,
			PriceDate:       b.Date,
			Open:            b.Open,
			High:            b.High,
			Low:             b.Low,
			Close:           b.Close,
			Volume:          b.Volume,
			AdjustedClose:   b.AdjustedClose,
			DataProvider:    provider,
			ShareCountBasis: &scb,
		}
	}
	return out
}
