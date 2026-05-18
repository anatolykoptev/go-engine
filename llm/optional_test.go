package llm

import (
	"context"
	"errors"
	"testing"

	kitllm "github.com/anatolykoptev/go-kit/llm"
)

func TestNewOptional_EmptyKeyDisabled(t *testing.T) {
	c, ok := NewOptional(WithAPIKey(""))
	if ok {
		t.Fatal("expected ok=false on empty key")
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}

	ctx := context.Background()

	_, err := c.Complete(ctx, "test")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Complete: want ErrUnavailable, got %v", err)
	}

	_, err = c.CompleteParams(ctx, "test", 0.5, 100)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("CompleteParams: want ErrUnavailable, got %v", err)
	}

	_, err = c.CompleteWithSystem(ctx, "sys", "user")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("CompleteWithSystem: want ErrUnavailable, got %v", err)
	}

	_, err = c.CompleteMultimodal(ctx, "test", nil)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("CompleteMultimodal: want ErrUnavailable, got %v", err)
	}
}

func TestNewOptional_NonEmptyKeyActive(t *testing.T) {
	c, ok := NewOptional(
		WithAPIKey("test-key"),
		WithAPIBase("http://example"),
		WithModel("test-model"),
	)
	if !ok {
		t.Fatal("expected ok=true with key")
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.disabled {
		t.Fatal("expected disabled=false when key is present")
	}
}

func TestErrUnavailable_SameSentinelAsKit(t *testing.T) {
	// ErrUnavailable is a direct assignment from kitllm.ErrUnavailable.
	// errors.Is checks by value equality — both directions must hold.
	if !errors.Is(ErrUnavailable, kitllm.ErrUnavailable) {
		t.Fatal("engine.ErrUnavailable must satisfy errors.Is(engine.ErrUnavailable, kitllm.ErrUnavailable)")
	}
}
