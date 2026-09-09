package view

import (
	"context"
	"testing"

	"github.com/clownware/go-performance-starter/internal/webutil"
)

// TestCanonicalURL pins how the layout derives <link rel="canonical"> and
// og:url: only from the configured public origin plus the request path that
// Render stashes in the context — never from the Host header (which a client
// controls) and never with a query string (one canonical per page).
func TestCanonicalURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		path string // "" leaves the request path out of the context
		want string
	}{
		{name: "no public origin configured means no canonical", base: "", path: "/patterns", want: ""},
		{name: "origin plus request path", base: "https://demo.example.com", path: "/patterns", want: "https://demo.example.com/patterns"},
		{name: "root path keeps its slash", base: "https://demo.example.com", path: "/", want: "https://demo.example.com/"},
		{name: "no request path in context means no canonical", base: "https://demo.example.com", path: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetPublicBaseURL(tt.base)
			t.Cleanup(func() { SetPublicBaseURL("") })
			ctx := context.Background()
			if tt.path != "" {
				ctx = webutil.WithRequestPath(ctx, tt.path)
			}
			if got := CanonicalURL(ctx); got != tt.want {
				t.Errorf("CanonicalURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAbsoluteURL covers the og:image case: social crawlers require an
// absolute image URL, so without a public origin the tag is omitted.
func TestAbsoluteURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		path string
		want string
	}{
		{name: "no origin yields empty", base: "", path: "/static/img/og.png", want: ""},
		{name: "origin joined to an absolute path", base: "https://demo.example.com", path: "/static/img/og.png", want: "https://demo.example.com/static/img/og.png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetPublicBaseURL(tt.base)
			t.Cleanup(func() { SetPublicBaseURL("") })
			if got := AbsoluteURL(tt.path); got != tt.want {
				t.Errorf("AbsoluteURL(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// TestBasePropsDescription pins the meta-description fallback: every page
// gets a real sentence, and a page-specific one wins when set.
func TestBasePropsDescription(t *testing.T) {
	if got := NewBaseProps("Terms").MetaDescription(); got != DefaultDescription {
		t.Errorf("default MetaDescription() = %q, want %q", got, DefaultDescription)
	}
	p := NewBaseProps("Patterns")
	p.Description = "Every HTMX pattern, live."
	if got := p.MetaDescription(); got != "Every HTMX pattern, live." {
		t.Errorf("custom MetaDescription() = %q", got)
	}
}
