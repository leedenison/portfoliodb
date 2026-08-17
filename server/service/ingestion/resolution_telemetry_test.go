package ingestion

import (
	"context"
	"errors"
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	descpkg "github.com/leedenison/portfoliodb/server/identifier/description"
	"go.uber.org/mock/gomock"
)

// keySpy records the key rows a ledger writes and the outcomes it stamps them
// with, keyed by description so a test names the row it means.
type keySpy struct {
	started map[string]db.TelemetryResolutionKey
	ended   map[string]db.TelemetryResolutionKeyOutcome
	desc    map[string]string // row id -> description
}

func newKeySpy(t *testing.T, tel *mock.MockTelemetryDB) *keySpy {
	t.Helper()
	s := &keySpy{
		started: map[string]db.TelemetryResolutionKey{},
		ended:   map[string]db.TelemetryResolutionKeyOutcome{},
		desc:    map[string]string{},
	}
	n := 0
	tel.EXPECT().StartResolutionKey(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, k db.TelemetryResolutionKey) string {
			n++
			id := string(rune('a' + n))
			s.started[k.Description] = k
			s.desc[id] = k.Description
			return id
		}).AnyTimes()
	tel.EXPECT().EndResolutionKey(gomock.Any(), gomock.Any(), gomock.Any()).
		Do(func(_ context.Context, id string, o db.TelemetryResolutionKeyOutcome) {
			s.ended[s.desc[id]] = o
		}).AnyTimes()
	return s
}

// tx is the least a posting needs to name a resolution key.
func tx(desc string) *apiv1.Tx {
	return &apiv1.Tx{
		InstrumentDescription: desc,
		AssetClassHint:        typev1.AssetClass_STOCK,
	}
}

// TestResolutionKeyCountsItsFanOut pins the grain. A resolution key is not a
// transaction: many share one and resolve once, so tx_count is what tells a
// failure affecting three postings from one affecting one.
func TestResolutionKeyCountsItsFanOut(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	tel := mock.NewMockTelemetryDB(ctrl)
	spy := newKeySpy(t, tel)

	txs := []*apiv1.Tx{tx("APPLE INC"), tx("APPLE INC"), tx("APPLE INC"), tx("MSFT")}
	hints := make([][]identifier.Identifier, len(txs))

	newResolutionKeys(context.Background(), tel, "run-1", "FIDELITY_CSV", txs, hints, nil)

	if len(spy.started) != 2 {
		t.Fatalf("wrote %d keys, want 2", len(spy.started))
	}
	if got := spy.started["APPLE INC"].TxCount; got != 3 {
		t.Errorf("tx_count for the shared description = %d, want 3", got)
	}
	if got := spy.started["MSFT"].TxCount; got != 1 {
		t.Errorf("tx_count for the lone description = %d, want 1", got)
	}
	if k := spy.started["APPLE INC"]; k.Source != "FIDELITY_CSV" || k.HadIdentifierHints {
		t.Errorf("key = %+v, want source FIDELITY_CSV and no identifier hints", k)
	}
}

// TestResolutionKeyHintsSplitTheKey pins the other half of the grain: two
// postings sharing a description but carrying different identifier hints resolve
// independently, so they are two keys rather than one.
func TestResolutionKeyHintsSplitTheKey(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	tel := mock.NewMockTelemetryDB(ctrl)
	var started []db.TelemetryResolutionKey
	tel.EXPECT().StartResolutionKey(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, k db.TelemetryResolutionKey) string {
			started = append(started, k)
			return "key"
		}).AnyTimes()

	txs := []*apiv1.Tx{tx("APPLE INC"), tx("APPLE INC")}
	hints := [][]identifier.Identifier{
		nil,
		{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}},
	}

	newResolutionKeys(context.Background(), tel, "run-1", "FIDELITY_CSV", txs, hints, nil)

	if len(started) != 2 {
		t.Fatalf("wrote %d keys, want 2", len(started))
	}
	// Extraction exists to find an identifier in a description; the key that
	// already names one skips it.
	for _, k := range started {
		if k.HadIdentifierHints && k.TxCount != 1 {
			t.Errorf("hinted key tx_count = %d, want 1", k.TxCount)
		}
	}
}

// TestResolutionKeyExtractionOutcome pins stage one landing on the key. A key
// carrying identifier hints records not-attempted whatever the pre-pass did with
// the description it shares, because extraction is not attempted for that key.
func TestResolutionKeyExtractionOutcome(t *testing.T) {
	tests := []struct {
		name       string
		hints      []identifier.Identifier
		extraction map[string]string
		want       string
	}{
		{
			name:       "extracted",
			extraction: map[string]string{cacheKey("SRC", "APPLE INC"): db.TelemetryExtractionHintsFound},
			want:       db.TelemetryExtractionHintsFound,
		},
		{
			name:       "db hit",
			extraction: map[string]string{cacheKey("SRC", "APPLE INC"): db.TelemetryExtractionNotAttemptedDBHit},
			want:       db.TelemetryExtractionNotAttemptedDBHit,
		},
		{
			name:       "hints supplied",
			hints:      []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}},
			extraction: map[string]string{cacheKey("SRC", "APPLE INC"): db.TelemetryExtractionHintsFound},
			want:       db.TelemetryExtractionNotAttemptedHintsSupplied,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)
			tel := mock.NewMockTelemetryDB(ctrl)
			spy := newKeySpy(t, tel)

			txs := []*apiv1.Tx{tx("APPLE INC")}
			keys := newResolutionKeys(context.Background(), tel, "run-1", "SRC", txs,
				[][]identifier.Identifier{tc.hints}, tc.extraction)
			keys.end(context.Background(),
				cacheKeyWithHints("SRC", "APPLE INC", tc.hints),
				db.TelemetryResolutionIdentified, "inst-1")

			got := spy.ended["APPLE INC"]
			if got.ExtractionOutcome != tc.want {
				t.Errorf("extraction outcome = %q, want %q", got.ExtractionOutcome, tc.want)
			}
			if got.Outcome != db.TelemetryResolutionIdentified {
				t.Errorf("outcome = %q, want %q", got.Outcome, db.TelemetryResolutionIdentified)
			}
			if got.InstrumentID != "inst-1" {
				t.Errorf("instrument id = %q, want inst-1", got.InstrumentID)
			}
		})
	}
}

// TestResolutionKeyStampsOnce pins the first stamp standing. A key resolves once
// and every later transaction sharing it is answered from the batch cache, so a
// second stamp would be the key being read rather than resolved again.
func TestResolutionKeyStampsOnce(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	tel := mock.NewMockTelemetryDB(ctrl)
	tel.EXPECT().StartResolutionKey(gomock.Any(), gomock.Any()).Return("key-1")
	tel.EXPECT().EndResolutionKey(gomock.Any(), "key-1", gomock.Any()).
		Do(func(_ context.Context, _ string, o db.TelemetryResolutionKeyOutcome) {
			if o.Outcome != db.TelemetryResolutionIdentified {
				t.Errorf("outcome = %q, want the first stamp to stand", o.Outcome)
			}
		}).Times(1)

	ctx := context.Background()
	txs := []*apiv1.Tx{tx("APPLE INC")}
	keys := newResolutionKeys(ctx, tel, "run-1", "SRC", txs, make([][]identifier.Identifier, 1), nil)
	key := cacheKeyWithHints("SRC", "APPLE INC", nil)
	keys.end(ctx, key, db.TelemetryResolutionIdentified, "inst-1")
	keys.end(ctx, key, db.TelemetryResolutionDBSourceDescription, "inst-1")
}

// TestNilLedgerRecordsNothing pins the nil ledger being usable, which is what
// keeps a build with no telemetry writer free of checks at every call site.
func TestNilLedgerRecordsNothing(t *testing.T) {
	var keys *resolutionKeys
	keys.mismatch("k")
	keys.end(context.Background(), "k", db.TelemetryResolutionIdentified, "inst-1")

	if got := newResolutionKeys(context.Background(), nil, "run-1", "SRC", nil, nil, nil); got != nil {
		t.Error("a ledger with no writer is not nil")
	}
	if got := newResolutionKeys(context.Background(), mock.NewMockTelemetryDB(gomock.NewController(t)), "", "SRC", nil, nil, nil); got != nil {
		t.Error("a ledger outside a run is not nil")
	}
}

// TestIdentifierResolutionKeys pins the synthetic key the price and corporate
// event parts get. They resolve from an identifier and no description, but an
// identification attempt reaches its run through a key, so the identifier names
// one and tx_count carries the groups sharing it.
func TestIdentifierResolutionKeys(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	tel := mock.NewMockTelemetryDB(ctrl)
	spy := newKeySpy(t, tel)

	refs := []identifierRef{
		{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"},
		{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"},
		{Type: "CUSIP", Value: "594918104"},
	}
	newIdentifierResolutionKeys(context.Background(), tel, "run-1", refs)

	if len(spy.started) != 2 {
		t.Fatalf("wrote %d keys, want 2", len(spy.started))
	}
	k, ok := spy.started["MIC_TICKER:XNAS:AAPL"]
	if !ok {
		t.Fatalf("no key named by the identifier; wrote %v", spy.started)
	}
	if k.TxCount != 2 {
		t.Errorf("tx_count = %d, want 2 groups sharing the identifier", k.TxCount)
	}
	if !k.HadIdentifierHints || k.Source != "" {
		t.Errorf("key = %+v, want identifier hints and no source", k)
	}
}

// TestDescriptionPluginCallRows pins one row per ExtractBatch invocation, tokens
// included. Tokens are columns rather than running totals, which is what makes
// the cost of one import answerable, and they stay null for a plugin that costs
// nothing to call.
func TestDescriptionPluginCallRows(t *testing.T) {
	tests := []struct {
		name       string
		plugin     *fakeDescPlugin
		wantOut    string
		wantHints  int
		wantTokens *db.TelemetryTokens
	}{
		{
			name: "hints and tokens",
			plugin: &fakeDescPlugin{
				results: map[string][]identifier.Identifier{"item-1": {{Type: "MIC_TICKER", Value: "AAPL"}}},
				tokens:  &descpkg.Usage{PromptTokens: 11, CompletionTokens: 3, TotalTokens: 14},
			},
			wantOut:    string(descpkg.OutcomeHintsReturned),
			wantHints:  1,
			wantTokens: &db.TelemetryTokens{Prompt: 11, Completion: 3, Total: 14},
		},
		{
			name:      "no hints and no token cost",
			plugin:    &fakeDescPlugin{results: map[string][]identifier.Identifier{}},
			wantOut:   string(descpkg.OutcomeNoHints),
			wantHints: 0,
		},
		{
			name:      "the call failed",
			plugin:    &fakeDescPlugin{err: errors.New("upstream down")},
			wantOut:   string(descpkg.OutcomeError),
			wantHints: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)
			database := mock.NewMockDB(ctrl)
			tel := mock.NewMockTelemetryDB(ctrl)
			descRegistry := descpkg.NewRegistry()
			descRegistry.Register("fake", tc.plugin)
			database.EXPECT().
				ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryDescription).
				Return([]db.PluginConfigRow{{PluginID: "fake", Precedence: 70}}, nil)

			var got db.TelemetryDescriptionPluginCall
			tel.EXPECT().WriteDescriptionPluginCall(gomock.Any(), gomock.Any()).
				Do(func(_ context.Context, c db.TelemetryDescriptionPluginCall) { got = c }).Times(1)

			items := []descpkg.BatchItem{{ID: "item-1", InstrumentDescription: "APPLE INC"}}
			deps := ingestDeps{DB: database, DescRegistry: descRegistry, Telemetry: tel, RunID: "run-1"}
			if _, _, err := runDescriptionPluginsBatch(context.Background(), deps, "FIDELITY", "SRC", items); err != nil {
				t.Fatalf("runDescriptionPluginsBatch: %v", err)
			}

			if got.RunID != "run-1" || got.PluginID != "fake" {
				t.Errorf("row = %+v, want it against run-1 and plugin fake", got)
			}
			if got.BatchSize != 1 {
				t.Errorf("batch_size = %d, want 1", got.BatchSize)
			}
			// The plugin's configured precedence, which is what puts the chain
			// back in order: batch_size cannot, since two plugins handed equal
			// batches would sort arbitrarily.
			if got.Precedence != 70 {
				t.Errorf("precedence = %d, want 70", got.Precedence)
			}
			if got.Outcome != tc.wantOut {
				t.Errorf("outcome = %q, want %q", got.Outcome, tc.wantOut)
			}
			if got.ItemsWithHints != tc.wantHints {
				t.Errorf("items_with_hints = %d, want %d", got.ItemsWithHints, tc.wantHints)
			}
			switch {
			case tc.wantTokens == nil && got.Tokens != nil:
				t.Errorf("tokens = %+v, want null for a plugin with no token cost", got.Tokens)
			case tc.wantTokens != nil && (got.Tokens == nil || *got.Tokens != *tc.wantTokens):
				t.Errorf("tokens = %+v, want %+v", got.Tokens, tc.wantTokens)
			}
		})
	}
}

// TestExtractionOutcomesPerItem pins where the skips live. An item no enabled
// plugin accepted was never put to one, which is not the same as one that was
// asked about and yielded nothing.
func TestExtractionOutcomesPerItem(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	database := mock.NewMockDB(ctrl)
	descRegistry := descpkg.NewRegistry()
	descRegistry.Register("stock", &fakeDescPlugin{
		acceptable: map[string]bool{identifier.SecurityTypeHintStock: true},
		results:    map[string][]identifier.Identifier{"found": {{Type: "MIC_TICKER", Value: "AAPL"}}},
	})
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryDescription).
		Return([]db.PluginConfigRow{{PluginID: "stock", Precedence: 1}}, nil)

	stock := identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}
	items := []descpkg.BatchItem{
		{ID: "found", InstrumentDescription: "APPLE INC", Hints: stock},
		{ID: "missed", InstrumentDescription: "SOMETHING", Hints: stock},
		{ID: "filtered", InstrumentDescription: "USD", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintCash}},
	}
	_, outcomes, err := runDescriptionPluginsBatch(context.Background(),
		ingestDeps{DB: database, DescRegistry: descRegistry}, "FIDELITY", "SRC", items)
	if err != nil {
		t.Fatalf("runDescriptionPluginsBatch: %v", err)
	}

	want := map[string]string{
		"found":    db.TelemetryExtractionHintsFound,
		"missed":   db.TelemetryExtractionNoHints,
		"filtered": db.TelemetryExtractionNotAttemptedTypeFilter,
	}
	for id, w := range want {
		if outcomes[id] != w {
			t.Errorf("outcome for %q = %q, want %q", id, outcomes[id], w)
		}
	}
}

// TestExtractionNotAttemptedWithoutPlugins pins the installation with no
// description plugins. Nothing was asked, so nothing found nothing.
func TestExtractionNotAttemptedWithoutPlugins(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	database := mock.NewMockDB(ctrl)
	descRegistry := descpkg.NewRegistry()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryDescription).
		Return(nil, nil)

	items := []descpkg.BatchItem{{ID: "item-1", InstrumentDescription: "APPLE INC"}}
	_, outcomes, err := runDescriptionPluginsBatch(context.Background(),
		ingestDeps{DB: database, DescRegistry: descRegistry}, "FIDELITY", "SRC", items)
	if err != nil {
		t.Fatalf("runDescriptionPluginsBatch: %v", err)
	}
	if outcomes["item-1"] != db.TelemetryExtractionNotAttemptedNoPlugins {
		t.Errorf("outcome = %q, want %q", outcomes["item-1"], db.TelemetryExtractionNotAttemptedNoPlugins)
	}
}

// TestMismatchCheckProbesAreTheirOwnAttempts pins the two calls the issue names.
// When extraction returns both a MIC_TICKER and an OPENFIGI_SHARE_CLASS the
// resolver resolves each separately to see whether they agree; both are real
// resolutions against real plugins, and until now both were made with a nil
// counter and were invisible. Naming them mismatch_check is also what stops them
// inflating the denominator of a failure rate over primary attempts.
func TestMismatchCheckProbesAreTheirOwnAttempts(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	tel := mock.NewMockTelemetryDB(ctrl)
	spy := newKeySpy(t, tel)

	var attempts []db.TelemetryIdentificationAttempt
	tel.EXPECT().WriteIdentificationAttempt(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, a db.TelemetryIdentificationAttempt) string {
			attempts = append(attempts, a)
			return "attempt"
		}).AnyTimes()

	const source, desc = "IBKR:test:statement", "APPLE INC COM"
	// Both hints already resolve to the same instrument, so every call short
	// circuits in the database and no plugin is involved.
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("inst-1", "STOCK", "XNAS", "USD", nil).AnyTimes()
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "OPENFIGI_SHARE_CLASS", "", "BBG000B9XRY4").
		Return("inst-1", "STOCK", "XNAS", "USD", nil).AnyTimes()

	ctx := context.Background()
	txs := []*apiv1.Tx{tx(desc)}
	keys := newResolutionKeys(ctx, tel, "run-1", source, txs, make([][]identifier.Identifier, 1), nil)
	extracted := map[string][]identifier.Identifier{
		cacheKey(source, desc): {
			{Type: "MIC_TICKER", Value: "AAPL"},
			{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG000B9XRY4"},
		},
	}

	if _, err := Resolve(ctx, database, identifier.NewRegistry(), "IBKR", source, desc,
		identifier.Hints{}, nil, nil, 0, extracted, nil, keys); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if len(attempts) != 3 {
		t.Fatalf("wrote %d attempts, want 3 -- two probes and the resolution", len(attempts))
	}
	byPurpose := map[string]int{}
	for _, a := range attempts {
		byPurpose[a.Purpose]++
	}
	if byPurpose[db.TelemetryPurposeMismatchCheck] != 2 {
		t.Errorf("mismatch_check attempts = %d, want 2", byPurpose[db.TelemetryPurposeMismatchCheck])
	}
	if byPurpose[db.TelemetryPurposePrimary] != 1 {
		t.Errorf("primary attempts = %d, want 1", byPurpose[db.TelemetryPurposePrimary])
	}
	// The probes must not stamp the key: the resolution that decides it does.
	if got := spy.ended[desc]; got.Outcome != db.TelemetryResolutionIdentified {
		t.Errorf("key outcome = %q, want %q from the primary resolution", got.Outcome, db.TelemetryResolutionIdentified)
	}
	if spy.ended[desc].MismatchDetected {
		t.Error("mismatch_detected set for two hints that agreed")
	}
}
