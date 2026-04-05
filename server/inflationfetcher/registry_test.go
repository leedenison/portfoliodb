package inflationfetcher

import (
	"context"
	"testing"
	"time"
)

// stubPlugin is a minimal Plugin implementation for registry tests.
type stubPlugin struct {
	name       string
	currencies []string
}

func (s *stubPlugin) DisplayName() string            { return s.name }
func (s *stubPlugin) SupportedCurrencies() []string   { return s.currencies }
func (s *stubPlugin) DefaultConfig() []byte            { return []byte(`{}`) }
func (s *stubPlugin) FetchInflation(_ context.Context, _ []byte, _ string, _, _ time.Time) (*FetchResult, error) {
	return nil, ErrNoData
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	p := &stubPlugin{name: "Test", currencies: []string{"GBP"}}

	r.Register("test", p)

	got := r.Get("test")
	if got != p {
		t.Fatal("expected registered plugin")
	}
	if r.Get("unknown") != nil {
		t.Fatal("expected nil for unknown plugin")
	}
}

func TestRegistry_ListIDs(t *testing.T) {
	r := NewRegistry()
	r.Register("a", &stubPlugin{name: "A"})
	r.Register("b", &stubPlugin{name: "B"})

	ids := r.ListIDs()
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("expected [a b], got %v", ids)
	}
}

func TestRegistry_RegisterNil(t *testing.T) {
	r := NewRegistry()
	r.Register("nil", nil)

	if len(r.ListIDs()) != 0 {
		t.Fatal("nil plugin should not be registered")
	}
}

func TestRegistry_GetDisplayName(t *testing.T) {
	r := NewRegistry()
	r.Register("ons", &stubPlugin{name: "ONS (UK)"})

	if r.GetDisplayName("ons") != "ONS (UK)" {
		t.Fatalf("expected 'ONS (UK)', got %q", r.GetDisplayName("ons"))
	}
	if r.GetDisplayName("unknown") != "unknown" {
		t.Fatalf("expected 'unknown' for unregistered, got %q", r.GetDisplayName("unknown"))
	}
}

func TestRegistry_ReplaceExisting(t *testing.T) {
	r := NewRegistry()
	r.Register("x", &stubPlugin{name: "Old"})
	r.Register("x", &stubPlugin{name: "New"})

	ids := r.ListIDs()
	if len(ids) != 1 {
		t.Fatalf("expected 1 ID after replace, got %d", len(ids))
	}
	if r.GetDisplayName("x") != "New" {
		t.Fatalf("expected replaced plugin, got %q", r.GetDisplayName("x"))
	}
}
