package postgres

import (
	"context"
	"fmt"

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
func (p *Postgres) SetIgnoredAssetClasses(ctx context.Context, userID string, rules []db.IgnoredAssetClass) error {
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

		// 3. Delete matching txs, synthetic INITIALIZE txs included.
		if err := deleteIgnoredTxs(ctx, tx, userUUID, rules); err != nil {
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
func (p *Postgres) CountIgnoredTxs(ctx context.Context, userID string, rules []db.IgnoredAssetClass) (int32, int32, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid user id: %w", err)
	}
	if len(rules) == 0 {
		return 0, 0, nil
	}

	txCount, err := countMatchingTxs(ctx, p.q, userUUID, rules)
	if err != nil {
		return 0, 0, err
	}
	declCount, err := countMatchingDeclarations(ctx, p.q, userUUID, rules)
	if err != nil {
		return 0, 0, err
	}
	return txCount, declCount, nil
}

// deleteIgnoredTxs deletes txs, synthetic or not, whose instrument's asset
// class matches an ignore rule. A posting whose instrument is unresolved has
// no asset class and is left alone.
func deleteIgnoredTxs(ctx context.Context, tx queryable, userUUID uuid.UUID, rules []db.IgnoredAssetClass) error {
	for _, r := range rules {
		query, args := buildDeleteTxsQuery(userUUID, r)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("delete ignored txs: %w", err)
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

// buildDeleteTxsQuery builds a DELETE for txs where the instrument's asset class matches.
func buildDeleteTxsQuery(userUUID uuid.UUID, r db.IgnoredAssetClass) (string, []any) {
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

// countMatchingTxs counts regular txs whose instrument's asset class matches the
// given ignore rules. Only postings a source stated are counted: a pad comes from a
// holding declaration, which is counted separately, and a routed residual is
// arithmetic on legs the rule would have dropped before it was ever posted.
func countMatchingTxs(ctx context.Context, q queryable, userUUID uuid.UUID, rules []db.IgnoredAssetClass) (int32, error) {
	var total int32
	for _, r := range rules {
		args := []any{userUUID, r.Broker, r.AssetClass}
		accountClause := ""
		if r.Account != "" {
			accountClause = " AND t.account = $4"
			args = append(args, r.Account)
		}
		query := fmt.Sprintf(`
			SELECT COUNT(*) FROM txs t
			JOIN instruments i ON t.instrument_id = i.id
			WHERE t.user_id = $1 AND t.broker = $2%s
			  AND t.synthetic_purpose IS NULL
			  AND i.asset_class = $3
		`, accountClause)
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
