package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-engine/sources"
)

// mockResponse is a local OpenAI-compatible response for test servers.
type mockResponse struct {
	Choices []mockChoice `json:"choices"`
}

type mockChoice struct {
	Message mockMessage `json:"message"`
}

type mockMessage struct {
	Content string `json:"content"`
}

// mockLLMServer returns a test server that echoes the given response as an
// OpenAI-compatible chat completion.
func mockLLMServer(t *testing.T, response string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := mockResponse{
			Choices: []mockChoice{
				{Message: mockMessage{Content: response}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestNewClient_Defaults(t *testing.T) {
	c := New()
	if c.temperature != defaultTemperature {
		t.Errorf("temperature = %v, want %v", c.temperature, defaultTemperature)
	}
	if c.maxTokens != defaultMaxTokens {
		t.Errorf("maxTokens = %d, want %d", c.maxTokens, defaultMaxTokens)
	}
}

func TestNewClient_WithOptions(t *testing.T) {
	c := New(
		WithAPIBase("http://example.com/v1"),
		WithAPIKey("key1"),
		WithModel("gpt-4"),
		WithTemperature(0.5),
		WithMaxTokens(500),
	)
	if c.temperature != 0.5 {
		t.Errorf("temperature = %v", c.temperature)
	}
	if c.maxTokens != 500 {
		t.Errorf("maxTokens = %d", c.maxTokens)
	}
}

func TestComplete_Success(t *testing.T) {
	srv := mockLLMServer(t, "hello world")
	defer srv.Close()

	c := New(WithAPIBase(srv.URL), WithAPIKey("test-key"), WithModel("test"))
	got, err := c.Complete(context.Background(), "test prompt")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestComplete_StripsMarkdownFence(t *testing.T) {
	srv := mockLLMServer(t, "```json\n{\"answer\": \"test\"}\n```")
	defer srv.Close()

	c := New(WithAPIBase(srv.URL), WithAPIKey("test-key"), WithModel("test"))
	got, err := c.Complete(context.Background(), "test prompt")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"answer": "test"}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestComplete_KeyRotation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "Bearer bad-key" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := mockResponse{
			Choices: []mockChoice{
				{Message: mockMessage{Content: "rotated-success"}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New(
		WithAPIBase(srv.URL),
		WithAPIKey("bad-key"),
		WithAPIKeyFallbacks([]string{"good-key"}),
		WithModel("test"),
	)
	got, err := c.Complete(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if got != "rotated-success" {
		t.Errorf("got %q, want %q", got, "rotated-success")
	}
}

func TestRewriteQuery_Success(t *testing.T) {
	srv := mockLLMServer(t, "golang kubernetes deployment")
	defer srv.Close()

	c := New(WithAPIBase(srv.URL), WithAPIKey("key"), WithModel("test"))
	got := c.RewriteQuery(context.Background(), "как задеплоить go в k8s?")
	if got != "golang kubernetes deployment" {
		t.Errorf("got %q", got)
	}
}

func TestRewriteQuery_FallbackOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "error", http.StatusForbidden)
	}))
	defer srv.Close()

	c := New(WithAPIBase(srv.URL), WithAPIKey("key"), WithModel("test"))
	original := "test query"
	got := c.RewriteQuery(context.Background(), original)
	if got != original {
		t.Errorf("got %q, want original %q", got, original)
	}
}

func TestRewriteQuery_RejectMultiline(t *testing.T) {
	srv := mockLLMServer(t, "line1\nline2")
	defer srv.Close()

	c := New(WithAPIBase(srv.URL), WithAPIKey("key"), WithModel("test"))
	original := "test query"
	got := c.RewriteQuery(context.Background(), original)
	if got != original {
		t.Errorf("got %q, want original %q", got, original)
	}
}

func TestExpandWebSearchQueries(t *testing.T) {
	srv := mockLLMServer(t, `["query1", "query2", "query3"]`)
	defer srv.Close()

	c := New(WithAPIBase(srv.URL), WithAPIKey("key"), WithModel("test"))
	got, err := c.ExpandWebSearchQueries(context.Background(), "golang http", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("got %d variants, want 3", len(got))
	}
}

func TestExpandSearchQueries_TruncatesToN(t *testing.T) {
	srv := mockLLMServer(t, `["q1", "q2", "q3", "q4", "q5"]`)
	defer srv.Close()

	c := New(WithAPIBase(srv.URL), WithAPIKey("key"), WithModel("test"))
	got, err := c.ExpandSearchQueries(context.Background(), "test", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d, want 2", len(got))
	}
}

func TestBuildSourcesText_Snippets(t *testing.T) {
	results := []sources.Result{
		{Title: "Title1", URL: "http://a.com", Content: "snippet1"},
		{Title: "Title2", URL: "http://b.com", Content: "snippet2"},
	}
	got := BuildSourcesText(results, nil, 1000)
	if !strings.Contains(got, "[1] Title1") {
		t.Errorf("missing [1] Title1 in: %s", got)
	}
	if !strings.Contains(got, "Snippet: snippet1") {
		t.Errorf("missing Snippet: snippet1 in: %s", got)
	}
}

func TestBuildSourcesText_WithContents(t *testing.T) {
	results := []sources.Result{
		{Title: "Title1", URL: "http://a.com", Content: "snippet1"},
	}
	contents := map[string]string{"http://a.com": "full content here"}
	got := BuildSourcesText(results, contents, 1000)
	if !strings.Contains(got, "Content: full content here") {
		t.Errorf("should include content: %s", got)
	}
	if strings.Contains(got, "Snippet:") {
		t.Error("should NOT include snippet when content is present")
	}
}

func TestBuildSourcesText_TruncatesContent(t *testing.T) {
	results := []sources.Result{
		{Title: "T", URL: "http://a.com"},
	}
	contents := map[string]string{"http://a.com": "abcdefghij"}
	got := BuildSourcesText(results, contents, 5)
	if !strings.Contains(got, "abcde...") {
		t.Errorf("should truncate content: %s", got)
	}
}

func TestSummarizeWithInstruction(t *testing.T) {
	resp := `{"answer": "Test answer.", "facts": [{"point": "Fact one.", "sources": [1]}]}`
	srv := mockLLMServer(t, resp)
	defer srv.Close()

	c := New(WithAPIBase(srv.URL), WithAPIKey("key"), WithModel("test"))
	results := []sources.Result{{Title: "T", URL: "http://a.com", Content: "c"}}
	got, err := c.SummarizeWithInstruction(context.Background(), "q", "instr", 1000, results, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Answer != "Test answer." {
		t.Errorf("answer = %q", got.Answer)
	}
	if len(got.Facts) != 1 {
		t.Errorf("facts count = %d, want 1", len(got.Facts))
	}
}

func TestSummarizeDeep(t *testing.T) {
	resp := `{"answer": "Deep answer.", "facts": [{"point": "F1.", "sources": [1]}, {"point": "F2.", "sources": [2]}]}`
	srv := mockLLMServer(t, resp)
	defer srv.Close()

	c := New(WithAPIBase(srv.URL), WithAPIKey("key"), WithModel("test"))
	results := []sources.Result{{Title: "T", URL: "http://a.com", Content: "c"}}
	got, err := c.SummarizeDeep(context.Background(), "q", "instr", 1000, results, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Answer != "Deep answer." {
		t.Errorf("answer = %q", got.Answer)
	}
	if len(got.Facts) != 2 {
		t.Errorf("facts = %d, want 2", len(got.Facts))
	}
}

func TestSummarize_MalformedJSON(t *testing.T) {
	srv := mockLLMServer(t, `{"answer": "partial result", malformed`)
	defer srv.Close()

	c := New(WithAPIBase(srv.URL), WithAPIKey("key"), WithModel("test"))
	got, err := c.SummarizeWithInstruction(context.Background(), "q", "", 1000, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Answer != "partial result" {
		t.Errorf("should extract answer from malformed JSON, got %q", got.Answer)
	}
}

func TestSummarizeToJSON(t *testing.T) {
	type custom struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	resp := `{"name": "test", "count": 42}`
	srv := mockLLMServer(t, resp)
	defer srv.Close()

	c := New(WithAPIBase(srv.URL), WithAPIKey("key"), WithModel("test"))
	results := []sources.Result{{Title: "T", URL: "http://a.com"}}
	got, raw, err := SummarizeToJSON[custom](context.Background(), c, "q", "instr", 1000, results, nil)
	if err != nil {
		t.Fatal(err)
	}
	if raw != "" {
		t.Errorf("raw should be empty on success, got %q", raw)
	}
	if got.Name != "test" || got.Count != 42 {
		t.Errorf("parsed = %+v", got)
	}
}

func TestSummarizeToJSON_ParseFailure(t *testing.T) {
	srv := mockLLMServer(t, "not json at all")
	defer srv.Close()

	c := New(WithAPIBase(srv.URL), WithAPIKey("key"), WithModel("test"))
	type custom struct{ Name string }
	got, raw, err := SummarizeToJSON[custom](context.Background(), c, "q", "instr", 1000, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("should be nil on parse failure")
	}
	if raw != "not json at all" {
		t.Errorf("raw = %q", raw)
	}
}

func TestCompleteMultimodal_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var body struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Multimodal requests have content as array (text + image_url parts).
		if len(body.Messages) == 0 {
			http.Error(w, "no messages", http.StatusBadRequest)
			return
		}
		var parts []map[string]any
		if err := json.Unmarshal(body.Messages[0].Content, &parts); err != nil {
			t.Errorf("content should be array of parts: %v", err)
		}
		if len(parts) != 2 {
			t.Errorf("expected 2 content parts (text + image_url), got %d", len(parts))
		}

		w.Header().Set("Content-Type", "application/json")
		resp := mockResponse{
			Choices: []mockChoice{
				{Message: mockMessage{Content: "saw the image"}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New(WithAPIBase(srv.URL), WithAPIKey("test-key"), WithModel("test"))
	got, err := c.CompleteMultimodal(context.Background(), "describe this", []ImagePart{
		{URL: "https://example.com/img.png", MIMEType: "image/png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "saw the image" {
		t.Errorf("got %q, want %q", got, "saw the image")
	}
}

func TestCompleteMultimodal_StripsFences(t *testing.T) {
	srv := mockLLMServer(t, "```json\n{\"description\": \"a cat\"}\n```")
	defer srv.Close()

	c := New(WithAPIBase(srv.URL), WithAPIKey("test-key"), WithModel("test"))
	got, err := c.CompleteMultimodal(context.Background(), "describe", []ImagePart{
		{URL: "https://example.com/cat.jpg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"description": "a cat"}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractJSONAnswer(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"valid", `{"answer": "hello world", "facts": []}`, "hello world"},
		{"escaped_quote", `{"answer": "hello \"world\""}`, `hello "world"`},
		{"escaped_newline", `{"answer": "line1\nline2"}`, "line1\nline2"},
		{"no_answer_field", `{"data": "nope"}`, ""},
		{"empty_string", ``, ""},
		{"unclosed_quote", `{"answer": "partial`, "partial"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractJSONAnswer(tt.raw)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
