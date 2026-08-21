package pluginreg

import "testing"

// stub is a minimal Named implementation. The registry asks nothing else of a
// plugin, so the real families' interfaces add nothing a test here could use.
type stub struct {
	name string
}

func (s *stub) DisplayName() string { return s.name }

func TestRegisterAndGet(t *testing.T) {
	r := New[*stub]()
	p := &stub{name: "Test"}
	r.Register("test", p)

	if got := r.Get("test"); got != p {
		t.Fatalf("Get = %v, want %v", got, p)
	}
	if r.Get("nonexistent") != nil {
		t.Error("Get for an unregistered id should return the zero plugin")
	}
}

func TestRegisterNil(t *testing.T) {
	r := New[*stub]()
	r.Register("nil-plugin", nil)

	if len(r.ListIDs()) != 0 {
		t.Error("a nil plugin should not be registered")
	}
}

func TestListIDsPreservesOrder(t *testing.T) {
	r := New[*stub]()
	r.Register("c", &stub{name: "C"})
	r.Register("a", &stub{name: "A"})
	r.Register("b", &stub{name: "B"})

	ids := r.ListIDs()
	if len(ids) != 3 || ids[0] != "c" || ids[1] != "a" || ids[2] != "b" {
		t.Errorf("ListIDs = %v, want [c a b]", ids)
	}
}

// Registration order decides default precedence on first insert, so re-registering
// an id must replace the plugin without moving it.
func TestRegisterReplacesWithoutReordering(t *testing.T) {
	r := New[*stub]()
	r.Register("x", &stub{name: "V1"})
	r.Register("y", &stub{name: "Y"})
	r.Register("x", &stub{name: "V2"})

	ids := r.ListIDs()
	if len(ids) != 2 || ids[0] != "x" || ids[1] != "y" {
		t.Fatalf("ListIDs = %v, want [x y]", ids)
	}
	if r.GetDisplayName("x") != "V2" {
		t.Errorf("re-register should replace the plugin, got %q", r.GetDisplayName("x"))
	}
}

func TestGetDisplayName(t *testing.T) {
	r := New[*stub]()
	r.Register("p", &stub{name: "Pretty Name"})

	if r.GetDisplayName("p") != "Pretty Name" {
		t.Errorf("GetDisplayName = %q, want Pretty Name", r.GetDisplayName("p"))
	}
	if r.GetDisplayName("unknown") != "unknown" {
		t.Error("GetDisplayName for an unregistered id should return the id")
	}
}

// ListIDs must hand back a copy: a caller that sorts or truncates the slice it
// gets would otherwise reorder the precedence the registry is recording.
func TestListIDsReturnsACopy(t *testing.T) {
	r := New[*stub]()
	r.Register("a", &stub{name: "A"})
	r.Register("b", &stub{name: "B"})

	ids := r.ListIDs()
	ids[0] = "mutated"

	if again := r.ListIDs(); again[0] != "a" {
		t.Errorf("mutating the returned slice changed the registry: %v", again)
	}
}
