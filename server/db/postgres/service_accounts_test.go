package postgres

import (
	"context"
	"testing"
)

func TestGetServiceAccount(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	// Insert a service account via raw SQL
	var id string
	err := p.q.QueryRowContext(ctx,
		`INSERT INTO service_accounts (name, client_secret_hash, role)
		 VALUES ('test-sa', '$2a$04$placeholder', 'service_account')
		 RETURNING id`,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert service account: %v", err)
	}

	row, err := p.GetServiceAccount(ctx, id)
	if err != nil {
		t.Fatalf("GetServiceAccount: %v", err)
	}
	if row == nil {
		t.Fatal("expected non-nil row")
	}
	if row.ID != id {
		t.Fatalf("ID: got %q, want %q", row.ID, id)
	}
	if row.Name != "test-sa" {
		t.Fatalf("Name: got %q, want test-sa", row.Name)
	}
	if row.ClientSecretHash != "$2a$04$placeholder" {
		t.Fatalf("ClientSecretHash: got %q", row.ClientSecretHash)
	}
	if row.Role != "service_account" {
		t.Fatalf("Role: got %q, want service_account", row.Role)
	}
}

func TestGetServiceAccount_NotFound(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	row, err := p.GetServiceAccount(ctx, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("GetServiceAccount nonexistent: %v", err)
	}
	if row != nil {
		t.Fatalf("expected nil for nonexistent ID, got %+v", row)
	}
}

func TestGetServiceAccount_InvalidID(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	_, err := p.GetServiceAccount(ctx, "not-a-uuid")
	if err == nil {
		t.Fatal("expected error for invalid UUID")
	}
}
