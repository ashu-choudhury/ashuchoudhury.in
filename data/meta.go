package data

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/ashu-choudhury/portfolio/store"
)

// BaseURL returns the canonical site origin, overridable with SITE_URL.
func BaseURL() string {
	if u := os.Getenv("SITE_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return DefaultURL
}

// URL joins the base URL and a path.
func URL(path string) string {
	return BaseURL() + path
}

// PageMeta carries the per-page SEO information rendered into <head>.
type PageMeta struct {
	Title       string // full <title>
	Description string // meta description
	Canonical   string // absolute canonical URL
	Image       string // og:image path, e.g. /static/og.svg
	Type        string // og:type: website | profile | article
	JSONLD      string // pre-marshalled JSON-LD block
	Active      string // nav item to highlight
	Theme       string // dark | light
	NoIndex     bool   // robots noindex
}

func jsonLD(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

// HomeMeta returns SEO metadata for the home page.
func HomeMeta(theme string) PageMeta {
	return PageMeta{
		Title:       SiteName + " — " + SiteRole,
		Description: "Android & Full-Stack Developer building apps, systems and AI tooling — from the binary up to the API. Explore open-source projects on GitHub.",
		Canonical:   URL("/"),
		Image:       "/static/og.svg",
		Type:        "website",
		JSONLD: jsonLD([]any{
			map[string]any{
				"@context":    "https://schema.org",
				"@type":       "Person",
				"name":        SiteName,
				"jobTitle":    SiteRole,
				"description": SiteTag,
				"url":         URL("/"),
				"sameAs":      []string{"https://github.com/ashu-choudhury", "https://www.npmjs.com/~ashu-choudhury", "https://pub.dev/packages/jiosaavn_dart"},
				"knowsAbout":  []string{"Android", "Kotlin", "Go", "Rust", "Python", "TypeScript", "Dart", "Jetpack Compose", "NDK", "REST APIs", "AI"},
			},
			map[string]any{
				"@context":   "https://schema.org",
				"@type":      "WebSite",
				"name":       SiteName + " — " + SiteRole,
				"url":        URL("/"),
				"inLanguage": "en",
			},
		}),
		Active: "home",
		Theme:  theme,
	}
}

// AboutMeta returns SEO metadata for the about page.
func AboutMeta(theme string) PageMeta {
	return PageMeta{
		Title:       "About — " + SiteName,
		Description: "Learn about " + SiteName + ", a self-taught " + SiteRole + " — Android apps, Go and Rust systems, Kotlin libraries and AI tooling.",
		Canonical:   URL("/about"),
		Image:       "/static/og.svg",
		Type:        "profile",
		JSONLD: jsonLD(map[string]any{
			"@context":   "https://schema.org",
			"@type":      "AboutPage",
			"name":       "About " + SiteName,
			"url":        URL("/about"),
			"inLanguage": "en",
			"mainEntity": map[string]any{
				"@type":       "Person",
				"name":        SiteName,
				"jobTitle":    SiteRole,
				"description": SiteTag,
				"url":         URL("/"),
				"sameAs":      []string{"https://github.com/ashu-choudhury"},
			},
		}),
		Active: "about",
		Theme:  theme,
	}
}

// ContactMeta returns SEO metadata for the contact page.
func ContactMeta(theme string) PageMeta {
	return PageMeta{
		Title:       "Contact — " + SiteName,
		Description: "Get in touch with " + SiteName + " for collaboration, open-source contributions or project work. Reach out via the form or GitHub.",
		Canonical:   URL("/contact"),
		Image:       "/static/og.svg",
		Type:        "website",
		JSONLD: jsonLD(map[string]any{
			"@context":   "https://schema.org",
			"@type":      "ContactPage",
			"name":       "Contact " + SiteName,
			"url":        URL("/contact"),
			"inLanguage": "en",
		}),
		Active: "contact",
		Theme:  theme,
	}
}

// ProjectsMeta returns SEO metadata for the projects index page.
func ProjectsMeta(theme string, count int) PageMeta {
	return PageMeta{
		Title:       "Projects — " + SiteName,
		Description: "Open-source projects by " + SiteName + " — Android apps, Go and Rust systems, Kotlin libraries and AI tooling, on GitHub, npm and pub.dev.",
		Canonical:   URL("/projects"),
		Image:       "/static/og.svg",
		Type:        "website",
		Active:      "projects",
		Theme:       theme,
		JSONLD: jsonLD(map[string]any{
			"@context":   "https://schema.org",
			"@type":      "CollectionPage",
			"name":       "Projects by " + SiteName,
			"url":        URL("/projects"),
			"inLanguage": "en",
			"about": map[string]any{
				"@type":    "Person",
				"name":     SiteName,
				"jobTitle": SiteRole,
			},
		}),
	}
}

// ProjectMeta returns SEO metadata for a project detail page.
func ProjectMeta(p *store.Project, theme string) PageMeta {
	return PageMeta{
		Title:       p.Name + " — " + SiteName,
		Description: truncate(p.Tagline+". "+p.Summary, 158),
		Canonical:   URL("/projects/" + p.Slug),
		Image:       "/static/og.svg",
		Type:        "article",
		Active:      "projects",
		Theme:       theme,
		JSONLD: jsonLD(map[string]any{
			"@context":            "https://schema.org",
			"@type":               "SoftwareSourceCode",
			"name":                p.Name,
			"alternateName":       p.Slug,
			"description":         p.Summary,
			"url":                 URL("/projects/" + p.Slug),
			"programmingLanguage": strings.Join(p.Stack, ", "),
			"author": map[string]any{
				"@type": "Person",
				"name":  SiteName,
				"url":   URL("/about"),
			},
			"codeRepository": p.RepoURL,
		}),
	}
}

// BlogIndexMeta returns SEO metadata for the blog index.
func BlogIndexMeta(theme string) PageMeta {
	return PageMeta{
		Title:       "Blog — " + SiteName,
		Description: "Notes and writing from " + SiteName + " on Android, Go, Rust and the things he builds.",
		Canonical:   URL("/blog"),
		Image:       "/static/og.svg",
		Type:        "website",
		Active:      "blog",
		Theme:       theme,
		JSONLD: jsonLD(map[string]any{
			"@context":   "https://schema.org",
			"@type":      "Blog",
			"name":       SiteName + " — Blog",
			"url":        URL("/blog"),
			"inLanguage": "en",
			"author": map[string]any{
				"@type": "Person",
				"name":  SiteName,
				"url":   URL("/about"),
			},
		}),
	}
}

// BlogPostMeta returns SEO metadata for a single post.
func BlogPostMeta(p *store.Post, theme string) PageMeta {
	pub := ""
	if !p.PublishedAt.IsZero() {
		pub = p.PublishedAt.UTC().Format("2006-01-02")
	}
	return PageMeta{
		Title:       p.Title + " — " + SiteName,
		Description: firstLineOf(p.Summary),
		Canonical:   URL("/blog/" + p.Slug),
		Image:       "/static/og.svg",
		Type:        "article",
		Active:      "blog",
		Theme:       theme,
		JSONLD: jsonLD(map[string]any{
			"@context":      "https://schema.org",
			"@type":         "BlogPosting",
			"headline":      p.Title,
			"description":   p.Summary,
			"datePublished": pub,
			"url":           URL("/blog/" + p.Slug),
			"inLanguage":    "en",
			"author": map[string]any{
				"@type": "Person",
				"name":  SiteName,
			},
		}),
	}
}

// NotFoundMeta returns metadata for the 404 page (noindexed).
func NotFoundMeta(theme string) PageMeta {
	return PageMeta{
		Title:       "Page not found — " + SiteName,
		Description: "The page you are looking for does not exist.",
		Canonical:   URL("/"),
		Image:       "/static/og.svg",
		Type:        "website",
		Active:      "",
		Theme:       theme,
		NoIndex:     true,
	}
}

// truncate shortens s to at most n runes, appending an ellipsis.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// firstLineOf returns the first non-empty line of s, truncated to fit a
// 160-char meta description (truncate adds the ellipsis).
func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return truncate(strings.TrimSpace(s), 158)
}

// SitemapXML renders the sitemap for the canonical pages plus the dynamic
// paths (project detail pages, blog posts) supplied by the caller.
func SitemapXML(paths ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	write := func(p string) {
		b.WriteString("  <url>\n    <loc>" + URL(p) + "</loc>\n  </url>\n")
	}
	write("/")
	write("/about")
	write("/projects")
	write("/blog")
	write("/contact")
	for _, p := range paths {
		write(p)
	}
	b.WriteString("</urlset>\n")
	return b.String()
}

// RobotsTXT renders robots.txt: allow the whole public site, explicitly
// block the admin panel, welcome every major AI assistant/search/training
// crawler, and point crawlers at the sitemap. "User-agent: *" already
// permits all of them — the explicit list is a documented statement that
// the site's content may be crawled and used for AI training.
func RobotsTXT() string {
	aiCrawlers := []string{
		// OpenAI: training + search
		"GPTBot", "OAI-SearchBot", "ChatGPT-User",
		// Google: Gemini training (Google-Extended) is separate from Googlebot
		"Google-Extended",
		// Anthropic
		"ClaudeBot", "Claude-Web", "anthropic-ai",
		// Perplexity, Meta, Amazon, Apple, Brave, You.com
		"PerplexityBot", "meta-externalagent", "meta-externalFetcher",
		"Amazonbot", "Applebot-Extended", "FriendlyCrawler", "YouBot",
		// Common Crawl & research corpora
		"CCBot", "ai2bot", "omgili", "SeekrBot", "Diffbot", "ImagesiftBot",
		// Chinese AI crawlers
		"Bytespider", "cohere-ai", "Timpibot",
	}
	var b strings.Builder
	b.WriteString("User-agent: *\nAllow: /\nDisallow: /admin\n\n")
	b.WriteString("# AI assistants, AI search engines and training crawlers are welcome.\n")
	for _, ua := range aiCrawlers {
		b.WriteString("User-agent: " + ua + "\n")
	}
	b.WriteString("Allow: /\n\n")
	b.WriteString("Sitemap: " + URL("/sitemap.xml") + "\n")
	return b.String()
}

// LLMSTXT renders llms.txt following the llmstxt.org convention so AI
// agents (and the Lighthouse agentic-browsing checks) can discover the
// site: the canonical pages plus every shown project and published blog
// post passed in from the store.
func LLMSTXT(projects []store.Project, posts []store.Post) string {
	var b strings.Builder
	b.WriteString("# " + SiteName + "\n\n")
	b.WriteString("> " + SiteTag + "\n\n")
	b.WriteString("Personal website of " + SiteName + ", an " + SiteRole + ".\n\n")
	b.WriteString("## Pages\n\n")
	b.WriteString("- [About](" + URL("/about") + "): biography, experience and skills\n")
	b.WriteString("- [Projects](" + URL("/projects") + "): open-source projects, apps and libraries\n")
	b.WriteString("- [Blog](" + URL("/blog") + "): articles and engineering notes\n")
	b.WriteString("- [Contact](" + URL("/contact") + "): get in touch\n")

	if len(projects) > 0 {
		b.WriteString("\n## Projects\n\n")
		for _, p := range projects {
			b.WriteString(llmsLink(p.Name, URL("/projects/"+p.Slug), firstLineOf(p.Tagline)))
		}
	}
	if len(posts) > 0 {
		b.WriteString("\n## Blog posts\n\n")
		for _, p := range posts {
			b.WriteString(llmsLink(p.Title, URL("/blog/"+p.Slug), firstLineOf(p.Summary)))
		}
	}
	return b.String()
}

// llmsLink renders one "- [label](url): description" bullet, omitting the
// description when there is none.
func llmsLink(label, url, desc string) string {
	if desc == "" {
		return "- [" + label + "](" + url + ")\n"
	}
	return "- [" + label + "](" + url + "): " + desc + "\n"
}
