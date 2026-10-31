package srcprobe

import "testing"

func TestProbeInventoryMetrics(t *testing.T) {
	srv := fixture(t)
	js := run(t, Options{Target: srv.URL + "/", Kind: KindJS})
	if js.Metrics["js_discovered"] != 1 || js.Metrics["js_fetched"] != 1 {
		t.Fatalf("js inventory=%v", js.Metrics)
	}
	if js.Metrics["sourcemap_refs"] != 1 || js.Metrics["sourcemap"] != 1 {
		t.Fatalf("sourcemap inventory=%v", js.Metrics)
	}
	methods := run(t, Options{Target: srv.URL, Kind: KindMethods})
	if methods.Metrics["safe_methods_available"] != 3 {
		t.Fatalf("safe method set=%v", methods.Metrics)
	}
	non404 := run(t, Options{Target: srv.URL, Kind: KindNon404, Retest: []string{srv.URL + "/api/users/1", srv.URL + "/admin"}})
	if non404.Metrics["non404_routes"] != 2 || non404.Metrics["retested_routes"] != 2 {
		t.Fatalf("non404 inventory=%v", non404.Metrics)
	}
}
