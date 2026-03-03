package text

import "testing"

func TestFilter_Interface(t *testing.T) {
	var _ Filter = NewBM25Filter(3)
}

func TestBM25Filter_TopK(t *testing.T) {
	f := NewBM25Filter(2)
	chunks := []string{
		"The cat sat on the mat",
		"The dog played in the park",
		"Go programming language tutorial",
		"The cat chased the mouse across the room",
	}
	result := f.Filter(chunks, "cat")
	if len(result) != 2 {
		t.Fatalf("results = %d, want 2", len(result))
	}
	for i, r := range result {
		if r != chunks[0] && r != chunks[3] {
			t.Errorf("result[%d] = %q, expected chunk about cats", i, r)
		}
	}
}

func TestBM25Filter_AllRelevant(t *testing.T) {
	f := NewBM25Filter(10)
	chunks := []string{"go test", "go build", "go run"}
	result := f.Filter(chunks, "go")
	if len(result) != 3 {
		t.Errorf("results = %d, want 3 (all chunks match)", len(result))
	}
}

func TestBM25Filter_NoMatch(t *testing.T) {
	f := NewBM25Filter(3)
	chunks := []string{"apple", "banana", "cherry"}
	result := f.Filter(chunks, "zebra")
	if len(result) > 3 {
		t.Errorf("results = %d, should be <= 3", len(result))
	}
}

func TestBM25Filter_Empty(t *testing.T) {
	f := NewBM25Filter(3)
	result := f.Filter(nil, "query")
	if len(result) != 0 {
		t.Errorf("results = %d, want 0 for nil chunks", len(result))
	}
	result = f.Filter([]string{"chunk"}, "")
	if len(result) != 1 {
		t.Errorf("results = %d, want 1 for empty query (return all)", len(result))
	}
}

func TestBM25Filter_MultiTermQuery(t *testing.T) {
	f := NewBM25Filter(2)
	chunks := []string{
		"go programming language",
		"python machine learning",
		"go machine learning with tensorflow",
		"rust systems programming",
	}
	result := f.Filter(chunks, "go machine learning")
	if len(result) != 2 {
		t.Fatalf("results = %d, want 2", len(result))
	}
	if result[0] != chunks[2] {
		t.Errorf("result[0] = %q, want %q (most relevant)", result[0], chunks[2])
	}
}

func TestBM25Filter_ScoringOrder(t *testing.T) {
	f := NewBM25Filter(3)
	chunks := []string{
		"the the the the",
		"golang concurrency patterns and tricks",
		"golang is great for concurrency",
	}
	result := f.Filter(chunks, "golang concurrency")
	if len(result) < 2 {
		t.Fatalf("results = %d, want >= 2", len(result))
	}
	if result[0] != chunks[1] && result[0] != chunks[2] {
		t.Errorf("result[0] = %q, expected golang chunk", result[0])
	}
}
