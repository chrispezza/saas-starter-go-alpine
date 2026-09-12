package view

import (
	"context"

	"github.com/clownware/go-performance-starter/internal/webutil"
)

// DefaultDescription is the meta description every page falls back to when
// its handler does not set a page-specific one.
const DefaultDescription = "A server-rendered Go + HTMX starter that proves its own architecture: typed templates, RLS-scoped data access, and performance budgets enforced in CI."

// SiteName is the og:site_name shared by every page.
const SiteName = "Go Performance Starter"

// OGImagePath is the share image the layout advertises (1200×630 PNG).
const OGImagePath = "/static/img/og.png"

// publicBaseURL is the internet-facing origin (PUBLIC_BASE_URL, normalized
// without a trailing slash). Empty means unknown: absolute tags are omitted
// rather than derived from the Host header, which a client controls.
var publicBaseURL string

// SetPublicBaseURL records the configured public origin; server.New calls
// it from config so tests and cloners without the variable get no
// canonical/og:url rather than a wrong one.
func SetPublicBaseURL(base string) {
	publicBaseURL = base
}

// AbsoluteURL joins an absolute path onto the public origin, or returns ""
// when no origin is configured.
func AbsoluteURL(path string) string {
	if publicBaseURL == "" {
		return ""
	}
	return publicBaseURL + path
}

// CanonicalURL is the page's canonical location: public origin + the
// request path Render stored in the context (query strings never count —
// one canonical per page). Empty when either half is unknown.
func CanonicalURL(ctx context.Context) string {
	path := webutil.RequestPathFromContext(ctx)
	if path == "" {
		return ""
	}
	return AbsoluteURL(path)
}
