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
- 💻 **Projects**: Live Showcase of open-source projects with dynamic README rendering.
- 🛠️ **Admin Panel**: Lightweight content management system to publish posts, manage projects, and manage public files.

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

This project is released into the public domain under **[The Unlicense](LICENSE)**. Feel free to use the code for your own website or projects.
