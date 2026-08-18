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

## 📧 Admin Mail client (Zoho Mail API)

The admin panel includes a full email client backed by the free Zoho Mail REST API — inbox and all folders, reading, composing, sending, drafts, search, attachments, archive/spam/trash/move and read/unread. Everything runs server-side through the Zoho Mail API (OAuth 2.0), so no mail credentials ever reach the browser.

### One-time setup (Admin → Mail)

1. Create a **Server-based Application** in the Zoho API Console for India: `https://api-console.zoho.in`.
2. Add the **Authorized Redirect URI** shown on the connect screen (e.g. `https://your-site/admin/mail/oauth/callback`) to that app.
3. Paste the **Client ID** and **Client Secret** and click **Connect Zoho Mail**. You'll be taken to Zoho's consent screen (scopes: `ZohoMail.messages.ALL`, `ZohoMail.accounts.READ`, `ZohoMail.folders.READ`, `ZohoMail.attachments.ALL`).

The data center is **fixed to India** (`accounts.zoho.in` / `mail.zoho.in`) — there is no region selector, because selectable regions caused OAuth token mismatches for the Indian account.

Tokens are stored in the `settings` table and refreshed automatically (access tokens live one hour). The API works on the free Zoho Mail plans — sending limits are dynamic per account reputation.

Credentials can also live in the environment — `ZOHO_CLIENT_ID` and `ZOHO_CLIENT_SECRET` are seeded into the settings table at boot (only when set, so an empty var never wipes a value entered on the connect screen). A `.env` file next to the binary is loaded automatically (real environment variables win). The data center is always India (`mail.zoho.in`).

### Capabilities

- **Folders**: full tree (Inbox, Sent, Drafts, Spam, Trash, custom folders) with pagination.
- **Read**: HTML content with a sanitizer (scripts/handlers stripped, `cid:` inline images proxied through the server), plain-text fallback, attachment download links.
- **Compose**: send, save draft, edit drafts, reply / reply-all (quoted server-side by Zoho), forward, multiple attachments. The body is written in plain text or **Markdown** (converted to formatted HTML on send — raw HTML passes through as-is), and a **Refine with AI** button rewrites the draft into a polished, well-formatted email using your configured AI provider.
- **Manage**: archive, spam, trash, permanent delete, move between folders, mark read/unread, mailbox search.

Rate limits and endpoint quirks are documented in `zoho/zoho.go`.

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
