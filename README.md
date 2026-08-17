# ashuchoudhury.in

> My website created using Go + HTMX.

🌐 **[Visit site](https://ashuchoudhury.in)**

---

## About

This is the source code for my personal website and portfolio. It is designed to be fast, lightweight, and single-binary server-rendered application with zero client-authored JavaScript.

### Features
- 🚀 **Fast & Lightweight**: Server-rendered HTML with HTMX over the wire.
- 🎨 **Clean Aesthetics**: Modern typography, responsive design system, dark and light theme toggle.
- 📝 **Blog**: Built-in Markdown articles with full-text search and RSS feed.
- ✨ **AI blog generator**: Two-session pipeline — an AI strategist reviews the published blog history and picks the best next topic, then a brand-new AI session writes the full post. Generate from the admin panel or automatically via a cron ping.
- 💻 **Projects**: Live Showcase of open-source projects with dynamic README rendering.
- 🛠️ **Admin Panel**: Lightweight content management system to publish posts, manage projects, and manage public files.

---

## 🤖 Automatic AI blog generation

The site ships a fully automatic blog pipeline that runs on a cron ping:

```bash
curl -X POST "https://your-site/api/ai/generate?token=YOUR_TOKEN"
```

When that endpoint is hit it runs two **separate, isolated LLM sessions**:

1. **Strategist** — sees the published blog history (titles, tags, summaries)
   and picks the single best next topic (title, angle, tags). The session ends.
2. **Writer** — starts a brand-new chat that only receives the chosen topic
   (never the strategist's conversation or your blog history) and writes the
   complete, detailed Markdown post.

The post is then **published automatically** (slug deduplicated, RSS + search
engines notified). The endpoint returns `202` immediately and the pipeline
runs in the background; poll `GET /api/ai/generate/status?job_id=…` for
progress, or add `&wait=true` to run synchronously.

Set the token via the `AI_GENERATE_TOKEN` environment variable or Admin →
Settings → “Auto-generate ping token”, and the model/API key via
`OPENAI_API_KEY` / `OPENAI_BASE_URL` / `OPENAI_MODEL` (or Admin → Settings).
Then point a scheduler (cron-job.org, GitHub Actions, a cron job on your
server) at the URL above — e.g. once a week.

From the admin panel you can also generate a post manually with or without a
topic: leave the topic empty and the strategist picks the next one from your
blog history. Generated drafts are loaded into the editor for review before
saving (only the ping endpoint publishes automatically).

---

## Tech Stack

- **Backend**: Go (Standard Library `net/http`)
- **Templating**: [Templ](https://templ.guide) (Type-safe HTML templates in Go)
- **Frontend Interactivity**: [HTMX 2.0](https://htmx.org)
- **Database**: SQLite
- **Styling**: Vanilla CSS3

---

## Local Development

### Prerequisites
- [Go 1.22+](https://go.dev)
- [Templ CLI](https://templ.guide)

```bash
go install github.com/a-h/templ/cmd/templ@latest
```

### Running Locally

```bash
# Clone the repository
git clone https://github.com/ashu-choudhury/ashuchoudhury.in.git
cd ashuchoudhury.in

# Generate Templ components
templ generate

# Run the server
ADMIN_PASSWORD=admin go run main.go
```

Open `http://localhost:8080` in your browser.

---

## License

This project is licensed under the **[MIT License](LICENSE)**. Feel free to use the code for your own website or projects.
