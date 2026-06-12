package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	kitllm "github.com/anatolykoptev/go-kit/llm"
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

// modelsServer returns a test HTTP server that serves GET /v1/models with the
// given model ids, and an OpenAI-compatible chat endpoint. Both share one port.
func modelsServer(t *testing.T, modelIDs []string, chatBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			type modelObj struct {
				ID string `json:"id"`
			}
			type listResp struct {
				Data []modelObj `json:"data"`
			}
			items := make([]modelObj, len(modelIDs))
			for i, id := range modelIDs {
				items[i] = modelObj{ID: id}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(listResp{Data: items})
			return
		}
		// chat completions
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, chatBody)
	}))
	return srv
}

// TestWithModelFallbackChain_FilteredVariant_DropsAbsentModel verifies that
// when BuildModelChainEndpointsFiltered is wired via New(), a model absent
// from /v1/models is dropped from the chain. The test:
//   - serves /v1/models with only "present-model" (not "absent-model")
//   - configures primary="absent-model" chain=["present-model"]
//   - expects Complete() to succeed via the present model (not 500 on absent)
//   - expects the filter observer to report one dropped model
//
// RED-on-revert: revert the BuildModelChainEndpointsFiltered → BuildModelChainEndpoints
// change in client.go and this test fails because absent-model is hit first (500),
// observer fires zero Dropped items, and the fallback-hit assertion fails.
func TestWithModelFallbackChain_FilteredVariant_DropsAbsentModel(t *testing.T) {
	srv := modelsServer(t, []string{"present-model"}, "ok-filtered")
	defer srv.Close()

	// Track filter observer events.
	var filterEvents []ModelFilterEvent
	obs := func(ev ModelFilterEvent) {
		filterEvents = append(filterEvents, ev)
	}

	// Separate server for the chat path — shares base URL above; both are the
	// same httptest server so requests route correctly.
	c := New(
		WithAPIBase(srv.URL),
		WithAPIKey("k"),
		WithModel("absent-model"),
		WithModelFallbackChain([]string{"present-model"}),
		WithModelFilterObserver(obs),
	)

	out, err := c.Complete(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "ok-filtered" {
		t.Errorf("output = %q, want %q", out, "ok-filtered")
	}

	// Observer must have fired exactly once at construction.
	if len(filterEvents) != 1 {
		t.Fatalf("expected 1 filter event, got %d", len(filterEvents))
	}
	ev := filterEvents[0]
	if ev.Degraded {
		t.Errorf("filter event Degraded=true (reason=%q), want clean filter", ev.Reason)
	}
	if len(ev.Dropped) != 1 || ev.Dropped[0] != "absent-model" {
		t.Errorf("Dropped = %v, want [absent-model]", ev.Dropped)
	}
	if ev.Kept != 1 {
		t.Errorf("Kept = %d, want 1", ev.Kept)
	}
}

// TestWithModelFallbackChain_FilteredVariant_DegradeOnModelsDown verifies that
// when /v1/models is unreachable, the filter degrades to the full chain without
// changing observable Complete() behaviour.
func TestWithModelFallbackChain_FilteredVariant_DegradeOnModelsDown(t *testing.T) {
	// Chat server that always succeeds (no /v1/models endpoint → non-200).
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			http.NotFound(w, r)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		model, _ := req["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, "from-"+model)
	}))
	defer chatSrv.Close()

	var filterEvents []ModelFilterEvent
	c := New(
		WithAPIBase(chatSrv.URL),
		WithAPIKey("k"),
		WithModel("primary"),
		WithModelFallbackChain([]string{"fallback"}),
		WithModelFilterObserver(func(ev ModelFilterEvent) {
			filterEvents = append(filterEvents, ev)
		}),
	)

	out, err := c.Complete(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "from-primary" {
		t.Errorf("output = %q, want %q", out, "from-primary")
	}

	if len(filterEvents) != 1 {
		t.Fatalf("expected 1 filter event, got %d", len(filterEvents))
	}
	ev := filterEvents[0]
	if !ev.Degraded {
		t.Errorf("expected Degraded=true when /v1/models is 404, got false")
	}
	// Full chain preserved: both models remain as endpoints.
	if ev.Kept != 2 {
		t.Errorf("Kept = %d, want 2 (full chain on degrade)", ev.Kept)
	}
}

// TestModelFilterObserver_NilSafe ensures New() with nil filterObserver does
// not panic when the filter runs.
func TestModelFilterObserver_NilSafe(t *testing.T) {
	srv := modelsServer(t, []string{"m"}, "ok")
	defer srv.Close()

	c := New(
		WithAPIBase(srv.URL),
		WithAPIKey("k"),
		WithModel("m"),
		WithModelFallbackChain([]string{"m2"}),
		// no WithModelFilterObserver → nil observer
	)
	_, err := c.Complete(context.Background(), "hi")
	if err != nil {
		t.Fatalf("nil-observer Complete: %v", err)
	}
}

// TestModelFilterObserver_Reexport ensures ModelFilterObserver and
// ModelFilterEvent types are usable from the engine/llm package (not just
// go-kit/llm) so consumers need only import go-engine.
func TestModelFilterObserver_Reexport(t *testing.T) {
	// ModelFilterObserver and ModelFilterEvent are type aliases for go-kit types.
	// Assigning a go-engine typed func to a go-kit typed var (and vice versa)
	// confirms they are the same underlying type (= alias, not new type).
	var engObs ModelFilterObserver = func(_ ModelFilterEvent) {}
	kitObs := kitllm.ModelFilterObserver(engObs) // would not compile if types diverged
	_ = kitObs
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
