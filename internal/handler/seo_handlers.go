package handler

import (
	"encoding/xml"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Crawl policy. /learn/* is disallowed on purpose: its identity chain mints
// a real anonymous Supabase user on first touch (ADR-024), so every crawler
// visit would create an account for the reaper to clean up. The per-user
// surfaces (dashboard, profile, auth) and the /patterns fragment API are
// not pages either. Only identity-free public pages go in the sitemap.
var (
	robotsDisallow = []string{"/learn/", "/dashboard", "/profile", "/auth/", "/patterns/api/", "/first-run", "/metrics", "/health"}
	sitemapPaths   = []string{"/", "/patterns", "/terms", "/privacy"}
)

// SEORoutes registers /robots.txt and, when the public origin is known,
// /sitemap.xml (sitemap URLs must be absolute, so there is nothing honest
// to serve without it — the route stays a 404).
func SEORoutes(r chi.Router, publicBaseURL string) {
	r.Get("/robots.txt", RobotsTxt(publicBaseURL))
	if publicBaseURL != "" {
		r.Get("/sitemap.xml", Sitemap(publicBaseURL))
	}
}

// RobotsTxt serves the crawl policy; the Sitemap line appears only when the
// sitemap exists.
func RobotsTxt(publicBaseURL string) http.HandlerFunc {
	var b strings.Builder
	b.WriteString("User-agent: *\n")
	for _, p := range robotsDisallow {
		b.WriteString("Disallow: " + p + "\n")
	}
	b.WriteString("Allow: /\n")
	if publicBaseURL != "" {
		b.WriteString("Sitemap: " + publicBaseURL + "/sitemap.xml\n")
	}
	body := b.String()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write([]byte(body))
	}
}

type sitemapURLSet struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc string `xml:"loc"`
}

// Sitemap serves the public pages as absolute URLs on the configured origin.
func Sitemap(publicBaseURL string) http.HandlerFunc {
	set := sitemapURLSet{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	for _, p := range sitemapPaths {
		set.URLs = append(set.URLs, sitemapURL{Loc: publicBaseURL + p})
	}
	body, err := xml.MarshalIndent(set, "", "  ")
	if err != nil {
		// Marshalling a fixed struct cannot fail; log rather than panic so
		// a future field addition degrades to an empty sitemap, not a crash.
		slog.Error("Failed to build sitemap", "error", err)
	}
	payload := append([]byte(xml.Header), body...)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(payload)
	}
}
