package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/leedenison/portfoliodb/server/db"
)

// ListIgnoredAssetClasses implements db.IgnoredAssetClassDB.
func (p *Postgres) ListIgnoredAssetClasses(ctx context.Context, userID string) ([]db.IgnoredAssetClass, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	rows, err := p.q.QueryxContext(ctx, `
		SELECT broker, account, asset_class
		FROM ignored_asset_classes
		WHERE user_id = $1
		ORDER BY broker, account, asset_class
	`, userUUID)
	if err != nil {
		return nil, fmt.Errorf("list ignored asset classes: %w", err)
	}
	defer rows.Close()
	var result []db.IgnoredAssetClass
	for rows.Next() {
		var r db.IgnoredAssetClass
		if err := rows.Scan(&r.Broker, &r.Account, &r.AssetClass); err != nil {
			return nil, fmt.Errorf("scan ignored asset class: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// SetIgnoredAssetClasses implements db.IgnoredAssetClassDB.
func (p *Postgres) SetIgnoredAssetClasses(ctx context.Context, userID string, rules []db.IgnoredAssetClass, assetClassToTxTypes map[string][]string) error {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("invalid user id: %w", err)
	}
	return p.runInTx(ctx, func(tx queryable) error {
		// 1. Delete existing rules.
		if _, err := tx.ExecContext(ctx, `DELETE FROM ignored_asset_classes WHERE user_id = $1`, userUUID); err != nil {
			return fmt.Errorf("delete old ignore rules: %w", err)
		}

		// 2. Insert new rules.
		for _, r := range rules {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO ignored_asset_classes (user_id, broker, account, asset_class)
				VALUES ($1, $2, $3, $4)
			`, userUUID, r.Broker, r.Account, r.AssetClass); err != nil {
				return fmt.Errorf("insert ignore rule: %w", err)
			}
		}

		// 3. Delete matching regular txs and synthetic INITIALIZE txs.
		if err := deleteIgnoredTxs(ctx, tx, userUUID, rules, assetClassToTxTypes); err != nil {
			return err
		}

		// 4. Delete matching holding declarations.
		if err := deleteIgnoredDeclarations(ctx, tx, userUUID, rules); err != nil {
			return err
		}
		return nil
	})
}

// CountIgnoredTxs implements db.IgnoredAssetClassDB.
func (p *Postgres) CountIgnoredTxs(ctx context.Context, userID string, rules []db.IgnoredAssetClass, assetClassToTxTypes map[string][]string) (int32, int32, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid user id: %w", err)
	}
	if len(rules) == 0 {
		return 0, 0, nil
	}

	txCount, err := countMatchingTxs(ctx, p.q, userUUID, rules, assetClassToTxTypes)
	if err != nil {
		return 0, 0, err
	}
	declCount, err := countMatchingDeclarations(ctx, p.q, userUUID, rules)
	if err != nil {
		return 0, 0, err
	}
	return txCount, declCount, nil
}

// deleteIgnoredTxs deletes regular and synthetic txs matching the ignore rules.
// Regular txs are matched by tx_type (reverse-mapped from asset class).
// Synthetic INITIALIZE txs are matched by joining to instruments.asset_class.
func deleteIgnoredTxs(ctx context.Context, tx queryable, userUUID uuid.UUID, rules []db.IgnoredAssetClass, assetClassToTxTypes map[string][]string) error {
	for _, r := range rules {
		txTypes := assetClassToTxTypes[r.AssetClass]
		if len(txTypes) == 0 {
			continue
		}
		// Delete regular txs by tx_type.
		query, args := buildDeleteTxsQuery(userUUID, r, txTypes)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("delete ignored txs: %w", err)
		}
		// Delete synthetic INITIALIZE txs by instrument asset_class.
		query, args = buildDeleteSyntheticTxsQuery(userUUID, r)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("delete ignored synthetic txs: %w", err)
		}
	}
	return nil
}

// deleteIgnoredDeclarations deletes holding declarations for instruments matching the ignored asset classes.
func deleteIgnoredDeclarations(ctx context.Context, tx queryable, userUUID uuid.UUID, rules []db.IgnoredAssetClass) error {
	for _, r := range rules {
		query, args := buildDeleteDeclarationsQuery(userUUID, r)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("delete ignored declarations: %w", err)
		}
	}
	return nil
}

// buildDeleteTxsQuery builds a DELETE for regular txs matching the rule's tx types.
func buildDeleteTxsQuery(userUUID uuid.UUID, r db.IgnoredAssetClass, txTypes []string) (string, []any) {
	// Build tx_type IN (...) placeholders starting at $3 or $4.
	baseIdx := 3
	if r.Account != "" {
		baseIdx = 4
	}
	placeholders := make([]string, len(txTypes))
	args := []any{userUUID, r.Broker}
	if r.Account != "" {
		args = append(args, r.Account)
	}
	for i, t := range txTypes {
		placeholders[i] = fmt.Sprintf("$%d", baseIdx+i)
		args = append(args, t)
	}

	accountClause := ""
	if r.Account != "" {
		accountClause = " AND account = $3"
	}
	query := fmt.Sprintf(`
		DELETE FROM txs
		WHERE user_id = $1 AND broker = $2%s
		  AND synthetic_purpose IS NULL
		  AND tx_type IN (%s)
	`, accountClause, strings.Join(placeholders, ", "))
	return query, args
}

// buildDeleteSyntheticTxsQuery builds a DELETE for synthetic INITIALIZE txs where the instrument's asset class matches.
func buildDeleteSyntheticTxsQuery(userUUID uuid.UUID, r db.IgnoredAssetClass) (string, []any) {
	args := []any{userUUID, r.Broker, r.AssetClass}
	accountClause := ""
	if r.Account != "" {
		accountClause = " AND t.account = $4"
		args = append(args, r.Account)
	}
	query := fmt.Sprintf(`
		DELETE FROM txs t
		USING instruments i
		WHERE t.user_id = $1 AND t.broker = $2%s
		  AND t.synthetic_purpose = 'INITIALIZE'
		  AND t.instrument_id = i.id
		  AND i.asset_class = $3
	`, accountClause)
	return query, args
}

// buildDeleteDeclarationsQuery builds a DELETE for holding declarations where the instrument's asset class matches.
func buildDeleteDeclarationsQuery(userUUID uuid.UUID, r db.IgnoredAssetClass) (string, []any) {
	args := []any{userUUID, r.Broker, r.AssetClass}
	accountClause := ""
	if r.Account != "" {
		accountClause = " AND hd.account = $4"
		args = append(args, r.Account)
	}
	query := fmt.Sprintf(`
		DELETE FROM holding_declarations hd
		USING instruments i
		WHERE hd.user_id = $1 AND hd.broker = $2%s
		  AND hd.instrument_id = i.id
		  AND i.asset_class = $3
	`, accountClause)
	return query, args
}

// countMatchingTxs counts regular txs that match the given ignore rules.
func countMatchingTxs(ctx context.Context, q queryable, userUUID uuid.UUID, rules []db.IgnoredAssetClass, assetClassToTxTypes map[string][]string) (int32, error) {
	var total int32
	for _, r := range rules {
		txTypes := assetClassToTxTypes[r.AssetClass]
		if len(txTypes) == 0 {
			continue
		}
		baseIdx := 3
		if r.Account != "" {
			baseIdx = 4
		}
		placeholders := make([]string, len(txTypes))
		args := []any{userUUID, r.Broker}
		if r.Account != "" {
			args = append(args, r.Account)
		}
		for i, t := range txTypes {
			placeholders[i] = fmt.Sprintf("$%d", baseIdx+i)
			args = append(args, t)
		}
		accountClause := ""
		if r.Account != "" {
			accountClause = " AND account = $3"
		}
		query := fmt.Sprintf(`
			SELECT COUNT(*) FROM txs
			WHERE user_id = $1 AND broker = $2%s
			  AND synthetic_purpose IS NULL
			  AND tx_type IN (%s)
		`, accountClause, strings.Join(placeholders, ", "))
		var count int32
		if err := q.QueryRowxContext(ctx, query, args...).Scan(&count); err != nil {
			return 0, fmt.Errorf("count ignored txs: %w", err)
		}
		total += count
	}
	return total, nil
}

// countMatchingDeclarations counts holding declarations whose instrument's asset class matches.
func countMatchingDeclarations(ctx context.Context, q queryable, userUUID uuid.UUID, rules []db.IgnoredAssetClass) (int32, error) {
	var total int32
	for _, r := range rules {
		args := []any{userUUID, r.Broker, r.AssetClass}
		accountClause := ""
		if r.Account != "" {
			accountClause = " AND hd.account = $4"
			args = append(args, r.Account)
		}
		query := fmt.Sprintf(`
			SELECT COUNT(*) FROM holding_declarations hd
			JOIN instruments i ON hd.instrument_id = i.id
			WHERE hd.user_id = $1 AND hd.broker = $2%s
			  AND i.asset_class = $3
		`, accountClause)
		var count int32
		if err := q.QueryRowxContext(ctx, query, args...).Scan(&count); err != nil {
			return 0, fmt.Errorf("count ignored declarations: %w", err)
		}
		total += count
	}
	return total, nil
}
