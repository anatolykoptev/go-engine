package metrics

import (
	"context"
	"errors"
	"testing"
)

func TestTrackOperation_FastOp(t *testing.T) {
	err := TrackOperation(context.Background(), "fast-op", func(_ context.Context) error {
		return nil
	})
	if err != nil {
		t.Errorf("TrackOperation returned error: %v", err)
	}
}

func TestTrackOperation_PropagatesError(t *testing.T) {
	want := errors.New("test error")
	got := TrackOperation(context.Background(), "fail-op", func(_ context.Context) error {
		return want
	})
	if !errors.Is(got, want) {
		t.Errorf("TrackOperation error = %v, want %v", got, want)
	}
}
