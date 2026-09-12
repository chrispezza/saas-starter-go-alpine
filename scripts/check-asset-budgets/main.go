// Command check-asset-budgets enforces the ADR-000 frontend budgets against
// the assets the base layout actually ships (internal/view/layouts/base.templ).
// Sizes are measured gzipped — what crosses the wire — so the budgets AND the
// file lists in internal/performance are the single source of truth (the
// runtime observer measures the same files, ADR-034). Run from the repo root.
package main

import (
	"fmt"
	"os"

	"github.com/clownware/go-performance-starter/internal/performance"
)

var checks = []struct {
	name   string
	budget int64
	files  []string
}{
	{
		name:   "JavaScript Bundle",
		budget: performance.MaxJavaScriptSize,
		files:  performance.ShippedAssetPaths("web/static", performance.ShippedJS),
	},
	{
		name:   "CSS Bundle",
		budget: performance.MaxCSSSize,
		files:  performance.ShippedAssetPaths("web/static", performance.ShippedCSS),
	},
}

func main() {
	failed := false
	for _, c := range checks {
		total, err := performance.GzippedTotal(c.files...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ %s: %v\n", c.name, err)
			failed = true
			continue
		}
		if err := performance.CheckGzippedAssetSize(c.name, c.budget, c.files...); err != nil {
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			failed = true
			continue
		}
		fmt.Printf("✅ %s check passed: %d bytes gzipped (budget: %d)\n", c.name, total, c.budget)
	}
	if failed {
		os.Exit(1)
	}
}
