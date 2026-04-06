package ingestion

import (
	"context"
	"fmt"
	"log"
	"log/slog"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	ingestionv1 "github.com/leedenison/portfoliodb/proto/ingestion/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/identifier/description"
	"github.com/leedenison/portfoliodb/server/pricefetcher"
	"github.com/leedenison/portfoliodb/server/telemetry"
	"github.com/leedenison/portfoliodb/server/worker"
	"google.golang.org/protobuf/proto"
)

// ingestionLog is the logger for resolution and plugin orchestration (category server/service/ingestion).
// Set by RunWorker; when nil, resolve.go falls back to slog.Default().
var ingestionLog *slog.Logger

// RunWorker processes job requests from the channel until it is closed.
// Resolution uses DB, then in-batch cache, then description plugins (extract hints) and identifier plugins (timeout from config, retry once with backoff).
// ingestionLogger is the logger for ingestion/resolution (typically with category server/service/ingestion); may be nil.
// priceTrigger is optional; when non-nil, a non-blocking signal is sent after each successful job to trigger price fetching.
func RunWorker(ctx context.Context, database db.DB, queue <-chan *JobRequest, registry *identifier.Registry, descRegistry *description.Registry, counter telemetry.CounterIncrementer, ingestionLogger *slog.Logger, priceTrigger chan<- struct{}, workers *worker.Registry) {
	ingestionLog = ingestionLogger
	const name = "ingestion"
	if workers != nil {
		workers.SetIdle(name)
	}
	for {
		if workers != nil {
			workers.SetQueueDepth(name, len(queue))
		}
		select {
		case <-ctx.Done():
			return
		case j, ok := <-queue:
			if !ok {
				return
			}
			if workers != nil {
				workers.SetRunning(name, fmt.Sprintf("Processing job %s", j.JobID))
				workers.SetQueueDepth(name, len(queue))
			}
			processJob(ctx, database, registry, descRegistry, counter, j, priceTrigger)
			if workers != nil {
				workers.SetIdle(name)
			}
		}
	}
}

func processJob(ctx context.Context, database db.DB, registry *identifier.Registry, descRegistry *description.Registry, counter telemetry.CounterIncrementer, j *JobRequest, priceTrigger chan<- struct{}) {
	_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_RUNNING)

	switch j.JobType {
	case "tx":
		if ok, userID := processTx(ctx, database, registry, descRegistry, counter, j); ok {
			if err := recalcAfterIngestion(ctx, database, userID); err != nil {
				log.Printf("ingestion job %s: recalc INITIALIZE txs: %v", j.JobID, err)
			}
			pricefetcher.Trigger(priceTrigger)
		}
	case "price":
		processPriceImport(ctx, database, registry, j)
		pricefetcher.Trigger(priceTrigger)
	default:
		log.Printf("ingestion job %s: unknown job type %q", j.JobID, j.JobType)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
	}
}

// loadAndClearPayload loads the serialized payload from the DB and clears it.
func loadAndClearPayload(ctx context.Context, database db.DB, jobID string) ([]byte, error) {
	payload, err := database.LoadJobPayload(ctx, jobID)
	if err != nil {
		return nil, err
	}
	_ = database.ClearJobPayload(ctx, jobID)
	return payload, nil
}

func processTx(ctx context.Context, database db.DB, registry *identifier.Registry, descRegistry *description.Registry, counter telemetry.CounterIncrementer, j *JobRequest) (bool, string) {
	// Look up userID from the job row.
	_, _, _, userID, _, _, _ := database.GetJob(ctx, j.JobID)

	payload, err := loadAndClearPayload(ctx, database, j.JobID)
	if err != nil {
		log.Printf("ingestion job %s: load payload: %v", j.JobID, err)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}
	var req ingestionv1.UpsertTxsRequest
	if err := proto.Unmarshal(payload, &req); err != nil {
		log.Printf("ingestion job %s: unmarshal payload: %v", j.JobID, err)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}

	txs := req.GetTxs()
	if txs == nil {
		txs = []*apiv1.Tx{}
	}
	source := req.GetSource()
	broker, _ := brokerToStr(req.Broker)
	bulk := req.PeriodFrom != nil && req.PeriodTo != nil

	// Validate.
	errs := ValidateTxs(txs)
	if len(errs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, errs)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}
	// Load ignored asset classes for this user.
	ignoredClasses, err := database.ListIgnoredAssetClasses(ctx, userID)
	if err != nil {
		log.Printf("ingestion job %s: load ignored asset classes: %v", j.JobID, err)
		ignoredClasses = nil // non-fatal: proceed without filtering
	}
	// Filter non-stored tx types (e.g. SPLIT) and ignored asset classes.
	txsToProcess, originalIndices := filterStoredTxs(txs, broker, ignoredClasses)
	if len(txsToProcess) == 0 {
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_SUCCESS)
		return true, userID
	}
	_ = database.SetJobTotalCount(ctx, j.JobID, int32(len(txsToProcess)))
	// Extract description hints.
	cache, extractedHintsCache, descInstruments, descValErrs, err := extractDescHints(ctx, database, descRegistry, counter, source, broker, txsToProcess)
	if err != nil {
		log.Printf("ingestion job %s: extract description hints: %v", j.JobID, err)
		_ = database.AppendValidationErrors(ctx, j.JobID, []*apiv1.ValidationError{
			{RowIndex: -1, Field: "txs", Message: err.Error()},
		})
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}
	if len(descValErrs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, descValErrs)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}
	// Resolve instruments.
	instrumentIDs, idErrs, resolveValErrs, err := resolveInstruments(ctx, database, registry, broker, source, j.JobID, counter, txsToProcess, originalIndices, cache, extractedHintsCache, descInstruments)
	if err != nil {
		log.Printf("ingestion job %s: resolve instrument: %v", j.JobID, err)
		_ = database.AppendValidationErrors(ctx, j.JobID, []*apiv1.ValidationError{
			{RowIndex: -1, Field: "instrument_description", Message: err.Error()},
		})
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}
	if len(resolveValErrs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, resolveValErrs)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}
	if len(idErrs) > 0 {
		_ = database.AppendIdentificationErrors(ctx, j.JobID, idErrs)
	}
	// Store transactions.
	var storeErr error
	if bulk {
		storeErr = database.ReplaceTxsInPeriod(ctx, userID, broker, req.PeriodFrom, req.PeriodTo, txsToProcess, instrumentIDs)
	} else {
		storeErr = database.CreateTx(ctx, userID, broker, txsToProcess[0].GetAccount(), txsToProcess[0], instrumentIDs[0])
	}
	if storeErr != nil {
		log.Printf("ingestion job %s: %v", j.JobID, storeErr)
		_ = database.AppendValidationErrors(ctx, j.JobID, []*apiv1.ValidationError{
			{RowIndex: -1, Field: "txs", Message: storeErr.Error()},
		})
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}

	_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_SUCCESS)
	return true, userID
}

// filterStoredTxs returns only txs with stored types that are not ignored, along with their original indices.
func filterStoredTxs(txs []*apiv1.Tx, broker string, ignored []db.IgnoredAssetClass) ([]*apiv1.Tx, []int) {
	var filtered []*apiv1.Tx
	var indices []int
	for i, tx := range txs {
		if !TxTypeStored(tx.Type) {
			continue
		}
		if TxIgnored(tx, broker, ignored) {
			continue
		}
		filtered = append(filtered, tx)
		indices = append(indices, i)
	}
	return filtered, indices
}

// extractDescHints looks up each distinct (source, description) in DB and runs
// batch description extraction for misses. Returns a resolve cache pre-populated
// with DB hits, an extracted hints cache keyed by cacheKey(source, desc), a map
// of instrument rows found via broker description lookup (for later identifier
// contradiction checks), and any validation errors from contradictions.
//
// Contradictions detected:
//   - Within-batch: same (source, desc) with both CASH and SECURITY InstrumentKinds.
//   - DB: broker description resolves to an instrument whose asset class contradicts the tx's InstrumentKind.
func extractDescHints(ctx context.Context, database db.DB, descRegistry *description.Registry, counter telemetry.CounterIncrementer, source, broker string, txs []*apiv1.Tx) (map[string]resolveResult, map[string][]identifier.Identifier, map[string]*db.InstrumentRow, []*apiv1.ValidationError, error) {
	cache := make(map[string]resolveResult)
	descInstruments := make(map[string]*db.InstrumentRow)
	var extractedHintsCache map[string][]identifier.Identifier
	seen := make(map[string]bool)
	kindByDesc := make(map[string]string) // first InstrumentKind seen per (source, desc)
	var valErrs []*apiv1.ValidationError
	var batchItems []description.BatchItem
	idByKey := make(map[string]string)
	for i, tx := range txs {
		desc := tx.GetInstrumentDescription()
		key := cacheKey(source, desc)
		kind := db.TxTypeToInstrumentKind(tx.GetType())

		// Within-batch kind contradiction check.
		if prev, ok := kindByDesc[key]; ok {
			if prev != kind {
				valErrs = append(valErrs, &apiv1.ValidationError{
					RowIndex: int32(i),
					Field:    "instrument_description",
					Message:  fmt.Sprintf("instrument_description %q appears with conflicting transaction types (both %s and %s); use different descriptions for the cash and security legs", desc, prev, kind),
				})
				continue
			}
		} else {
			kindByDesc[key] = kind
		}

		if seen[key] {
			continue
		}
		seen[key] = true
		id, err := database.FindInstrumentBySourceDescription(ctx, source, desc)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if id != "" {
			// DB kind contradiction check.
			inst, err := database.GetInstrument(ctx, id)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			if inst != nil {
				descInstruments[key] = inst
				var instAC string
				if inst.AssetClass != nil {
					instAC = *inst.AssetClass
				}
				instKind := db.AssetClassToInstrumentKind(instAC)
				if instKind != "" && instKind != kind {
					valErrs = append(valErrs, &apiv1.ValidationError{
						RowIndex: int32(i),
						Field:    "instrument_description",
						Message:  fmt.Sprintf("instrument_description %q was previously identified as a %s instrument but is used here as a %s transaction", desc, instKind, kind),
					})
					continue
				}
			}
			cache[key] = resolveResult{InstrumentID: id}
		} else {
			batchID := shortHashForBatch(key)
			idByKey[key] = batchID
			batchItems = append(batchItems, description.BatchItem{
				ID:                    batchID,
				InstrumentDescription: desc,
				Hints:                 HintsFromTx(tx),
			})
		}
	}
	if len(valErrs) > 0 {
		return nil, nil, nil, valErrs, nil
	}
	if len(batchItems) > 0 {
		hintsByID, err := runDescriptionPluginsBatch(ctx, database, descRegistry, counter, broker, source, batchItems)
		if err == nil && hintsByID != nil {
			extractedHintsCache = make(map[string][]identifier.Identifier)
			for key, id := range idByKey {
				extractedHintsCache[key] = hintsByID[id]
			}
		}
	}
	return cache, extractedHintsCache, descInstruments, nil, nil
}

// resolveInstruments resolves each tx to an instrument ID using the pre-populated
// cache and extracted hints. Returns the instrument IDs (parallel to txs) and any
// identification errors collected from the cache. descInstruments maps cacheKey(source, desc)
// to instruments found via broker description lookup; used to detect identifier contradictions.
func resolveInstruments(ctx context.Context, database db.DB, registry *identifier.Registry, broker, source, jobID string, counter telemetry.CounterIncrementer, txs []*apiv1.Tx, originalIndices []int, cache map[string]resolveResult, extractedHintsCache map[string][]identifier.Identifier, descInstruments map[string]*db.InstrumentRow) ([]string, []db.IdentificationError, []*apiv1.ValidationError, error) {
	instrumentIDs := make([]string, len(txs))
	var valErrs []*apiv1.ValidationError
	for i, tx := range txs {
		desc := tx.GetInstrumentDescription()
		rowIndex := int32(originalIndices[i])
		txHints := identifierHintsFromTx(ctx, tx)

		// Identifier contradiction check: if the description already maps to a
		// known instrument, verify that the tx's identifier hints do not conflict
		// with identifiers stored on that instrument.
		if len(txHints) > 0 && descInstruments != nil {
			key := cacheKey(source, desc)
			if inst, ok := descInstruments[key]; ok {
				if valErr := checkIdentifierContradiction(inst, txHints, desc, rowIndex); valErr != nil {
					valErrs = append(valErrs, valErr)
					continue
				}
			}
		}

		r, err := Resolve(ctx, database, registry, broker, source, desc, HintsFromTx(tx), txHints, cache, rowIndex, counter, extractedHintsCache)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("row %d: %w", rowIndex, err)
		}
		instrumentIDs[i] = r.InstrumentID
		_ = database.IncrJobProcessedCount(ctx, jobID)
	}
	if len(valErrs) > 0 {
		return nil, nil, valErrs, nil
	}
	var idErrs []db.IdentificationError
	for _, r := range cache {
		if r.IdErr != nil {
			idErrs = append(idErrs, *r.IdErr)
		}
	}
	return instrumentIDs, idErrs, nil, nil
}

// checkIdentifierContradiction checks whether any of the tx's identifier hints
// contradict identifiers stored on the instrument previously resolved for this
// description. Returns a validation error if a contradiction is found.
func checkIdentifierContradiction(inst *db.InstrumentRow, txHints []identifier.Identifier, desc string, rowIndex int32) *apiv1.ValidationError {
	for _, hint := range txHints {
		if hint.Type == "BROKER_DESCRIPTION" || hint.Type == "" || hint.Value == "" {
			continue
		}
		// Check if the instrument has identifiers of the same type.
		for _, stored := range inst.Identifiers {
			if stored.Type != hint.Type {
				continue
			}
			// Same type exists — values must match.
			if stored.Value != hint.Value {
				return &apiv1.ValidationError{
					RowIndex: rowIndex,
					Field:    "identifier_hints",
					Message:  fmt.Sprintf("identifier hint %s:%s contradicts the instrument previously identified for description %q (has %s:%s)", hint.Type, hint.Value, desc, stored.Type, stored.Value),
				}
			}
		}
	}
	return nil
}

// brokerToStr converts a proto Broker enum to its string representation.
func brokerToStr(b apiv1.Broker) (string, error) {
	switch b {
	case apiv1.Broker_IBKR:
		return "IBKR", nil
	case apiv1.Broker_SCHB:
		return "SCHB", nil
	case apiv1.Broker_FIDELITY:
		return "Fidelity", nil
	default:
		return "", fmt.Errorf("unknown broker")
	}
}
