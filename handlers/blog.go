package handlers

import (
	"bytes"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/ashu-choudhury/portfolio/components"
	"github.com/ashu-choudhury/portfolio/data"
	"github.com/ashu-choudhury/portfolio/store"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// markdown is the shared goldmark renderer used for blog posts and the
// admin preview. Safe HTML is kept (same trust model as the admin editor).
var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.Typographer),
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

// renderMarkdown converts Markdown source to HTML.
func renderMarkdown(src string) string {
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(src), &buf); err != nil {
		log.Printf("markdown: %v", err)
		return "<p>Could not render this post.</p>"
	}
	return buf.String()
}

func (s *Server) handleBlogIndex(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	posts := s.blogPosts(r, query)
	s.render(w, r, data.BlogIndexMeta(theme(r)), components.BlogIndex(posts, query))
}

// handleBlogSearch is the HTMX fragment endpoint for live post search.
func (s *Server) handleBlogSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	posts := s.blogPosts(r, query)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	components.BlogList(posts).Render(r.Context(), w)
}

// blogPosts returns published posts, filtered by the FTS query when present.
func (s *Server) blogPosts(r *http.Request, query string) []store.Post {
	if query != "" {
		posts, err := s.Store.SearchPosts(r.Context(), query)
		if err != nil {
			log.Printf("blog search %q: %v", query, err)
			return []store.Post{}
		}
		return posts
	}
	posts, err := s.Store.ListPosts(r.Context(), false)
	if err != nil {
		log.Printf("blog list: %v", err)
		return []store.Post{}
	}
	return posts
}

func (s *Server) handleBlogPost(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.GetPost(r.Context(), r.PathValue("slug"))
	if err != nil || !p.Published {
		s.handleNotFound(w, r)
		return
	}
	s.render(w, r, data.BlogPostMeta(p, theme(r)), components.BlogPostPage(*p, renderMarkdown(p.Body)))
}

// handleBlogFeed serves an RSS 2.0 feed of published posts.
func (s *Server) handleBlogFeed(w http.ResponseWriter, r *http.Request) {
	posts, err := s.Store.ListPosts(r.Context(), false)
	if err != nil {
		http.Error(w, "could not load posts", http.StatusInternalServerError)
		return
	}
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">` + "\n")
	b.WriteString("<channel>\n")
	b.WriteString("<title>" + xmlEscape(data.SiteName+" — Blog") + "</title>\n")
	b.WriteString("<link>" + data.URL("/blog") + "</link>\n")
	b.WriteString("<description>" + xmlEscape(data.SiteTag) + "</description>\n")
	b.WriteString("<language>en</language>\n")
	b.WriteString("<atom:link href=\"" + data.URL("/blog/feed.xml") + "\" rel=\"self\" type=\"application/rss+xml\"/>\n")
	lastBuild := time.Now().UTC().Format(time.RFC1123Z)
	if len(posts) > 0 && !posts[0].PublishedAt.IsZero() {
		lastBuild = posts[0].PublishedAt.UTC().Format(time.RFC1123Z)
	}
	b.WriteString("<lastBuildDate>" + lastBuild + "</lastBuildDate>\n")
	for _, p := range posts {
		b.WriteString("<item>\n")
		b.WriteString("<title>" + xmlEscape(p.Title) + "</title>\n")
		b.WriteString("<link>" + data.URL("/blog/"+p.Slug) + "</link>\n")
		b.WriteString("<guid isPermaLink=\"true\">" + data.URL("/blog/"+p.Slug) + "</guid>\n")
		b.WriteString("<description>" + xmlEscape(renderMarkdown(p.Body)) + "</description>\n")
		if !p.PublishedAt.IsZero() {
			b.WriteString("<pubDate>" + p.PublishedAt.UTC().Format(time.RFC1123Z) + "</pubDate>\n")
		}
		b.WriteString("</item>\n")
	}
	b.WriteString("</channel>\n</rss>\n")

	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	_, _ = w.Write(b.Bytes())
}

// xmlEscape escapes text for embedding in XML/RSS output.
func xmlEscape(s string) string {
	var buf bytes.Buffer
	for _, r := range s {
		switch r {
		case '&':
			buf.WriteString("&amp;")
		case '<':
			buf.WriteString("&lt;")
		case '>':
			buf.WriteString("&gt;")
		case '"':
			buf.WriteString("&quot;")
		case '\'':
			buf.WriteString("&apos;")
		default:
			buf.WriteRune(r)
		}
	}
	return buf.String()
}

// renderAdminPage renders an admin page inside the admin layout.
func (s *Server) renderAdminPage(w http.ResponseWriter, r *http.Request, meta components.AdminPageMeta, content templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	components.AdminLayout(meta, content).Render(r.Context(), w)
}
