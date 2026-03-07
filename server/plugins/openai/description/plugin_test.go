package description

import (
	"context"
	"log/slog"
	"testing"

	"github.com/leedenison/portfoliodb/server/telemetry"
)

func TestIsOpenAIModelNotFound(t *testing.T) {
	tests := []struct {
		err  string
		want bool
	}{
		{"openai 404: {}", true},
		{`openai 404: {"error":{"code":"model_not_found"}}`, true},
		{"the model `gpt-5.2` was not found", true},
		{"openai 429: quota", false},
		{"openai 200: ok", false},
	}
	for _, tt := range tests {
		got := isOpenAIModelNotFound(tt.err)
		if got != tt.want {
			t.Errorf("isOpenAIModelNotFound(%q) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestIsOpenAIQuotaExceeded(t *testing.T) {
	tests := []struct {
		err  string
		want bool
	}{
		{"openai 429: {}", true},
		{`{"error":{"code":"insufficient_quota"}}`, true},
		{"You exceeded your current quota", true},
		{"openai 404: not found", false},
		{"openai 200: ok", false},
	}
	for _, tt := range tests {
		got := isOpenAIQuotaExceeded(tt.err)
		if got != tt.want {
			t.Errorf("isOpenAIQuotaExceeded(%q) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

// recordingCounter records the last counter name passed to Incr.
type recordingCounter struct {
	last string
}

func (r *recordingCounter) Incr(_ context.Context, name string) { r.last = name }

func (r *recordingCounter) IncrBy(_ context.Context, name string, _ int64) { r.last = name }

func TestHandleOpenAIError_IncrementsCounters(t *testing.T) {
	ctx := context.Background()

	t.Run("model not found", func(t *testing.T) {
		c := &recordingCounter{}
		p := NewPlugin(c, slog.Default())
		p.handleOpenAIError(ctx, "INRG", &errWithMessage{"openai 404: model not found"})
		if c.last != CounterModelNotFound {
			t.Errorf("counter = %q, want %q", c.last, CounterModelNotFound)
		}
	})

	t.Run("quota exceeded", func(t *testing.T) {
		c := &recordingCounter{}
		p := NewPlugin(c, slog.Default())
		p.handleOpenAIError(ctx, "VUSA", &errWithMessage{"openai 429: insufficient_quota"})
		if c.last != CounterQuotaExceeded {
			t.Errorf("counter = %q, want %q", c.last, CounterQuotaExceeded)
		}
	})

	t.Run("nil counter no panic", func(t *testing.T) {
		p := NewPlugin(nil, slog.Default())
		p.handleOpenAIError(ctx, "X", &errWithMessage{"openai 404: not found"})
	})

	t.Run("other error no increment", func(t *testing.T) {
		c := &recordingCounter{}
		p := NewPlugin(c, slog.Default())
		p.handleOpenAIError(ctx, "X", &errWithMessage{"openai 500: internal server error"})
		if c.last != "" {
			t.Errorf("unexpected counter increment: %q", c.last)
		}
	})
}

type errWithMessage struct{ msg string }

func (e *errWithMessage) Error() string { return e.msg }

var _ telemetry.CounterIncrementer = (*recordingCounter)(nil)
