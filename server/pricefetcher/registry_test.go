package pricefetcher

import (
	"context"
	"testing"
	"time"
)

// stubPlugin is a minimal Plugin for registry tests.
type stubPlugin struct {
	name string
}

func (s *stubPlugin) DisplayName() string                     { return s.name }
func (s *stubPlugin) SupportedIdentifierTypes() []string      { return nil }
func (s *stubPlugin) AcceptableAssetClasses() map[string]bool { return nil }
func (s *stubPlugin) AcceptableExchanges() map[string]bool    { return nil }
func (s *stubPlugin) AcceptableCurrencies() map[string]bool   { return nil }
func (s *stubPlugin) FetchPrices(_ context.Context, _ []byte, _ []Identifier, _ string, _, _ time.Time) (*FetchResult, error) {
	return nil, ErrNoData
}
func (s *stubPlugin) DefaultConfig() []byte { return nil }

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	p := &stubPlugin{name: "Test"}
	r.Register("test", p)

	got := r.Get("test")
	if got != p {
		t.Fatalf("Get returned %v, want %v", got, p)
	}
	if r.Get("nonexistent") != nil {
		t.Error("Get(nonexistent) should return nil")
	}
}

func TestRegistry_RegisterNil(t *testing.T) {
	r := NewRegistry()
	r.Register("nil-plugin", nil)
	if len(r.ListIDs()) != 0 {
		t.Error("nil plugin should not be registered")
	}
}

func TestRegistry_ListIDs_PreservesOrder(t *testing.T) {
	r := NewRegistry()
	r.Register("c", &stubPlugin{name: "C"})
	r.Register("a", &stubPlugin{name: "A"})
	r.Register("b", &stubPlugin{name: "B"})

	ids := r.ListIDs()
	if len(ids) != 3 || ids[0] != "c" || ids[1] != "a" || ids[2] != "b" {
		t.Errorf("ListIDs = %v, want [c a b]", ids)
	}
}

func TestRegistry_RegisterIdempotent(t *testing.T) {
	r := NewRegistry()
	r.Register("x", &stubPlugin{name: "V1"})
	r.Register("x", &stubPlugin{name: "V2"})

	ids := r.ListIDs()
	if len(ids) != 1 {
		t.Fatalf("duplicate register should not add extra ID, got %v", ids)
	}
	if r.GetDisplayName("x") != "V2" {
		t.Error("re-register should replace plugin")
	}
}

func TestRegistry_GetDisplayName(t *testing.T) {
	r := NewRegistry()
	r.Register("p", &stubPlugin{name: "Pretty Name"})

	if r.GetDisplayName("p") != "Pretty Name" {
		t.Errorf("GetDisplayName = %q, want Pretty Name", r.GetDisplayName("p"))
	}
	if r.GetDisplayName("unknown") != "unknown" {
		t.Errorf("GetDisplayName for unknown should return the id")
	}
}
