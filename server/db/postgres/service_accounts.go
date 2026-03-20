package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/leedenison/portfoliodb/server/db"
)

// GetServiceAccount implements db.ServiceAccountDB.
func (p *Postgres) GetServiceAccount(ctx context.Context, id string) (*db.ServiceAccountRow, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("invalid service account id: %w", err)
	}
	var (
		saID             uuid.UUID
		name             string
		clientSecretHash string
		role             string
	)
	err = p.q.QueryRowContext(ctx,
		`SELECT id, name, client_secret_hash, role FROM service_accounts WHERE id = $1`,
		uid,
	).Scan(&saID, &name, &clientSecretHash, &role)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get service account: %w", err)
	}
	return &db.ServiceAccountRow{
		ID:               saID.String(),
		Name:             name,
		ClientSecretHash: clientSecretHash,
		Role:             role,
	}, nil
}
