package components

import (
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ashu-choudhury/portfolio/zoho"
)

// ---------------------------------------------------------------------------
// Data types shared between handlers and templates

// MailConnectData drives the "connect Zoho Mail" screen.
type MailConnectData struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Connected    bool
	Email        string
	Error        string
}

// MailListData is the message-list pane (and the full shell on load).
type MailListData struct {
	Folders          []zoho.Folder
	ActiveFolder     string // folderId
	ActiveFolderName string
	Messages         []zoho.MessageSummary
	Start            int
	Limit            int
	HasMore          bool
	SearchQuery      string
	Email            string
	ActiveMessage    string // messageId currently open (row highlight)
	Error            string
}

// MailReadData is the reading pane.
type MailReadData struct {
	FolderID    string
	MessageID   string
	Subject     string
	FromAddress string
	ToAddress   string
	CcAddress   string
	Date        string
	BodyHTML    string // sanitized HTML, safe for @templ.Raw
	BodyText    string
	Attachments []zoho.Attachment
	Folders     []zoho.Folder
	Error       string
}

// MailComposeData is the compose pane.
type MailComposeData struct {
	To          string
	Cc          string
	Bcc         string
	Subject     string
	Body        string
	DraftID     string
	FolderID    string // active folder, for list refresh after send/draft
	ReplyMsgID  string // when replying: message being replied to
	ReplyAction string // "reply" | "replyall" | ""
	Error       string
}

// ---------------------------------------------------------------------------
// Sanitization of untrusted email HTML

var (
	mailStripBlockRe = regexp.MustCompile(`(?is)<(script|style|iframe|object|embed|form|link|meta|head|base|title|template)[^>]*>.*?</(script|style|iframe|object|embed|form|link|meta|head|base|title|template)>`)
	mailStripSelfRe  = regexp.MustCompile(`(?is)<(script|style|iframe|object|embed|form|link|meta|head|base)(\s[^>]*)?/?>`)
	mailOnAttrRe     = regexp.MustCompile(`(?is)\son\w+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	mailJSURLRe      = regexp.MustCompile(`(?i)\b(javascript|vbscript):`)
	mailCidRe        = regexp.MustCompile(`(?i)(\bsrc\s*=\s*["'])(cid:)([^"']+)(["'])`)
)

// SanitizeMailHTML strips dangerous constructs from incoming email HTML so
// it can be rendered with @templ.Raw. Scripts and event handlers are
// removed outright (the strict CSP is the second line of defence), and
// cid: images are rewritten to the local inline-image proxy so they load
// despite the CSP's img-src allowlist.
func SanitizeMailHTML(h, folderID, messageID string) string {
	if h == "" {
		return ""
	}
	h = mailStripBlockRe.ReplaceAllString(h, "")
	h = mailStripSelfRe.ReplaceAllString(h, "")
	h = mailOnAttrRe.ReplaceAllString(h, "")
	h = mailJSURLRe.ReplaceAllString(h, "")
	h = mailCidRe.ReplaceAllStringFunc(h, func(m string) string {
		sub := mailCidRe.FindStringSubmatch(m)
		if len(sub) != 5 {
			return m
		}
		ref := url.PathEscape(strings.TrimSpace(sub[3]))
		return sub[1] + "/admin/mail/inline/" + url.PathEscape(folderID) + "/" + url.PathEscape(messageID) + "/" + ref + sub[4]
	})
	return h
}

// ---------------------------------------------------------------------------
// Display helpers

// mailName extracts a human-friendly display name from an address string
// like `"Ashu" <ashu@example.com>` or `ashu@example.com`.
func mailName(addr string) string {
	addr = html.UnescapeString(strings.TrimSpace(addr))
	if i := strings.Index(addr, "<"); i >= 0 {
		name := strings.Trim(strings.TrimSpace(addr[:i]), `"' `)
		if name != "" {
			return name
		}
		rest := addr[i+1:]
		if j := strings.Index(rest, ">"); j >= 0 {
			return rest[:j]
		}
		return rest
	}
	addr = strings.Trim(strings.TrimSpace(addr), `"' `)
	if i := strings.Index(addr, "@"); i > 0 {
		return addr[:i]
	}
	if addr == "" {
		return "(no sender)"
	}
	return addr
}

// mailSubject cleans up a subject for display (Zoho HTML-encodes it).
func mailSubject(s string) string {
	s = html.UnescapeString(strings.TrimSpace(s))
	if s == "" {
		return "(no subject)"
	}
	return s
}

// mailSnippet trims an entity-decoded summary to one line.
func mailSnippet(s string) string {
	s = html.UnescapeString(s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 140 {
		s = s[:140] + "…"
	}
	return s
}

// mailWhen renders a relative-ish timestamp: time today, date this year,
// full date otherwise.
func mailWhen(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	if t.Year() == now.Year() {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2, 2006")
}

// mailFullDate renders the long date used in the reading pane.
func mailFullDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("Mon, Jan 2, 2006 at 3:04 PM")
}

// mailSize formats a byte count.
func mailSize(s string) string {
	n, _ := strconv.ParseInt(s, 10, 64)
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) + " " + string("KMGTPE"[exp]) + "B"
}

// mailFolderIcon picks an icon name for a folder.
func mailFolderIcon(name string) string {
	switch strings.ToLower(name) {
	case "inbox":
		return "inbox"
	case "drafts":
		return "draft"
	case "sent":
		return "send"
	case "spam":
		return "alert"
	case "trash":
		return "trash"
	case "archive":
		return "archive"
	case "templates":
		return "code"
	case "starred":
		return "star"
	default:
		return "folder"
	}
}

// mailFolderPriority orders system folders first, then custom folders.
func mailFolderPriority(name string) int {
	switch strings.ToLower(name) {
	case "inbox":
		return 0
	case "drafts":
		return 1
	case "sent":
		return 2
	case "spam":
		return 3
	case "trash":
		return 4
	case "archive":
		return 5
	default:
		return 100
	}
}

// mailOrderFolders sorts folders: system first, custom after.
func mailOrderFolders(fs []zoho.Folder) []zoho.Folder {
	out := make([]zoho.Folder, len(fs))
	copy(out, fs)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			a, b := mailFolderPriority(out[j].FolderName), mailFolderPriority(out[j-1].FolderName)
			if a < b || (a == b && out[j].FolderName < out[j-1].FolderName) {
				out[j], out[j-1] = out[j-1], out[j]
			}
		}
	}
	return out
}

// mailFolderID finds the folderId for a system folder type, used to
// default to Inbox on first load.
func mailFolderID(fs []zoho.Folder, folderType string) string {
	for _, f := range fs {
		if strings.EqualFold(f.FolderType, folderType) {
			return f.FolderID
		}
	}
	return ""
}

// Exported wrappers used by the handler package (the unexported helpers
// above are template-facing).

// MailFolderName resolves a folderId to its display name.
func MailFolderName(fs []zoho.Folder, id string) string { return mailFolderName(fs, id) }

// MailFolderIDByType finds the folderId of a system folder (e.g. "Inbox").
func MailFolderIDByType(fs []zoho.Folder, folderType string) string {
	return mailFolderID(fs, folderType)
}

// MailName extracts a display name from an address string.
func MailName(addr string) string { return mailName(addr) }

// MailEmail extracts the bare email address from an address string.
func MailEmail(addr string) string { return mailEmail(addr) }

// MailFullDate renders the long date used in the reading pane.
func MailFullDate(t time.Time) string { return mailFullDate(t) }

// MailCleanAddresses trims a comma-separated address list for the compose
// form (drops empty entries and Zoho's "Not Provided" placeholder).
func MailCleanAddresses(addr string) string {
	addr = html.UnescapeString(strings.TrimSpace(addr))
	if addr == "" || strings.EqualFold(addr, "not provided") {
		return ""
	}
	parts := strings.Split(addr, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" && !strings.EqualFold(p, "Not Provided") {
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

// mailFolderName resolves a folderId to its display name.
func mailFolderName(fs []zoho.Folder, id string) string {
	for _, f := range fs {
		if f.FolderID == id {
			return f.FolderName
		}
	}
	return ""
}

// mailFolderActive returns the CSS class for the active folder link.
func mailFolderActive(folderID, active string) string {
	if folderID == active {
		return "mail-folder-active"
	}
	return ""
}

// mailFolderCurrent returns the aria-current value for the active folder.
func mailFolderCurrent(folderID, active string) string {
	if folderID == active {
		return "page"
	}
	return ""
}

// mailListTitle is the list-pane heading (folder name or search query).
func mailListTitle(d MailListData) string {
	if d.SearchQuery != "" {
		return "Search: “" + d.SearchQuery + "”"
	}
	if d.ActiveFolderName != "" {
		return d.ActiveFolderName
	}
	return "Mail"
}

// mailRowFolder returns the folder a row's link should use — the active
// folder normally, the message's own folder in search results (where
// results span folders).
func mailRowFolder(d MailListData, m zoho.MessageSummary) string {
	if d.SearchQuery != "" && m.FolderID != "" {
		return m.FolderID
	}
	return d.ActiveFolder
}

// mailRowClass returns the row classes for a message row.
func mailRowClass(m zoho.MessageSummary, activeMessage string) string {
	cls := "mail-row"
	if !m.IsRead() {
		cls += " mail-row-unread"
	}
	if m.MessageID == activeMessage {
		cls += " mail-row-active"
	}
	return cls
}

// mailPrevStart / mailNextStart compute pagination offsets.
func mailPrevStart(d MailListData) int {
	if d.Start-d.Limit < 1 {
		return 1
	}
	return d.Start - d.Limit
}

func mailNextStart(d MailListData) int {
	return d.Start + d.Limit
}

// DraftEditable reports whether this message can be opened in the compose
// editor (true only for drafts).
func (d MailReadData) DraftEditable() bool {
	return d.FolderID != "" && d.MessageID != ""
}

// mailEmail extracts the bare email address from an address string.
func mailEmail(addr string) string {
	addr = html.UnescapeString(strings.TrimSpace(addr))
	if i := strings.Index(addr, "<"); i >= 0 {
		rest := addr[i+1:]
		if j := strings.Index(rest, ">"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
		return strings.TrimSpace(rest)
	}
	return strings.Trim(strings.TrimSpace(addr), `"' `)
}

// mailAddressList renders a comma-separated address list for the meta line.
func mailAddressList(addr string) string {
	addr = html.UnescapeString(strings.TrimSpace(addr))
	if addr == "" || strings.EqualFold(addr, "not provided") {
		return "—"
	}
	parts := strings.Split(addr, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, mailName(p)+" <"+mailEmail(p)+">")
	}
	return strings.Join(out, ", ")
}

// mailActionIcon maps reply modes to icons.
func mailActionIcon(mode string) string {
	switch mode {
	case "reply":
		return "reply"
	case "replyall":
		return "reply"
	case "forward":
		return "forward"
	default:
		return "send"
	}
}
