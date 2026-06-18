package search

import (
	"testing"

	"github.com/anatolykoptev/go-engine/metrics"
)

// TestRecordSourceResult verifies the per-source fan-out outcome counter is
// encoded as name{source=...,outcome=...} so the go-kit/metrics Prometheus
// bridge surfaces a per-source failure rate. Regression guard for the
// silent-redundancy gap: a source failing 100% (e.g. yep on the deprecated
// endpoint) was invisible because a sibling source covered the result set.
func TestRecordSourceResult(t *testing.T) {
	m := metrics.New()

	recordSourceResult(m, "yep", "fail")
	recordSourceResult(m, "yep", "fail")
	recordSourceResult(m, "yep", "ok")
	recordSourceResult(m, "yandex", "ok")

	snap := m.Snapshot()

	if got := snap["go_search_source_result_total{source=yep,outcome=fail}"]; got != 2 {
		t.Errorf("yep fail = %d, want 2 (snapshot: %v)", got, snap)
	}
	if got := snap["go_search_source_result_total{source=yep,outcome=ok}"]; got != 1 {
		t.Errorf("yep ok = %d, want 1", got)
	}
	if got := snap["go_search_source_result_total{source=yandex,outcome=ok}"]; got != 1 {
		t.Errorf("yandex ok = %d, want 1", got)
	}
}

// TestRecordSourceResult_NilSafe guards the nil-registry path: SearchDirect may
// run with cfg.Metrics == nil.
func TestRecordSourceResult_NilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("recordSourceResult panicked on nil registry: %v", r)
		}
	}()
	recordSourceResult(nil, "yep", "fail")
}
