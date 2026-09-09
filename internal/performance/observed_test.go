package performance

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLatencyRecorder_Percentiles pins the nearest-rank percentiles over a
// bounded ring: an empty recorder reports nothing (the UI must say
// "unmeasured", not "0ms"), a full sequence yields the textbook ranks, and
// once the ring wraps only the most recent window counts.
func TestLatencyRecorder_Percentiles(t *testing.T) {
	tests := []struct {
		name    string
		size    int
		samples []time.Duration
		want    LatencyPercentiles
	}{
		{
			name: "empty recorder reports zero samples",
			size: 8,
			want: LatencyPercentiles{},
		},
		{
			name:    "single sample is every percentile",
			size:    8,
			samples: []time.Duration{7 * time.Millisecond},
			want:    LatencyPercentiles{P50: 7 * time.Millisecond, P95: 7 * time.Millisecond, P99: 7 * time.Millisecond, Samples: 1},
		},
		{
			name:    "1..100ms yields nearest-rank 50/95/99",
			size:    100,
			samples: ramp(100),
			want:    LatencyPercentiles{P50: 50 * time.Millisecond, P95: 95 * time.Millisecond, P99: 99 * time.Millisecond, Samples: 100},
		},
		{
			name:    "ring keeps only the newest window",
			size:    10,
			samples: ramp(100), // last ten are 91..100ms
			want:    LatencyPercentiles{P50: 95 * time.Millisecond, P95: 100 * time.Millisecond, P99: 100 * time.Millisecond, Samples: 10},
		},
		{
			name:    "unsorted input is sorted before ranking",
			size:    8,
			samples: []time.Duration{9 * time.Millisecond, 1 * time.Millisecond, 5 * time.Millisecond, 3 * time.Millisecond},
			want:    LatencyPercentiles{P50: 3 * time.Millisecond, P95: 9 * time.Millisecond, P99: 9 * time.Millisecond, Samples: 4},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewLatencyRecorder(tt.size)
			for _, d := range tt.samples {
				r.Record(d)
			}
			if got := r.Percentiles(); got != tt.want {
				t.Errorf("Percentiles() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func ramp(n int) []time.Duration {
	out := make([]time.Duration, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, time.Duration(i)*time.Millisecond)
	}
	return out
}

// TestObserver_Snapshot covers what the process can measure about itself:
// shipped-asset sizes from the static dir (gzipped, same lists CI gates),
// a startup duration recorded once, a memory high-water mark that never
// falls, and honest zeros when a measurement is unavailable.
func TestObserver_Snapshot(t *testing.T) {
	staticDir := t.TempDir()
	mustWrite(t, filepath.Join(staticDir, "js", "htmx.min.js"), 4000)
	mustWrite(t, filepath.Join(staticDir, "js", "alpine.min.js"), 3000)
	mustWrite(t, filepath.Join(staticDir, "js", "app.js"), 500)
	mustWrite(t, filepath.Join(staticDir, "css", "app.css"), 2500)

	o := NewObserver(staticDir)
	o.RecordStartup(120 * time.Millisecond)
	o.RecordStartup(900 * time.Millisecond) // second call is ignored: startup happens once
	o.RecordLatency(20 * time.Millisecond)
	o.RecordLatency(40 * time.Millisecond)

	snap := o.Snapshot()
	if snap.Startup != 120*time.Millisecond {
		t.Errorf("Startup = %v, want 120ms (first RecordStartup wins)", snap.Startup)
	}
	if snap.Samples != 2 || snap.P50 != 20*time.Millisecond || snap.P99 != 40*time.Millisecond {
		t.Errorf("latency snapshot = p50 %v p99 %v n=%d, want p50 20ms p99 40ms n=2", snap.P50, snap.P99, snap.Samples)
	}
	if snap.JSGzipped <= 0 || snap.JSGzipped >= 7500 {
		t.Errorf("JSGzipped = %d, want a gzipped total below the 7500 raw bytes", snap.JSGzipped)
	}
	if snap.CSSGzipped <= 0 || snap.CSSGzipped >= 2500 {
		t.Errorf("CSSGzipped = %d, want a gzipped total below the 2500 raw bytes", snap.CSSGzipped)
	}
	if snap.BinarySize <= 0 {
		t.Errorf("BinarySize = %d, want the running executable's size", snap.BinarySize)
	}
	if snap.Sys == 0 || snap.HeapInUse == 0 {
		t.Errorf("memory snapshot = sys %d heap %d, want non-zero", snap.Sys, snap.HeapInUse)
	}
	if snap.PeakSys < snap.Sys {
		t.Errorf("PeakSys = %d < Sys = %d; the high-water mark must never trail the current value", snap.PeakSys, snap.Sys)
	}
	if snap.Uptime <= 0 || snap.Goroutines <= 0 {
		t.Errorf("Uptime = %v, Goroutines = %d, want positive", snap.Uptime, snap.Goroutines)
	}

	// Missing assets: zeros, no panic, everything else still measured.
	empty := NewObserver(filepath.Join(staticDir, "nope"))
	es := empty.Snapshot()
	if es.JSGzipped != 0 || es.CSSGzipped != 0 {
		t.Errorf("missing static dir: JS %d CSS %d, want 0 (unmeasured)", es.JSGzipped, es.CSSGzipped)
	}
	if es.Startup != 0 || es.Samples != 0 {
		t.Errorf("fresh observer: Startup %v Samples %d, want zero (unmeasured)", es.Startup, es.Samples)
	}
}

func mustWrite(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestShippedAssetPaths pins that the runtime observer and the CI gate
// measure the same files: one list, joined onto whichever static root the
// caller has.
func TestShippedAssetPaths(t *testing.T) {
	got := ShippedAssetPaths("web/static", ShippedJS)
	want := []string{"web/static/js/htmx.min.js", "web/static/js/alpine.min.js", "web/static/js/app.js"}
	if len(got) != len(want) {
		t.Fatalf("ShippedAssetPaths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ShippedAssetPaths[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
