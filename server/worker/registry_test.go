package worker

import (
	"testing"
)

func TestNewRegistry_Empty(t *testing.T) {
	r := NewRegistry()
	if got := r.List(); len(got) != 0 {
		t.Fatalf("expected empty list, got %d", len(got))
	}
}

func TestSetRunning(t *testing.T) {
	r := NewRegistry()
	r.SetRunning("w1", "doing stuff")
	list := r.List()
	if len(list) != 1 {
		t.Fatalf("expected 1, got %d", len(list))
	}
	if list[0].State != Running || list[0].Summary != "doing stuff" {
		t.Errorf("got state=%v summary=%q", list[0].State, list[0].Summary)
	}
}

func TestSetIdle_ClearsSummary(t *testing.T) {
	r := NewRegistry()
	r.SetRunning("w1", "busy")
	r.SetIdle("w1")
	list := r.List()
	if list[0].State != Idle || list[0].Summary != "" {
		t.Errorf("got state=%v summary=%q", list[0].State, list[0].Summary)
	}
}

func TestSetQueueDepth(t *testing.T) {
	r := NewRegistry()
	r.SetQueueDepth("w1", 5)
	list := r.List()
	if len(list) != 1 {
		t.Fatalf("expected 1, got %d", len(list))
	}
	if list[0].QueueDepth != 5 {
		t.Errorf("expected queue_depth=5, got %d", list[0].QueueDepth)
	}
}

func TestListReturnsSnapshot(t *testing.T) {
	r := NewRegistry()
	r.SetIdle("a")
	r.SetRunning("b", "working")
	list := r.List()
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
	// Mutate returned slice; original should be unaffected.
	list[0].Summary = "hacked"
	fresh := r.List()
	for _, s := range fresh {
		if s.Summary == "hacked" {
			t.Error("List returned a reference, not a copy")
		}
	}
}
