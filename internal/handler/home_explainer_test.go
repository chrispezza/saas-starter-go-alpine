package handler

import (
	"strings"
	"testing"
	"time"

	"github.com/clownware/go-performance-starter/internal/performance"
	"github.com/clownware/go-performance-starter/internal/view/pages"
)

// TestExplainerNodes pins the teachable spine (ADR-024 surface 1, #67): the
// explainer narrates a request's journey in five fixed steps, each anchored,
// linked to its ADR, backed by a real source peek, and keyed to the quiz
// topic that draws from it — so read → quiz → flashcards closes the loop.
func TestExplainerNodes(t *testing.T) {
	nodes := ExplainerNodes()

	wantOrder := []struct{ anchor, topic, adr string }{
		{"routing", "routing", "ADR-014"},
		{"handlers", "handlers", "ADR-017"},
		{"database", "database", "ADR-003"},
		{"frontend", "frontend", "ADR-007"},
		{"performance", "performance", "ADR-000"},
	}
	if len(nodes) != len(wantOrder) {
		t.Fatalf("got %d nodes, want %d", len(nodes), len(wantOrder))
	}
	seenAnchors := map[string]bool{}
	for i, want := range wantOrder {
		n := nodes[i]
		if n.Step != i+1 {
			t.Errorf("node %d Step = %d, want %d", i, n.Step, i+1)
		}
		if n.Anchor != want.anchor {
			t.Errorf("node %d Anchor = %q, want %q", i, n.Anchor, want.anchor)
		}
		if seenAnchors[n.Anchor] {
			t.Errorf("duplicate anchor %q", n.Anchor)
		}
		seenAnchors[n.Anchor] = true
		if n.QuizTopic != want.topic {
			t.Errorf("node %d QuizTopic = %q, want %q (must match the seeded quiz_questions.topic)", i, n.QuizTopic, want.topic)
		}
		if !strings.Contains(n.ADR.Label, want.adr) || !strings.Contains(n.ADR.Href, want.adr) {
			t.Errorf("node %d ADR = %+v, want it to name %s", i, n.ADR, want.adr)
		}
		if n.Title == "" || n.Summary == "" || len(n.Prose) == 0 {
			t.Errorf("node %d is missing title/summary/prose: %+v", i, n)
		}
		if n.Source.File == "" || !strings.Contains(n.Source.Snippet, "\n") {
			t.Errorf("node %d needs a source peek with a file path and a multi-line snippet", i)
		}
		if !strings.HasPrefix(n.Source.File, "internal/") && !strings.HasPrefix(n.Source.File, "sql/") && !strings.HasPrefix(n.Source.File, "migrations/") {
			t.Errorf("node %d Source.File = %q should point into the repo tree", i, n.Source.File)
		}
	}
}

// TestPerfBudgetStats pins the "render from constants, never hardcode" rule
// (design brief §Live performance stats): every stat is derived from
// internal/performance at request time, so a budget change in ADR-000's
// constants changes the landing page without anyone editing a template.
func TestPerfBudgetStats(t *testing.T) {
	stats := PerfBudgetStats(performance.Snapshot{})

	want := map[string]string{
		"P50 response":  performance.MaxP50ResponseTime.String(),
		"P95 response":  performance.MaxP95ResponseTime.String(),
		"P99 response":  performance.MaxP99ResponseTime.String(),
		"Binary size":   "20 MB",
		"Memory":        "128 MB",
		"Startup":       performance.MaxStartupTime.String(),
		"JavaScript":    "50 KB",
		"CSS":           "30 KB",
		"Total page":    "500 KB",
		"Docker image":  "", // not a performance constant — must NOT appear
		"Peak memory":   "256 MB",
		"Response time": "", // generic label must not appear; the three percentiles do
	}
	got := map[string]string{}
	for _, s := range stats {
		if s.Label == "" || s.Budget == "" {
			t.Errorf("stat with empty label/budget: %+v", s)
		}
		got[s.Label] = s.Budget
	}
	for label, value := range want {
		if value == "" {
			if _, ok := got[label]; ok {
				t.Errorf("stat %q should not be rendered (no constant backs it)", label)
			}
			continue
		}
		if got[label] != value {
			t.Errorf("stat %q = %q, want %q (from internal/performance)", label, got[label], value)
		}
	}
	if len(stats) != 10 {
		t.Errorf("got %d stats, want 10 (three latency percentiles, four resource, three frontend)", len(stats))
	}
}

// TestFormatBudgetBytes pins the human units the stats use.
func TestFormatBudgetBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{512, "512 B"},
		{50 * 1024, "50 KB"},
		{30 * 1024, "30 KB"},
		{500 * 1024, "500 KB"},
		{20 * 1024 * 1024, "20 MB"},
		{128 * 1024 * 1024, "128 MB"},
		{1536, "1.5 KB"},
	}
	for _, tt := range tests {
		if got := formatBudgetBytes(tt.in); got != tt.want {
			t.Errorf("formatBudgetBytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestPerfBudgetStats_Observed pins the observed-vs-budget grid (ADR-034):
// every row states its ADR-000 class, an unmeasured value renders as
// unmeasured (never as a passing zero), and pass/fail is decided against the
// constants in internal/performance — the same values task ci enforces.
func TestPerfBudgetStats_Observed(t *testing.T) {
	const mb = 1024 * 1024
	healthy := performance.Snapshot{
		Samples: 12, P50: 12 * time.Millisecond, P95: 40 * time.Millisecond, P99: 80 * time.Millisecond,
		HeapInUse: 20 * mb, Sys: 60 * mb, PeakSys: 70 * mb,
		Startup: 120 * time.Millisecond, BinarySize: 15 * mb,
		JSGzipped: 32771, CSSGzipped: 7758,
	}
	breached := healthy
	breached.P95 = 150 * time.Millisecond
	breached.Sys = 200 * mb
	breached.PeakSys = 300 * mb

	type want struct {
		status   string
		class    string
		observed string // substring; "" skips
	}
	tests := []struct {
		name string
		snap performance.Snapshot
		rows map[string]want // keyed by Label
	}{
		{
			name: "nothing measured yet",
			snap: performance.Snapshot{},
			rows: map[string]want{
				"P50 response": {status: "unmeasured", class: "Monitored", observed: "—"},
				"P95 response": {status: "unmeasured", class: "Monitored"},
				"Startup":      {status: "unmeasured", class: "Monitored"},
				"Binary size":  {status: "unmeasured", class: "Enforced"},
				"JavaScript":   {status: "unmeasured", class: "Enforced"},
				"Total page":   {status: "unmeasured", class: "Aspirational", observed: "—"},
			},
		},
		{
			name: "healthy instance passes every measured budget",
			snap: healthy,
			rows: map[string]want{
				"P50 response": {status: "pass", class: "Monitored", observed: "12ms"},
				"P95 response": {status: "pass", class: "Monitored", observed: "40ms"},
				"P99 response": {status: "pass", class: "Monitored", observed: "80ms"},
				"Binary size":  {status: "pass", class: "Enforced", observed: "15 MB"},
				"Memory":       {status: "pass", class: "Monitored", observed: "60 MB"},
				"Peak memory":  {status: "pass", class: "Monitored", observed: "70 MB"},
				"Startup":      {status: "pass", class: "Monitored", observed: "120ms"},
				"JavaScript":   {status: "pass", class: "Enforced", observed: "32.0 KB"},
				"CSS":          {status: "pass", class: "Enforced", observed: "7.6 KB"},
				"Total page":   {status: "unmeasured", class: "Aspirational"},
			},
		},
		{
			name: "breaches are reported, not hidden",
			snap: breached,
			rows: map[string]want{
				"P95 response": {status: "fail", class: "Monitored", observed: "150ms"},
				"P50 response": {status: "pass", class: "Monitored"},
				"Memory":       {status: "fail", class: "Monitored", observed: "200 MB"},
				"Peak memory":  {status: "fail", class: "Monitored", observed: "300 MB"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := PerfBudgetStats(tt.snap)
			if len(stats) != 10 {
				t.Fatalf("PerfBudgetStats returned %d rows, want 10", len(stats))
			}
			byLabel := map[string]pages.BudgetStat{}
			for _, s := range stats {
				byLabel[s.Label] = s
				if s.Budget == "" || s.Class == "" || s.Status == "" || s.Observed == "" {
					t.Errorf("row %q incomplete: %+v", s.Label, s)
				}
			}
			for label, w := range tt.rows {
				row, ok := byLabel[label]
				if !ok {
					t.Errorf("missing row %q", label)
					continue
				}
				if row.Status != w.status {
					t.Errorf("%s: Status = %q, want %q (observed %q)", label, row.Status, w.status, row.Observed)
				}
				if row.Class != w.class {
					t.Errorf("%s: Class = %q, want %q", label, row.Class, w.class)
				}
				if w.observed != "" && !strings.Contains(row.Observed, w.observed) {
					t.Errorf("%s: Observed = %q, want it to contain %q", label, row.Observed, w.observed)
				}
			}
		})
	}
}

// TestFormatObservedDuration pins the observed-column units: tenth-of-a-
// millisecond rounding, and a floor label instead of "0s" for readings the
// resolution cannot express (static files serve in tens of microseconds).
func TestFormatObservedDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "<0.1ms"},
		{40 * time.Microsecond, "<0.1ms"},
		{100 * time.Microsecond, "100µs"},
		{12340 * time.Microsecond, "12.3ms"},
		{12 * time.Millisecond, "12ms"},
		{1500 * time.Millisecond, "1.5s"},
	}
	for _, tt := range tests {
		if got := formatObservedDuration(tt.in); got != tt.want {
			t.Errorf("formatObservedDuration(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
