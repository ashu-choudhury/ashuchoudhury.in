# Ashu Choudhury — Portfolio

A full-fledged, SEO-optimized portfolio website built with the **Go + Templ + HTMX** stack:
strongly typed server-rendered HTML, HTML-over-the-wire interactivity, and **zero
client-authored JavaScript** — all embedded into a single ~18 MB binary.

The site is database-driven: projects, blog posts, contact messages, page-view analytics
and admin sessions all live in **SQLite** (pure-Go driver, `CGO_ENABLED=0` friendly),
and the public pages render from that data. A **GitHub importer** (`cmd/import-github`)
syncs your repositories into the database, classifying each one so only *your* work is
shown — originals and substantially-rewritten forks yes, plain clones no — sorted by
last-updated, newest first.

## Features

**Public site**
- Home (hero + terminal card, about teaser, featured projects, skills, CTA), About,
  Projects with live server-side search, per-project detail pages with stack fingerprints,
  Contact, Blog, and a styled 404.
- Blog with SQLite FTS5 full-text search, goldmark markdown rendering, and an RSS feed
  at `/blog/feed.xml`.

**Admin panel** (`/admin`)
- Login with bcrypt password hashing, session cookies, per-IP rate limiting and CSRF protection.
- Dashboard with first-party page-view analytics: totals, today, a zero-JS server-rendered
  SVG trend chart, and a top-pages table.
- Blog editor with a **live HTMX markdown preview**, drafts, tags, publish/unpublish, delete.
- Contact-message inbox (every form submission is stored in SQLite).
- Project curation: change classification (original / rewritten / clone), visibility and
  featured flags per project — your say overrides the importer's heuristics.
- Settings: edit the site name and meta description; changes apply immediately.

**Stack story, honored**
- htmx 2.0.4 self-hosted (~12.9 KB gzipped), zero `node_modules`, one binary with
  everything embedded via `go:embed`.
- HTMX end-to-end: live project search, live blog search, live markdown preview, contact
  form fragments, theme toggle via `HX-Refresh`, `hx-boost` navigation, native `<details>` mobile menu.

**SEO & security**
- Unique title / description / canonical per page, Open Graph + Twitter cards, JSON-LD
  (`Person`, `SoftwareSourceCode`, `BlogPosting`, `ItemList`), `sitemap.xml` + `robots.txt`
  generated from the live database, RSS feed, semantic single-`h1` markup.
- Strict CSP, `X-Content-Type-Options`, `Referrer-Policy`, `frame-ancestors 'none'`, no inline styles.

## Quick start

Requires **Go 1.22+** and the [templ CLI](https://templ.guide) (pinned to v0.3.1020).

```bash
go install github.com/a-h/templ/cmd/templ@v0.3.1020

templ generate            # typed HTML components
go build -o portfolio .   # single binary; static/, migrations and seed data embedded
ADMIN_PASSWORD=changeme ./portfolio   # listens on :8080
```

Open <http://localhost:8080>. The admin panel is at <http://localhost:8080/admin>
(default login `admin` / whatever `ADMIN_PASSWORD` you set — **always set it**).

## Configuration (environment variables)

| Variable          | Default            | Purpose |
| ----------------- | ------------------ | ------- |
| `PORT`            | `8080`             | HTTP listen port. |
| `SITE_URL`        | `https://ashuchoudhury.in` | Canonical origin for sitemap, canonical/OG/Twitter URLs and JSON-LD. |
| `DB_PATH`         | `storage/persisted/portfolio.db` | SQLite database file. Use `:memory:` for an ephemeral run. |
| `ADMIN_USER`      | `admin`            | Admin username. |
| `ADMIN_PASSWORD`  | `admin`            | Admin password (prints a warning if unset). |
| `S3_BUCKET`       | *(disabled)*       | Bucket name — **setting it enables S3 backups** of the database. |
| `S3_REGION`       | `us-east-1`        | S3 region. |
| `S3_ENDPOINT`     | *(empty)*          | Custom endpoint for S3-compatible providers (Cloudflare R2, MinIO…); path-style requests are used automatically. |
| `S3_PREFIX` / `S3_DB_KEY` | `portfolio/` / `portfolio.db` | Object key for the database backup. |
| `S3_INTERVAL`     | `300`              | Backup interval in seconds. |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` | *(credential chain)* | AWS credentials; if unset the default AWS chain applies. |

> **Persistence model.** Only the SQLite file lives on disk, under `storage/persisted/`
> (auto-created; gitignored). The database is **WAL-checkpointed before each upload** so
> S3 snapshots are always consistent, and on a fresh volume with no local database the
> latest backup is **restored automatically**. See [Deployment](#deployment).

## Syncing from GitHub

```bash
go run ./cmd/import-github -user ashu-choudhury -db portfolio.db
```

Pulls your repositories (name, description, language, topics, stars, forks, pushed date)
plus a fingerprint of each project's stack (build files, manifests, README signals) and
classifies every repo:

- **original** — created by you → shown
- **rewritten** — forked, but you substantially rewrote/repurposed it → shown
- **clone** — a fork of someone else's work you didn't meaningfully change → hidden

Classification is heuristic; **override any repo in the admin panel** (Projects page).
Run the importer again any time — it upserts and never touches your curation flags.

## Project structure

```
├── main.go                 # boot: open DB, seed, S3 restore/backup, wire handlers, graceful shutdown
├── cmd/import-github/      # CLI: sync repos from the GitHub API into SQLite
├── storage/                # durable persistence (S3-compatible backups, env-configured)
│   ├── s3.go               # restore-on-empty + periodic backup loop with WAL checkpoint
│   └── persisted/          # local runtime data (SQLite file — gitignored, back it up)
├── data/                   # identity content + SEO metadata builders (no hard-coded projects)
│   ├── profile.go          # ✏️ bio, highlights, skills, socials (static identity)
│   └── meta.go             # per-page SEO + JSON-LD + sitemap/robots builders
├── store/                  # ⬅ the database center point
│   ├── store.go            # Store interface + models (projects, posts, messages, analytics, sessions, settings)
│   ├── sqlite.go           # SQLite implementation (pure-Go modernc.org/sqlite, embedded schema)
│   ├── memory.go           # in-memory implementation (tests, demos)
│   └── seed.go             # curated project catalogue + welcome post (idempotent)
├── handlers/               # HTTP layer: routes, middleware, pages, blog, admin
│   ├── server.go           # mux wiring, security headers, static serving
│   ├── middleware.go       # logging, analytics, auth, CSRF, rate limiting
│   ├── pages.go            # public pages + HTMX fragments
│   ├── blog.go             # blog index/search/feed/detail + markdown rendering
│   └── admin.go            # admin auth, dashboard, posts CRUD, messages, curation, settings
├── components/             # templ components (typed HTML)
│   ├── layout.templ / nav.templ / footer.templ
│   ├── home.templ / about.templ / projects.templ / contact.templ / errors.templ
│   ├── blog.templ          # blog index + list fragment + post page
│   ├── admin.templ         # admin shell, login, dashboard, editors, tables
│   ├── icons.templ         # inline SVG icon set
│   └── helpers.go          # Go helpers for templates
└── static/
    ├── css/style.css       # design system (dark + light), admin + blog styles
    ├── htmx.min.js         # htmx 2.0.4, self-hosted
    ├── htmx-config.js      # opts out of htmx's injected indicator styles (CSP-safe)
    ├── favicon.svg / og.svg
```

## Swapping the database

Everything talks to the `store.Store` interface — never to a concrete database. To use a
different backend, implement `Store` and pass it to `handlers.New` in `main.go`; nothing
else changes. Two implementations ship: `store.OpenSQLite` (persistent, default) and
`store.NewMemory` (in-memory, handy for tests).

## Deployment

The output is one self-contained binary (templates, CSS, htmx, schema and seed data are
embedded); the only thing on disk is the SQLite file under `storage/persisted/`, which must
persist across restarts. Everything is configured from the environment — **no secrets are
hardcoded anywhere** — so this repository can be published publicly.

### Bare metal

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o portfolio .
SITE_URL=https://yourdomain.in PORT=8080 ADMIN_PASSWORD=$(openssl rand -hex 16) ./portfolio
```

Put it behind a TLS-terminating reverse proxy (Caddy/Nginx/Cloudflare).

### Docker

```bash
docker compose up -d --build          # see docker-compose.yml; ADMIN_PASSWORD required
cp .env.example .env                  # then fill in values (never commit .env)
docker compose --profile minio up -d  # optionally: local MinIO for testing S3 backups
```

The image is a multi-stage build that produces a ~15 MB scratch image. The `storage`
volume persists the database; set `DB_PATH=/storage/persisted/portfolio.db` (the default
in the image).

### Render / Fly.io / Railway (ephemeral disks)

These platforms give containers a **fresh, empty disk on every deploy** — the SQLite file
would be lost unless you back it up. That's exactly what the S3 layer is for:

1. Create an S3 bucket (AWS S3, or Cloudflare R2 / MinIO via `S3_ENDPOINT`).
2. Set these env vars on the service (Render: *Environment* → *Secret Files* or env
   variables; never commit them):

   ```
   S3_BUCKET=your-bucket
   AWS_ACCESS_KEY_ID=…
   AWS_SECRET_ACCESS_KEY=…
   DB_PATH=/storage/persisted/portfolio.db
   ```

3. Mount a persistent disk/volume at `/storage` if the platform supports it (Render
   persistent disks, Fly volumes) — **or** let the S3 layer be the persistence: on every
   boot the app restores the latest backup into the fresh volume, serves traffic, and
   backs up every `S3_INTERVAL` seconds plus a final backup on shutdown.

**Backup/restore rules:** a backup runs on boot (if the local file is missing), every
`S3_INTERVAL` seconds, and once more on graceful shutdown. The database is
WAL-checkpointed before each upload so the snapshot is always consistent. Local data is
never overwritten by a restore.

## Notes

- The templ CLI version must match `go.mod` (v0.3.1020): plain-Go `if`/`for`/`switch`
  inside templates, `@Component()` calls at line starts.
- Analytics are first-party and bot-filtered; `HX-Request` fragment calls are not
  double-counted. Data lives only in your own database — no third-party trackers.
- The admin panel is a single account (env-configured). For anything heavier, put the
  whole `/admin` behind a VPN or an IP allow-list at the proxy.
