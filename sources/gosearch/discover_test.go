package gosearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// researchFixtureSSE returns an SSE stream wrapping a research tool response
// containing the given source URLs.
func researchFixtureSSE(sources []struct {
	index      int
	title, url string
}) string {
	// Build pipeline.SearchOutput-compatible JSON
	type srcItem struct {
		Index int    `json:"index"`
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	items := make([]srcItem, len(sources))
	for i, s := range sources {
		items[i] = srcItem{Index: s.index, Title: s.title, URL: s.url}
	}
	inner, _ := json.Marshal(map[string]any{
		"query":   "тестовый запрос",
		"answer":  "",
		"facts":   []any{},
		"sources": items,
	})
	outer, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result": map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": string(inner)},
			},
		},
	})
	return "event: message\ndata: " + string(outer) + "\n\n"
}

func TestDiscover_ParsesSourceURLs(t *testing.T) {
	sources := []struct {
		index      int
		title, url string
	}{
		{1, "Лучшие рестораны СПб - Sobaka.ru", "https://www.sobaka.ru/spb/restaurants/"},
		{2, "ТОП кафе Санкт-Петербург - Tripadvisor", "https://www.tripadvisor.ru/Restaurants-g298507.html"},
		{3, "Рестораны СПб - Kudago", "https://kudago.com/spb/restaurants/"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		params := body["params"].(map[string]any)
		if params["name"] != "research" {
			http.Error(w, "wrong tool name", http.StatusBadRequest)
			return
		}
		args := params["arguments"].(map[string]any)
		if args["source"] != "piternow" {
			http.Error(w, "wrong source", http.StatusBadRequest)
			return
		}
		if args["depth"] != "fast" {
			http.Error(w, "wrong depth", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, researchFixtureSSE(sources))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	c.ok.Store(true)

	results, err := c.Discover(context.Background(), "рестораны Санкт-Петербург",
		DiscoverOpts{Source: "piternow", Depth: "fast"})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(results) != len(sources) {
		t.Fatalf("expected %d results, got %d", len(sources), len(results))
	}
	for i, want := range sources {
		if results[i].URL != want.url {
			t.Errorf("[%d] URL: got %q, want %q", i, results[i].URL, want.url)
		}
		if results[i].Title != want.title {
			t.Errorf("[%d] Title: got %q, want %q", i, results[i].Title, want.title)
		}
	}
}

func TestDiscover_EmptySources_ReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner, _ := json.Marshal(map[string]any{
			"query": "test", "answer": "", "facts": []any{}, "sources": []any{},
		})
		outer, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": string(inner)}},
			},
		})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", string(outer))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	c.ok.Store(true)

	results, err := c.Discover(context.Background(), "тест", DiscoverOpts{Source: "piternow", Depth: "fast"})
	if err != nil {
		t.Fatalf("expected nil error on empty sources, got: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results on empty sources, got %+v", results)
	}
}

func TestDiscover_RPCError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outer, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"error": map[string]any{"code": -32000, "message": "tool not found: research"},
		})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", string(outer))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	c.ok.Store(true)

	_, err := c.Discover(context.Background(), "тест", DiscoverOpts{})
	if err == nil {
		t.Fatal("expected error on RPC error response")
	}
	if !strings.Contains(err.Error(), "rpc error") {
		t.Errorf("expected 'rpc error' in message, got: %v", err)
	}
}

func TestDiscover_HTTPError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	c.ok.Store(true)

	_, err := c.Discover(context.Background(), "тест", DiscoverOpts{})
	if err == nil {
		t.Fatal("expected error on HTTP 500")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("expected status 500 in error, got: %v", err)
	}
}

func TestDiscover_NotConfigured_ReturnsError(t *testing.T) {
	c := NewClient("", nil)
	_, err := c.Discover(context.Background(), "тест", DiscoverOpts{})
	if err == nil {
		t.Fatal("expected error when client not configured")
	}
}

func TestDiscover_SkipsBlankURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner, _ := json.Marshal(map[string]any{
			"query": "test", "answer": "", "facts": []any{},
			"sources": []map[string]any{
				{"index": 1, "title": "Good", "url": "https://sobaka.ru/"},
				{"index": 2, "title": "Bad", "url": ""},
				{"index": 3, "title": "Also Good", "url": "https://kudago.com/"},
			},
		})
		outer, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": string(inner)}},
			},
		})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", string(outer))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	c.ok.Store(true)

	results, err := c.Discover(context.Background(), "тест", DiscoverOpts{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results (blank URL filtered), got %d", len(results))
	}
}

// TestDiscover_SendsBothAcceptTypes guards the streamable-HTTP contract: the
// go-sdk MCP server rejects POST /mcp with 400 unless Accept lists BOTH
// application/json and text/event-stream. A prior version sent only
// text/event-stream, 400ing every live research call (the mocks above did not
// enforce Accept, so the bug shipped). This mock mimics the real server-side
// enforcement so the regression cannot return silently.
func TestDiscover_SendsBothAcceptTypes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept := r.Header.Get("Accept")
		if !strings.Contains(accept, "application/json") || !strings.Contains(accept, "text/event-stream") {
			http.Error(w, "Accept must contain both application/json and text/event-stream", http.StatusBadRequest)
			return
		}
		sources := []struct {
			index      int
			title, url string
		}{
			{1, "ProDoctorov", "https://prodoctorov.ru/spb/top/chastnaya-stomatologiya/"},
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, researchFixtureSSE(sources))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	c.ok.Store(true)

	results, err := c.Discover(context.Background(), "стоматологии Санкт-Петербург",
		DiscoverOpts{Source: "piternow", Depth: "fast"})
	if err != nil {
		t.Fatalf("Discover must send both Accept types and succeed; got: %v", err)
	}
	if len(results) != 1 || results[0].URL != "https://prodoctorov.ru/spb/top/chastnaya-stomatologiya/" {
		t.Fatalf("unexpected results: %+v", results)
	}
}
