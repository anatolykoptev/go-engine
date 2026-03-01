package text

import "testing"

func TestDetectQueryType(t *testing.T) {
	tests := []struct {
		query string
		want  QueryType
	}{
		// Fact
		{"сколько стоит биткоин", QtFact},
		{"what is the population of Japan", QtFact},
		{"price of gold", QtFact},
		{"кто такой Илон Маск", QtFact},
		// Comparison
		{"React vs Vue", QtComparison},
		{"что лучше Go или Rust", QtComparison},
		{"compare PostgreSQL and MySQL", QtComparison},
		// List
		{"лучшие фреймворки для Go", QtList},
		{"top 10 best databases", QtList},
		{"list all alternatives to Redis", QtList},
		// HowTo
		{"как настроить nginx", QtHowTo},
		{"how to install docker", QtHowTo},
		{"step by step guide to kubernetes", QtHowTo},
		// General
		{"golang concurrency patterns", QtGeneral},
		{"latest news about AI", QtGeneral},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := DetectQueryType(tt.query)
			if got != tt.want {
				t.Errorf("DetectQueryType(%q) = %d, want %d", tt.query, got, tt.want)
			}
		})
	}
}

func TestDetectQueryDomain(t *testing.T) {
	tests := []struct {
		query string
		want  QueryDomain
	}{
		// WordPress
		{"wordpress add_action hook", QdWordPress},
		{"wp_enqueue_script example", QdWordPress},
		{"gutenberg block tutorial", QdWordPress},
		// Claude Code
		{"claude code plugin development", QdClaudeCode},
		{"how to use claude.md", QdClaudeCode},
		// GitHub repo
		{"best library for websockets", QdGitHubRepo},
		{"go module for JWT auth", QdGitHubRepo},
		{"alternatives to express", QdGitHubRepo},
		// HuggingFace
		{"huggingface model for text classification", QdHuggingFace},
		{"best model for speech recognition", QdHuggingFace},
		{"gguf model for local llm", QdHuggingFace},
		// Library docs (via ExtractLibraryName)
		{"react hooks tutorial", QdLibDocs},
		{"fastapi dependency injection", QdLibDocs},
		// General
		{"latest tech news", QdGeneral},
		{"weather in Moscow", QdGeneral},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := DetectQueryDomain(tt.query)
			if got != tt.want {
				t.Errorf("DetectQueryDomain(%q) = %d, want %d", tt.query, got, tt.want)
			}
		})
	}
}

func TestExtractLibraryName(t *testing.T) {
	tests := []struct {
		query string
		want  string
	}{
		{"next.js routing docs", "next.js"},
		{"how to use prisma with typescript", "prisma"},
		{"tailwind responsive design", "tailwindcss"},
		{"gin-gonic middleware example", "gin-gonic"},
		{"pytorch training loop", "pytorch"},
		{"unknown library xyz", ""},
		{"just a regular query", ""},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := ExtractLibraryName(tt.query)
			if got != tt.want {
				t.Errorf("ExtractLibraryName(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}
