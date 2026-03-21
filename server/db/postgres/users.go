package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// GetOrCreateUser implements db.UserDB.
func (p *Postgres) GetOrCreateUser(ctx context.Context, authSub, name, email string) (string, error) {
	var id uuid.UUID
	err := p.q.QueryRowContext(ctx, `
		INSERT INTO users (auth_sub, name, email)
		VALUES ($1, $2, $3)
		ON CONFLICT (auth_sub) DO UPDATE SET name = $2, email = $3
		RETURNING id
	`, authSub, name, email).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("get or create user: %w", err)
	}
	return id.String(), nil
}

// GetUserByAuthSub implements db.UserDB.
func (p *Postgres) GetUserByAuthSub(ctx context.Context, authSub string) (userID, role string, err error) {
	var id uuid.UUID
	var roleVal string
	queryErr := p.q.QueryRowContext(ctx, `SELECT id, role FROM users WHERE auth_sub = $1`, authSub).Scan(&id, &roleVal)
	if queryErr == sql.ErrNoRows {
		return "", "", nil
	}
	if queryErr != nil {
		return "", "", fmt.Errorf("get user by auth sub: %w", queryErr)
	}
	return id.String(), roleVal, nil
}

// GetUserByEmail implements db.UserDB.
func (p *Postgres) GetUserByEmail(ctx context.Context, email string) (userID string, err error) {
	var id uuid.UUID
	queryErr := p.q.QueryRowContext(ctx, `SELECT id FROM users WHERE LOWER(email) = LOWER($1) LIMIT 1`, email).Scan(&id)
	if queryErr == sql.ErrNoRows {
		return "", nil
	}
	if queryErr != nil {
		return "", fmt.Errorf("get user by email: %w", queryErr)
	}
	return id.String(), nil
}

// GetDisplayCurrency implements db.UserDB.
func (p *Postgres) GetDisplayCurrency(ctx context.Context, userID string) (string, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return "", fmt.Errorf("invalid user id: %w", err)
	}
	var currency string
	if err := p.q.QueryRowContext(ctx, `SELECT display_currency FROM users WHERE id = $1`, userUUID).Scan(&currency); err != nil {
		return "", fmt.Errorf("get display currency: %w", err)
	}
	return currency, nil
}

// SetDisplayCurrency implements db.UserDB.
func (p *Postgres) SetDisplayCurrency(ctx context.Context, userID, currency string) error {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("invalid user id: %w", err)
	}
	_, err = p.q.ExecContext(ctx, `UPDATE users SET display_currency = $1 WHERE id = $2`, currency, userUUID)
	if err != nil {
		return fmt.Errorf("set display currency: %w", err)
	}
	return nil
}

// UpdateUserAuthSub implements db.UserDB.
func (p *Postgres) UpdateUserAuthSub(ctx context.Context, userID, authSub string) error {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("invalid user id: %w", err)
	}
	_, err = p.q.ExecContext(ctx, `UPDATE users SET auth_sub = $1 WHERE id = $2`, authSub, userUUID)
	if err != nil {
		return fmt.Errorf("update user auth_sub: %w", err)
	}
	return nil
}
