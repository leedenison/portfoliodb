package postgres

import (
	"context"
	"testing"

	"github.com/leedenison/portfoliodb/server/db"
)

func TestPortfolioCRUD(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|p", "U", "u@u.com")
	list, next, err := p.ListPortfolios(ctx, userID, 10, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 || next != "" {
		t.Fatalf("initial list should be empty: %v %s", list, next)
	}
	port, err := p.CreatePortfolio(ctx, userID, "My Portfolio")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if port.GetId() == "" || port.GetName() != "My Portfolio" {
		t.Fatalf("create: %v", port)
	}
	port2, uid, err := p.GetPortfolio(ctx, port.GetId())
	if err != nil || port2 == nil || uid != userID {
		t.Fatalf("get: %v %v %v", err, port2, uid)
	}
	port3, err := p.UpdatePortfolio(ctx, port.GetId(), "Renamed")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if port3.GetName() != "Renamed" {
		t.Fatalf("update name: %s", port3.GetName())
	}
	ok, _ := p.PortfolioBelongsToUser(ctx, port.GetId(), userID)
	if !ok {
		t.Fatal("portfolio should belong to user")
	}
	if err := p.DeletePortfolio(ctx, port.GetId()); err != nil {
		t.Fatalf("delete: %v", err)
	}
	port4, _, _ := p.GetPortfolio(ctx, port.GetId())
	if port4 != nil {
		t.Fatal("portfolio should be gone")
	}
}

func TestListPortfolioFilters_SetPortfolioFilters(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|pf", "U", "u@u.com")
	port, err := p.CreatePortfolio(ctx, userID, "P")
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	list, err := p.ListPortfolioFilters(ctx, port.GetId())
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("initial filters should be empty: %v", list)
	}
	filters := []db.PortfolioFilter{
		{FilterType: "broker", FilterValue: "IBKR"},
		{FilterType: "account", FilterValue: "Acc1"},
	}
	if err := p.SetPortfolioFilters(ctx, port.GetId(), filters); err != nil {
		t.Fatalf("set filters: %v", err)
	}
	list, err = p.ListPortfolioFilters(ctx, port.GetId())
	if err != nil {
		t.Fatalf("list after set: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 filters, got %v", list)
	}
	// Replace-all: set different filters
	filters2 := []db.PortfolioFilter{{FilterType: "broker", FilterValue: "SCHB"}}
	if err := p.SetPortfolioFilters(ctx, port.GetId(), filters2); err != nil {
		t.Fatalf("set filters 2: %v", err)
	}
	list, err = p.ListPortfolioFilters(ctx, port.GetId())
	if err != nil {
		t.Fatalf("list after replace: %v", err)
	}
	if len(list) != 1 || list[0].FilterType != "broker" || list[0].FilterValue != "SCHB" {
		t.Fatalf("expected single broker=SCHB filter, got %v", list)
	}
}
