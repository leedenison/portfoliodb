package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// ListPortfolios implements db.PortfolioDB.
func (p *Postgres) ListPortfolios(ctx context.Context, userID string, pageSize int32, pageToken string) ([]*apiv1.Portfolio, string, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid user id: %w", err)
	}
	limit := pageSize
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := decodePageToken(pageToken)
	rows, err := p.q.QueryContext(ctx, `
		SELECT id, name, created_at FROM portfolios
		WHERE user_id = $1
		ORDER BY created_at
		LIMIT $2 OFFSET $3
	`, userUUID, limit+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("list portfolios: %w", err)
	}
	defer rows.Close()
	var out []*apiv1.Portfolio
	var n int32
	for rows.Next() && n < limit {
		var id uuid.UUID
		var name string
		var createdAt time.Time
		if err := rows.Scan(&id, &name, &createdAt); err != nil {
			return nil, "", err
		}
		out = append(out, &apiv1.Portfolio{
			Id:        id.String(),
			Name:      name,
			CreatedAt: timeToTs(createdAt),
		})
		n++
	}
	var nextToken string
	if n == limit+1 || (rows.Next() && n == limit) {
		nextToken = encodePageToken(offset + int64(limit))
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	return out, nextToken, nil
}

// GetPortfolio implements db.PortfolioDB.
func (p *Postgres) GetPortfolio(ctx context.Context, portfolioID string) (*apiv1.Portfolio, string, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid portfolio id: %w", err)
	}
	var id uuid.UUID
	var userID uuid.UUID
	var name string
	var createdAt time.Time
	err = p.q.QueryRowContext(ctx, `
		SELECT id, user_id, name, created_at FROM portfolios WHERE id = $1
	`, portUUID).Scan(&id, &userID, &name, &createdAt)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("get portfolio: %w", err)
	}
	return &apiv1.Portfolio{
		Id:        id.String(),
		Name:      name,
		CreatedAt: timeToTs(createdAt),
	}, userID.String(), nil
}

// CreatePortfolio implements db.PortfolioDB.
func (p *Postgres) CreatePortfolio(ctx context.Context, userID, name string) (*apiv1.Portfolio, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	var id uuid.UUID
	var createdAt time.Time
	err = p.q.QueryRowContext(ctx, `
		INSERT INTO portfolios (user_id, name) VALUES ($1, $2)
		RETURNING id, created_at
	`, userUUID, name).Scan(&id, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("create portfolio: %w", err)
	}
	return &apiv1.Portfolio{
		Id:        id.String(),
		Name:      name,
		CreatedAt: timeToTs(createdAt),
	}, nil
}

// UpdatePortfolio implements db.PortfolioDB.
func (p *Postgres) UpdatePortfolio(ctx context.Context, portfolioID, name string) (*apiv1.Portfolio, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return nil, fmt.Errorf("invalid portfolio id: %w", err)
	}
	var createdAt time.Time
	err = p.q.QueryRowContext(ctx, `
		UPDATE portfolios SET name = $2 WHERE id = $1
		RETURNING created_at
	`, portUUID, name).Scan(&createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update portfolio: %w", err)
	}
	return &apiv1.Portfolio{
		Id:        portfolioID,
		Name:      name,
		CreatedAt: timeToTs(createdAt),
	}, nil
}

// DeletePortfolio implements db.PortfolioDB.
func (p *Postgres) DeletePortfolio(ctx context.Context, portfolioID string) error {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return fmt.Errorf("invalid portfolio id: %w", err)
	}
	res, err := p.q.ExecContext(ctx, `DELETE FROM portfolios WHERE id = $1`, portUUID)
	if err != nil {
		return fmt.Errorf("delete portfolio: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil
	}
	return nil
}

// PortfolioBelongsToUser implements db.PortfolioDB.
func (p *Postgres) PortfolioBelongsToUser(ctx context.Context, portfolioID, userID string) (bool, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return false, fmt.Errorf("invalid portfolio id: %w", err)
	}
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return false, fmt.Errorf("invalid user id: %w", err)
	}
	var count int
	err = p.q.QueryRowContext(ctx, `SELECT 1 FROM portfolios WHERE id = $1 AND user_id = $2`, portUUID, userUUID).Scan(&count)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("portfolio belongs to user: %w", err)
	}
	return true, nil
}

// ListPortfolioFilters implements db.PortfolioDB.
func (p *Postgres) ListPortfolioFilters(ctx context.Context, portfolioID string) ([]db.PortfolioFilter, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return nil, fmt.Errorf("invalid portfolio id: %w", err)
	}
	rows, err := p.q.QueryContext(ctx, `SELECT filter_type, filter_value FROM portfolio_filters WHERE portfolio_id = $1`, portUUID)
	if err != nil {
		return nil, fmt.Errorf("list portfolio filters: %w", err)
	}
	defer rows.Close()
	var out []db.PortfolioFilter
	for rows.Next() {
		var f db.PortfolioFilter
		if err := rows.Scan(&f.FilterType, &f.FilterValue); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListBrokersAndAccounts implements db.PortfolioDB.
func (p *Postgres) ListBrokersAndAccounts(ctx context.Context, userID string) ([]db.BrokerAccount, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	rows, err := p.q.QueryContext(ctx, `
		SELECT DISTINCT broker, account FROM txs
		WHERE user_id = $1
		ORDER BY broker, account
	`, userUUID)
	if err != nil {
		return nil, fmt.Errorf("list brokers and accounts: %w", err)
	}
	defer rows.Close()
	var out []db.BrokerAccount
	for rows.Next() {
		var ba db.BrokerAccount
		if err := rows.Scan(&ba.Broker, &ba.Account); err != nil {
			return nil, err
		}
		out = append(out, ba)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SetPortfolioFilters implements db.PortfolioDB.
func (p *Postgres) SetPortfolioFilters(ctx context.Context, portfolioID string, filters []db.PortfolioFilter) error {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return fmt.Errorf("invalid portfolio id: %w", err)
	}
	return p.runInTx(ctx, func(exec queryable) error {
		if _, err := exec.ExecContext(ctx, `DELETE FROM portfolio_filters WHERE portfolio_id = $1`, portUUID); err != nil {
			return fmt.Errorf("delete portfolio filters: %w", err)
		}
		for _, f := range filters {
			if f.FilterType != "broker" && f.FilterType != "account" && f.FilterType != "instrument" {
				return fmt.Errorf("invalid filter_type %q", f.FilterType)
			}
			if _, err := exec.ExecContext(ctx, `INSERT INTO portfolio_filters (portfolio_id, filter_type, filter_value) VALUES ($1, $2, $3)`,
				portUUID, f.FilterType, f.FilterValue); err != nil {
				return fmt.Errorf("insert portfolio filter: %w", err)
			}
		}
		return nil
	})
}
