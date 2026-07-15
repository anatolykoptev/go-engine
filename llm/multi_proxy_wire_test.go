package llm

import (
	"testing"

	kitllm "github.com/anatolykoptev/go-kit/llm"
)

func TestBuildProxySpecs(t *testing.T) {
	tests := []struct {
		name       string
		urls       []string
		keys       []string
		primaryKey string
		expected   []kitllm.ProxySpec
	}{
		{
			name:       "matching urls and keys",
			urls:       []string{"http://local/v1", "http://remote/v1"},
			keys:       []string{"kl", "kr"},
			primaryKey: "kp",
			expected: []kitllm.ProxySpec{
				{URL: "http://local/v1", Key: "kl"},
				{URL: "http://remote/v1", Key: "kr"},
			},
		},
		{
			name:       "keys shorter than urls defaults to primaryKey",
			urls:       []string{"http://local/v1", "http://remote/v1"},
			keys:       []string{"kl"},
			primaryKey: "kp",
			expected: []kitllm.ProxySpec{
				{URL: "http://local/v1", Key: "kl"},
				{URL: "http://remote/v1", Key: "kp"},
			},
		},
		{
			name:       "empty key at position defaults to primaryKey",
			urls:       []string{"http://local/v1", "http://remote/v1"},
			keys:       []string{"", "kr"},
			primaryKey: "kp",
			expected: []kitllm.ProxySpec{
				{URL: "http://local/v1", Key: "kp"},
				{URL: "http://remote/v1", Key: "kr"},
			},
		},
		{
			name:       "no keys at all defaults to primaryKey",
			urls:       []string{"http://local/v1", "http://remote/v1"},
			keys:       nil,
			primaryKey: "kp",
			expected: []kitllm.ProxySpec{
				{URL: "http://local/v1", Key: "kp"},
				{URL: "http://remote/v1", Key: "kp"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildProxySpecs(tt.urls, tt.keys, tt.primaryKey)
			if len(got) != len(tt.expected) {
				t.Fatalf("buildProxySpecs() len = %d, want %d", len(got), len(tt.expected))
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("buildProxySpecs()[%d] = %+v, want %+v", i, got[i], tt.expected[i])
				}
			}
		})
	}
}
