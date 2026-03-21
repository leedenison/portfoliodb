package ingestion

import (
	"context"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/service/api"
)

// recalcAfterIngestion recalculates all INITIALIZE txs for a user after
// transaction ingestion. This is a post-ingestion activity that ensures
// synthetic opening-balance transactions remain consistent with the current
// transaction history.
//
// Recalculation is triggered unconditionally after successful ingestion because:
//   - Bulk replace (ReplaceTxsInPeriod) may change the portfolio start date
//     and/or alter balances within declaration date ranges.
//   - Single tx addition (CreateTx) may introduce the earliest transaction
//     (changing the start date) or add a transaction within a declaration range.
//
// RecalcAllInitializeTxs is a no-op when no declarations exist.
func recalcAfterIngestion(ctx context.Context, database db.DB, userID string) error {
	return api.RecalcAllInitializeTxs(ctx, database, userID)
}
