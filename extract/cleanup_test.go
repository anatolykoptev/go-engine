package extract

import (
	"strings"
	"testing"
)

func TestStripScriptStyle_Basic(t *testing.T) {
	html := `<html><head><script>var x=1;</script><style>.a{}</style></head><body><p>Hello</p></body></html>`
	got := StripScriptStyle([]byte(html))
	if strings.Contains(string(got), "var x") {
		t.Error("script content should be stripped")
	}
	if strings.Contains(string(got), ".a{}") {
		t.Error("style content should be stripped")
	}
	if !strings.Contains(string(got), "Hello") {
		t.Error("body content should be preserved")
	}
}

func TestStripScriptStyle_PreservesStructure(t *testing.T) {
	html := `<html><body><p>Before</p><script type="text/javascript">alert(1);</script><p>After</p></body></html>`
	got := StripScriptStyle([]byte(html))
	if !strings.Contains(string(got), "Before") || !strings.Contains(string(got), "After") {
		t.Error("surrounding content should be preserved")
	}
	if strings.Contains(string(got), "alert") {
		t.Error("script should be stripped")
	}
}

func TestStripScriptStyle_Empty(t *testing.T) {
	got := StripScriptStyle([]byte(""))
	if len(got) != 0 {
		t.Errorf("empty input should return empty, got %d bytes", len(got))
	}
}

func TestStripScriptStyle_NoScripts(t *testing.T) {
	html := `<html><body><p>Clean HTML</p></body></html>`
	got := StripScriptStyle([]byte(html))
	if !strings.Contains(string(got), "Clean HTML") {
		t.Error("clean HTML should be unchanged")
	}
}
