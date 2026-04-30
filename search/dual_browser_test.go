package search

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type stubDoer struct {
	status int
	err    error
	body   string
	called int
	gotBody string
}

func (s *stubDoer) Do(method, url string, headers map[string]string, body io.Reader) ([]byte, map[string]string, int, error) {
	s.called++
	if body != nil {
		b, _ := io.ReadAll(body)
		s.gotBody = string(b)
	}
	if s.err != nil {
		return nil, nil, 0, s.err
	}
	return []byte(s.body), nil, s.status, nil
}

func TestDualBrowser_PrimaryOK(t *testing.T) {
	primary := &stubDoer{status: 200, body: "ok"}
	fallback := &stubDoer{status: 200, body: "fb"}
	d := newDualBrowser(primary, fallback)

	data, _, status, err := d.Do("GET", "http://x", nil, nil)
	if err != nil || status != 200 || string(data) != "ok" {
		t.Fatalf("unexpected primary path: status=%d data=%q err=%v", status, data, err)
	}
	if fallback.called != 0 {
		t.Fatalf("fallback should not be called on success")
	}
}

func TestDualBrowser_FallbackOn402(t *testing.T) {
	primary := &stubDoer{status: 402, body: "Payment Required"}
	fallback := &stubDoer{status: 200, body: "fb"}
	d := newDualBrowser(primary, fallback)

	data, _, status, err := d.Do("GET", "http://x", nil, nil)
	if err != nil || status != 200 || string(data) != "fb" {
		t.Fatalf("expected fallback success: status=%d data=%q err=%v", status, data, err)
	}
	if primary.called != 1 || fallback.called != 1 {
		t.Fatalf("call counts: primary=%d fallback=%d", primary.called, fallback.called)
	}
}

func TestDualBrowser_FallbackOnNetErr(t *testing.T) {
	primary := &stubDoer{err: errors.New("dial: connection refused")}
	fallback := &stubDoer{status: 200, body: "fb"}
	d := newDualBrowser(primary, fallback)

	_, _, status, err := d.Do("GET", "http://x", nil, nil)
	if err != nil || status != 200 {
		t.Fatalf("fallback path failed: status=%d err=%v", status, err)
	}
}

func TestDualBrowser_NoFallback(t *testing.T) {
	primary := &stubDoer{status: 200, body: "ok"}
	d := newDualBrowser(primary, nil)
	if d != primary {
		t.Fatalf("expected primary returned when fallback is nil")
	}
}

func TestDualBrowser_BodyReplay(t *testing.T) {
	primary := &stubDoer{status: 402}
	fallback := &stubDoer{status: 200, body: "fb"}
	d := newDualBrowser(primary, fallback)

	_, _, _, err := d.Do("POST", "http://x", nil, strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if primary.gotBody != "payload" || fallback.gotBody != "payload" {
		t.Fatalf("body not replayed: primary=%q fallback=%q", primary.gotBody, fallback.gotBody)
	}
}

func TestDualBrowser_PassthroughOn4xxNonProxy(t *testing.T) {
	// 403/404/429 are target-side responses, not proxy failures — do not fallback.
	primary := &stubDoer{status: 403, body: "forbidden"}
	fallback := &stubDoer{status: 200, body: "fb"}
	d := newDualBrowser(primary, fallback)

	data, _, status, _ := d.Do("GET", "http://x", nil, nil)
	if status != 403 || string(data) != "forbidden" {
		t.Fatalf("expected 403 passthrough, got %d %q", status, data)
	}
	if fallback.called != 0 {
		t.Fatalf("fallback called for non-proxy 4xx")
	}
}
