# ashuchoudhury.in

My website created using Go + HTMX.

🔗 **[Visit site](https://ashuchoudhury.in)**

---

## ⚡ Overview

A high-performance, single-binary personal website and portfolio built with **Go + Templ + HTMX**: strongly-typed server-rendered HTML, HTML-over-the-wire interactivity, and zero client-authored JavaScript — all compiled into a single executable with embedded static assets.

### Features
- **Public Website**: Home, About, Projects, Blog (with FTS5 full-text search & RSS feed at `/blog/feed.xml`), and Contact.
- **Server-Side Live GitHub README Fetching**: Renders repository `README.md` dynamically on project detail pages.
- **Admin Panel (`/admin`)**: Dashboard with zero-JS SVG analytics chart, Markdown blog editor with live preview, message inbox, project curation, and site settings.
- **File Manager (`/admin/files`)**: Upload, browse, and delete files inside `storage/persisted/www` with public streaming at `/files/*` (unlimited file size support & HTTP Range streaming).
- **Public Domain**: Completely open-source under [The Unlicense](LICENSE).

---

## 🛠️ Tech Stack

- **Backend**: Go (standard library `net/http`)
- **Templating**: [Templ](https://templ.guide) (type-safe HTML templates in Go)
- **Frontend Interactivity**: [HTMX 2.0](https://htmx.org) (self-hosted, zero JavaScript dependencies)
- **Styling**: Modern Vanilla CSS3 (Custom Properties, Flexbox & Grid layout)
- **Database**: SQLite (pure-Go via `modernc.org/sqlite` — zero CGO dependencies)
- **File Storage**: Local persistent disk `storage/persisted/www` with optional S3-compatible automated backups

---

## 🚀 Quick Start

### Prerequisites
- [Go 1.22+](https://go.dev)
- [templ CLI](https://templ.guide)

```bash
go install github.com/a-h/templ/cmd/templ@latest
```

### Build & Run Locally

```bash
# 1. Clone the repository
git clone https://github.com/ashu-choudhury/ashuchoudhury.in.git
cd ashuchoudhury.in

# 2. Generate Templ components
templ generate

# 3. Build single binary
go build -o portfolio .

# 4. Start the server
ADMIN_PASSWORD=admin ./portfolio
```

Open <http://localhost:8080> in your browser. The Admin Panel is available at <http://localhost:8080/admin>.

---

## ⚙️ Environment Variables

| Variable | Default | Purpose |
| :--- | :--- | :--- |
| `PORT` | `8080` | HTTP server port |
| `SITE_URL` | `https://ashuchoudhury.in` | Canonical site URL |
| `DB_PATH` | `storage/persisted/portfolio.db` | SQLite database file location |
| `ADMIN_USER` | `admin` | Admin panel username |
| `ADMIN_PASSWORD` | `admin` | Admin panel password |
| `S3_BUCKET` | *(disabled)* | S3 bucket name (enables automated SQLite backups) |

---

## 📜 License

This project is released into the public domain under **[The Unlicense](LICENSE)**. You are free to copy, modify, distribute, or use this code for any purpose, commercial or non-commercial.
