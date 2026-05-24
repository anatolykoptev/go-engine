package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// modelEchoServer возвращает 500 если model совпадает с failModel,
// иначе echoes back okBody. Засчитывает количество hits per model.
func modelEchoServer(t *testing.T, failModel, okBody string) (*httptest.Server, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	var failHits, okHits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		model, _ := req["model"].(string)
		if model == failModel {
			failHits.Add(1)
			http.Error(w, "model unavailable", http.StatusInternalServerError)
			return
		}
		okHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResponse{
			Choices: []mockChoice{{Message: mockMessage{Content: okBody}}},
		})
	}))
	return srv, &failHits, &okHits
}

func TestWithModelFallbackChain_FallsThroughChain(t *testing.T) {
	srv, failHits, okHits := modelEchoServer(t, "primary-broken", "ok from fallback")
	defer srv.Close()

	c := New(
		WithAPIBase(srv.URL),
		WithAPIKey("test-key"),
		WithModel("primary-broken"),
		WithModelFallbackChain([]string{"fallback-a", "fallback-b"}),
	)

	out, err := c.Complete(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "ok from fallback" {
		t.Errorf("output = %q, want %q", out, "ok from fallback")
	}
	if failHits.Load() == 0 {
		t.Error("expected primary model to be tried at least once")
	}
	if okHits.Load() == 0 {
		t.Error("expected fallback model to succeed at least once")
	}
}

func TestWithModelFallbackChain_NilOrEmpty_NoOp(t *testing.T) {
	srv, _, _ := modelEchoServer(t, "", "ok")
	defer srv.Close()

	// nil chain должна работать как без option — single primary model.
	c := New(
		WithAPIBase(srv.URL),
		WithAPIKey("test-key"),
		WithModel("only-model"),
		WithModelFallbackChain(nil),
	)
	out, err := c.Complete(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "ok" {
		t.Errorf("nil chain output = %q, want %q", out, "ok")
	}

	// Пустая slice — то же.
	c2 := New(
		WithAPIBase(srv.URL),
		WithAPIKey("test-key"),
		WithModel("only-model"),
		WithModelFallbackChain([]string{}),
	)
	out2, err := c2.Complete(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out2 != "ok" {
		t.Errorf("empty chain output = %q, want %q", out2, "ok")
	}
}

func TestWithModelChainObserver_FiresPerModel(t *testing.T) {
	srv, _, _ := modelEchoServer(t, "primary-broken", "ok")
	defer srv.Close()

	type obsCall struct {
		model string
		ok    bool
	}
	var calls []obsCall
	obs := func(ep Endpoint, err error) {
		calls = append(calls, obsCall{model: ep.Model, ok: err == nil})
	}

	c := New(
		WithAPIBase(srv.URL),
		WithAPIKey("k"),
		WithModel("primary-broken"),
		WithModelFallbackChain([]string{"fallback"}),
		WithModelChainObserver(obs),
	)
	_, err := c.Complete(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 observer calls (primary fail + fallback ok), got %d: %+v", len(calls), calls)
	}
	if calls[0].model != "primary-broken" || calls[0].ok {
		t.Errorf("calls[0] = %+v, want {primary-broken, false}", calls[0])
	}
	if calls[1].model != "fallback" || !calls[1].ok {
		t.Errorf("calls[1] = %+v, want {fallback, true}", calls[1])
	}
}

func TestParseModelFallbackChain_Reexport(t *testing.T) {
	got := ParseModelFallbackChain("a, b ,c,a,")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len=%d, want %d, got %v", len(got), len(want), got)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("got[%d] = %q, want %q", i, got[i], v)
		}
	}
}
