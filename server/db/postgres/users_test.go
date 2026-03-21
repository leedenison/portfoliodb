package postgres

import (
	"context"
	"testing"
)

func TestGetOrCreateUser(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	id1, err := p.GetOrCreateUser(ctx, "sub|1", "Alice", "a@b.com")
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if id1 == "" {
		t.Fatal("expected non-empty user id")
	}
	id2, err := p.GetOrCreateUser(ctx, "sub|1", "Alice Updated", "a2@b.com")
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("same auth_sub should return same id: %s != %s", id1, id2)
	}
}

func TestGetUserByAuthSub_ReturnsRole(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	id, err := p.GetOrCreateUser(ctx, "sub|role-test", "U", "u@u.com")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	userID, role, err := p.GetUserByAuthSub(ctx, "sub|role-test")
	if err != nil {
		t.Fatalf("GetUserByAuthSub: %v", err)
	}
	if userID != id || role != "user" {
		t.Fatalf("GetUserByAuthSub: got userID=%q role=%q, want userID=%q role=user", userID, role, id)
	}
	// Unknown auth_sub returns empty id and role, no error
	userID2, role2, err := p.GetUserByAuthSub(ctx, "sub|nonexistent")
	if err != nil {
		t.Fatalf("GetUserByAuthSub nonexistent: %v", err)
	}
	if userID2 != "" || role2 != "" {
		t.Fatalf("GetUserByAuthSub nonexistent: got userID=%q role=%q", userID2, role2)
	}
}

func TestDisplayCurrency(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, err := p.GetOrCreateUser(ctx, "sub|dc-test", "U", "u@dc.com")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Default should be USD.
	dc, err := p.GetDisplayCurrency(ctx, userID)
	if err != nil {
		t.Fatalf("GetDisplayCurrency: %v", err)
	}
	if dc != "USD" {
		t.Fatalf("default display currency: want USD, got %q", dc)
	}

	// Set to EUR.
	if err := p.SetDisplayCurrency(ctx, userID, "EUR"); err != nil {
		t.Fatalf("SetDisplayCurrency: %v", err)
	}
	dc, err = p.GetDisplayCurrency(ctx, userID)
	if err != nil {
		t.Fatalf("GetDisplayCurrency after set: %v", err)
	}
	if dc != "EUR" {
		t.Fatalf("display currency after set: want EUR, got %q", dc)
	}
}
