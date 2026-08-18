package handlers

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/ashu-choudhury/portfolio/components"
	"github.com/ashu-choudhury/portfolio/zoho"
)

// ---------------------------------------------------------------------------
// Out-of-band swap plumbing

// oobSwap is one hx-swap-oob element appended to an HTMX fragment
// response. For element ids the handler owns (mail-read / mail-list) the
// wrapper carries id + class + hx-swap-oob and wraps the fragment. For
// raw components (message rows) the component itself carries the id and
// hx-swap-oob attribute.
type oobSwap struct {
	id    string
	tag   string // "section" or "div" (default div)
	class string
	data  templ.Component
	raw   bool
}

// Render writes the OOB element to w.
func (o oobSwap) Render(ctx context.Context, w io.Writer) error {
	if !o.raw {
		if o.tag == "" {
			o.tag = "div"
		}
		fmt.Fprintf(w, `<%s id="%s" hx-swap-oob="outerHTML" class="%s">`, o.tag, o.id, o.class)
	}
	if err := o.data.Render(ctx, w); err != nil {
		return err
	}
	if !o.raw {
		fmt.Fprintf(w, `</%s>`, o.tag)
	}
	return nil
}

// renderMailFragment writes the main fragment followed by OOB swaps.
func (s *Server) renderMailFragment(w http.ResponseWriter, r *http.Request, main templ.Component, oob []oobSwap) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if main != nil {
		if err := main.Render(r.Context(), w); err != nil {
			log.Printf("admin mail: render fragment: %v", err)
			return
		}
	}
	for _, o := range oob {
		if err := o.Render(r.Context(), w); err != nil {
			log.Printf("admin mail: render oob: %v", err)
		}
	}
}

// mailListOOB builds the #mail-list refresh after an action.
func (s *Server) mailListOOB(r *http.Request, c *zoho.Client, folderID string) oobSwap {
	d := s.buildListData(r, c, folderID, 1, "")
	return oobSwap{id: "mail-list", tag: "section", class: "mail-pane mail-list", data: components.AdminMailList(d)}
}

// buildListData loads folders + messages for a pane render.
func (s *Server) buildListData(r *http.Request, c *zoho.Client, folderID string, start int, searchQuery string) components.MailListData {
	ctx := r.Context()
	email, _ := s.Store.GetSetting(ctx, settingZohoEmail)
	d := components.MailListData{
		ActiveFolder: folderID,
		Start:        start,
		Limit:        mailPageSize,
		Email:        email,
		SearchQuery:  searchQuery,
	}
	if searchQuery != "" {
		msgs, err := c.SearchMessages(ctx, searchQuery)
		if err != nil {
			d.Error = "Search failed: " + err.Error()
			return d
		}
		d.Messages = msgs
		return d
	}
	folders, err := c.Folders(ctx)
	if err != nil {
		d.Error = "Could not load folders: " + err.Error()
		return d
	}
	d.Folders = folders
	if folderID == "" {
		folderID = components.MailFolderIDByType(folders, "Inbox")
		d.ActiveFolder = folderID
	}
	d.ActiveFolderName = components.MailFolderName(folders, folderID)
	msgs, err := c.ListMessages(ctx, folderID, start, mailPageSize, "all")
	if err != nil {
		d.Error = "Could not load messages: " + err.Error()
		return d
	}
	d.Messages = msgs
	d.HasMore = len(msgs) == mailPageSize
	return d
}

// ---------------------------------------------------------------------------
// Folder list fragment

// handleAdminMailFolder renders the message list for a folder (HTMX
// fragment) plus an out-of-band empty reading pane.
func (s *Server) handleAdminMailFolder(w http.ResponseWriter, r *http.Request) {
	folderID := r.PathValue("folderID")
	start := atoiDefault(r.URL.Query().Get("start"), 1)
	c, err := s.zohoClient(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d := s.buildListData(r, c, folderID, start, "")
	s.renderMailFragment(w, r, components.AdminMailList(d), []oobSwap{
		{id: "mail-read", tag: "section", class: "mail-pane mail-read", data: components.AdminMailEmpty()},
	})
}

// ---------------------------------------------------------------------------
// Message reading pane

// handleAdminMailMessage renders the reading pane for one message and
// marks it as read in the background.
func (s *Server) handleAdminMailMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	folderID := r.PathValue("folderID")
	messageID := r.PathValue("messageID")

	c, err := s.zohoClient(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	folders, err := c.Folders(ctx)
	if err != nil {
		http.Error(w, "could not load folders", http.StatusInternalServerError)
		return
	}

	content, err := c.MessageContent(ctx, folderID, messageID, true)
	if err != nil {
		d := components.MailReadData{FolderID: folderID, MessageID: messageID, Error: "Could not load this message: " + err.Error()}
		s.renderMailFragment(w, r, components.AdminMailRead(d), nil)
		return
	}
	atts, _ := c.AttachmentInfo(ctx, folderID, messageID)

	d := components.MailReadData{
		FolderID:    folderID,
		MessageID:   messageID,
		Subject:     content.Subject,
		FromAddress: content.FromAddress,
		ToAddress:   content.ToAddress,
		CcAddress:   content.CcAddress,
		Date:        components.MailFullDate(parseMillis(content.ReceivedTime)),
		BodyHTML:    components.SanitizeMailHTML(content.Content, folderID, messageID),
		BodyText:    content.PlainText,
		Attachments: atts,
		Folders:     folders,
	}

	// Row OOB: reflect the read + active state in the list without a full
	// list swap (which would lose scroll position).
	row := zoho.MessageSummary{
		MessageID:     messageID,
		Subject:       content.Subject,
		FromAddress:   content.FromAddress,
		ToAddress:     content.ToAddress,
		Summary:       content.PlainText,
		ReceivedTime:  content.ReceivedTime,
		Status:        "1",
		HasAttachment: boolStr8(len(atts) > 0),
	}

	s.renderMailFragment(w, r, components.AdminMailRead(d), []oobSwap{
		{id: "msg-row-" + messageID, data: components.AdminMailRow(row, folderID, messageID, true), raw: true},
	})

	// Fire-and-forget: mark the message as read server-side.
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = c.UpdateMessages(bgCtx, "markAsRead", []string{messageID}, "")
	}()
}

// ---------------------------------------------------------------------------
// Compose pane

// handleAdminMailCompose renders the compose form, pre-filling it for
// replies, forwards and draft edits.
func (s *Server) handleAdminMailCompose(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	folderID := r.URL.Query().Get("folder")
	msgID := r.URL.Query().Get("msg")
	mode := r.URL.Query().Get("mode")

	d := components.MailComposeData{FolderID: folderID}

	if msgID != "" && (mode == "reply" || mode == "replyall" || mode == "forward" || mode == "draft") {
		c, err := s.zohoClient(ctx)
		if err != nil {
			d.Error = err.Error()
			s.renderMailFragment(w, r, components.AdminMailCompose(d), nil)
			return
		}
		content, err := c.MessageContent(ctx, folderID, msgID, true)
		if err != nil {
			d.Error = "Could not load the original message: " + err.Error()
			s.renderMailFragment(w, r, components.AdminMailCompose(d), nil)
			return
		}
		myEmail, _ := s.Store.GetSetting(ctx, settingZohoEmail)

		switch mode {
		case "reply":
			d.To = components.MailEmail(content.FromAddress)
			d.Subject = replySubject(content.Subject, "Re")
			d.ReplyMsgID = msgID
			d.ReplyAction = "reply"
		case "replyall":
			d.To = joinExcluding(content.ToAddress, myEmail)
			d.Cc = joinExcluding(content.CcAddress, myEmail)
			d.Subject = replySubject(content.Subject, "Re")
			d.ReplyMsgID = msgID
			d.ReplyAction = "replyall"
		case "forward":
			d.Subject = replySubject(content.Subject, "Fwd")
			body := content.PlainText
			if body == "" {
				body = content.Content
			}
			d.Body = "---------- Forwarded message ----------\nFrom: " + content.FromAddress +
				"\nDate: " + components.MailFullDate(parseMillis(content.ReceivedTime)) +
				"\nSubject: " + content.Subject + "\n\n" + body
		case "draft":
			d.To = components.MailCleanAddresses(content.ToAddress)
			d.Cc = components.MailCleanAddresses(content.CcAddress)
			d.Subject = content.Subject
			body := content.PlainText
			if body == "" {
				body = content.Content
			}
			d.Body = body
			d.DraftID = msgID
		}
	}

	s.renderMailFragment(w, r, components.AdminMailCompose(d), nil)
}

// ---------------------------------------------------------------------------
// Send / save draft

// handleAdminMailSend delivers the compose form: sends, saves a draft, or
// replies, depending on the form fields.
func (s *Server) handleAdminMailSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		s.renderMailStatus(w, r, false, "Bad form data: "+err.Error(), "")
		return
	}
	c, err := s.zohoClient(ctx)
	if err != nil {
		s.renderMailStatus(w, r, false, err.Error(), "")
		return
	}

	folderID := r.FormValue("folder_id")
	action := r.FormValue("action")
	from, _ := s.Store.GetSetting(ctx, settingZohoEmail)

	body := r.FormValue("body")
	req := &zoho.SendRequest{
		FromAddress: from,
		ToAddress:   splitAddresses(r.FormValue("to")),
		CcAddress:   splitAddresses(r.FormValue("cc")),
		BccAddress:  splitAddresses(r.FormValue("bcc")),
		Subject:     strings.TrimSpace(r.FormValue("subject")),
		Content:     mailBodyToHTML(body),
		MailFormat:  "html",
		Draft:       action == "draft",
		DraftID:     r.FormValue("draft_id"),
	}
	if req.FromAddress == "" {
		s.renderMailStatus(w, r, false, "The Zoho account email is missing — reconnect the account.", folderID)
		return
	}
	if !req.Draft && len(req.ToAddress) == 0 {
		s.renderMailStatus(w, r, false, "Add at least one recipient.", folderID)
		return
	}
	if strings.TrimSpace(req.Subject) == "" && strings.TrimSpace(req.Content) == "" {
		s.renderMailStatus(w, r, false, "Add a subject or some content before sending.", folderID)
		return
	}

	// Upload any attachments first, then reference them in the send.
	if files := r.MultipartForm.File["attachments"]; len(files) > 0 {
		ups := make([]zoho.UploadFile, 0, len(files))
		for _, fh := range files {
			f, err := fh.Open()
			if err != nil {
				continue
			}
			ups = append(ups, zoho.UploadFile{Name: fh.Filename, Content: f})
			defer f.Close()
		}
		atts, err := c.UploadAttachments(ctx, ups)
		if err != nil {
			s.renderMailStatus(w, r, false, "Attachment upload failed: "+err.Error(), folderID)
			return
		}
		req.Attachments = atts
	}

	replyMsg := r.FormValue("reply_msg")
	replyAction := r.FormValue("reply_action")
	if replyMsg != "" && (replyAction == "reply" || replyAction == "replyall") {
		if _, err := c.Reply(ctx, replyMsg, replyAction, req); err != nil {
			log.Printf("admin mail: reply: %v", err)
			s.renderMailStatus(w, r, false, "Reply failed: "+err.Error(), folderID)
			return
		}
	} else {
		if _, err := c.Send(ctx, req); err != nil {
			log.Printf("admin mail: send: %v", err)
			s.renderMailStatus(w, r, false, "Could not send: "+err.Error(), folderID)
			return
		}
	}

	msg := "Message sent."
	if req.Draft {
		msg = "Draft saved."
	}
	if folderID == "" {
		folderID = components.MailFolderIDByType(mustFolders(r, c), "Inbox")
	}
	s.renderMailStatus(w, r, true, msg, folderID)

	// Refresh the list pane out-of-band so Sent/Drafts reflect the change.
	d := s.buildListData(r, c, folderID, 1, "")
	s.renderMailFragment(w, r, nil, []oobSwap{{id: "mail-list", tag: "section", class: "mail-pane mail-list", data: components.AdminMailList(d)}})
}

// ---------------------------------------------------------------------------
// AI refine

// handleAdminMailRefine rewrites the compose body with the configured AI
// model. On success it swaps the textarea for the polished draft and shows
// a success note; on failure it keeps the draft as-is and shows the error.
func (s *Server) handleAdminMailRefine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	body := r.FormValue("body")
	subject := r.FormValue("subject")

	client := NewAIClient(s.DefaultAIConfig(ctx))
	refined, err := client.RefineEmail(ctx, subject, body)
	if err != nil {
		s.renderMailFragment(w, r, components.AdminMailBodyTextarea(body), []oobSwap{
			{id: "mail-refine-note", class: "form-note mail-refine-note mail-refine-err", data: components.AdminMailRefineNote("⚠ " + err.Error())},
		})
		return
	}
	s.renderMailFragment(w, r, components.AdminMailBodyTextarea(refined), []oobSwap{
		{id: "mail-refine-note", class: "form-note mail-refine-note mail-refine-ok", data: components.AdminMailRefineNote("✓ Refined with AI — review and send")},
	})
}

// ---------------------------------------------------------------------------
// Message actions (archive / spam / trash / read-unread / move / delete)

// handleAdminMailAction applies a state change to one message.
func (s *Server) handleAdminMailAction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	c, err := s.zohoClient(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	action := r.FormValue("action")
	folderID := r.FormValue("folder_id")
	messageID := r.FormValue("message_id")
	destFolder := r.FormValue("dest_folder")

	mode := ""
	switch action {
	case "archive":
		mode = "archive"
	case "unarchive":
		mode = "unarchive"
	case "spam":
		mode = "spam"
	case "notspam":
		mode = "notspam"
	case "trash":
		mode = "trash"
	case "unread":
		mode = "markAsUnread"
	case "read":
		mode = "markAsRead"
	case "move":
		mode = "moveMessage"
	}
	if mode == "" || messageID == "" {
		s.renderMailStatus(w, r, false, "Unknown action.", folderID)
		return
	}

	if err := c.UpdateMessages(ctx, mode, []string{messageID}, destFolder); err != nil {
		log.Printf("admin mail: action %s: %v", mode, err)
		s.renderMailStatus(w, r, false, "Action failed: "+err.Error(), folderID)
		return
	}

	// After a successful action the message leaves the current view:
	// clear the reading pane and refresh the list out-of-band.
	s.renderMailFragment(w, r, components.AdminMailEmpty(), []oobSwap{
		s.mailListOOB(r, c, folderID),
	})
}

// handleAdminMailDelete hard-deletes one message.
func (s *Server) handleAdminMailDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	c, err := s.zohoClient(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	folderID := r.FormValue("folder_id")
	messageID := r.FormValue("message_id")
	if messageID == "" {
		http.Error(w, "missing message id", http.StatusBadRequest)
		return
	}
	if err := c.DeleteMessage(ctx, folderID, messageID); err != nil {
		s.renderMailStatus(w, r, false, "Delete failed: "+err.Error(), folderID)
		return
	}
	s.renderMailFragment(w, r, components.AdminMailEmpty(), []oobSwap{
		s.mailListOOB(r, c, folderID),
	})
}

// ---------------------------------------------------------------------------
// Search

// handleAdminMailSearch renders search results in the list pane.
func (s *Server) handleAdminMailSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	c, err := s.zohoClient(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d := s.buildListData(r, c, "", 1, q)
	s.renderMailFragment(w, r, components.AdminMailList(d), nil)
}

// ---------------------------------------------------------------------------
// Attachments + inline images

// handleAdminMailAttachment proxies a message attachment download.
func (s *Server) handleAdminMailAttachment(w http.ResponseWriter, r *http.Request) {
	folderID := r.PathValue("folderID")
	messageID := r.PathValue("messageID")
	attachmentID := r.PathValue("attachmentID")

	c, err := s.zohoClient(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	body, name, err := c.DownloadAttachment(r.Context(), folderID, messageID, attachmentID)
	if err != nil {
		log.Printf("admin mail: attachment %s/%s: %v", messageID, attachmentID, err)
		http.Error(w, "could not download attachment", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", sniffContentType(body))
	w.Header().Set("Content-Disposition", contentDisposition(name))
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Write(body)
}

// handleAdminMailInline proxies an inline (cid:) image embedded in a
// message body so it renders under the strict CSP.
func (s *Server) handleAdminMailInline(w http.ResponseWriter, r *http.Request) {
	folderID := r.PathValue("folderID")
	messageID := r.PathValue("messageID")
	ref := r.PathValue("ref")

	c, err := s.zohoClient(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	body, err := c.InlineImage(r.Context(), folderID, messageID, ref)
	if err != nil {
		http.Error(w, "inline image not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", sniffContentType(body))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write(body)
}

// ---------------------------------------------------------------------------
// helpers

// renderMailStatus renders a status pane into #mail-read.
func (s *Server) renderMailStatus(w http.ResponseWriter, r *http.Request, ok bool, msg, folderID string) {
	s.renderMailFragment(w, r, components.AdminMailStatus(ok, msg, folderID), nil)
}

// mustFolders returns the account's folders (fallback: empty slice).
func mustFolders(r *http.Request, c *zoho.Client) []zoho.Folder {
	fs, err := c.Folders(r.Context())
	if err != nil {
		return nil
	}
	return fs
}

// parseMillis converts Zoho's epoch-millis timestamps to time.Time.
func parseMillis(ms string) time.Time {
	n, err := strconv.ParseInt(strings.TrimSpace(ms), 10, 64)
	if err != nil || n == 0 {
		return time.Time{}
	}
	return time.UnixMilli(n)
}

// replySubject adds (or de-duplicates) a Re:/Fwd: prefix.
func replySubject(subject, prefix string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return prefix + ": "
	}
	for _, p := range []string{prefix + ": ", prefix + ":"} {
		if strings.HasPrefix(strings.ToLower(subject), strings.ToLower(p)) {
			return subject
		}
	}
	return prefix + ": " + subject
}

// joinExcluding joins addresses but drops the account's own address.
func joinExcluding(addrList, exclude string) string {
	var out []string
	for _, p := range strings.Split(addrList, ",") {
		p = strings.TrimSpace(p)
		if p == "" || strings.EqualFold(p, "Not Provided") {
			continue
		}
		if exclude != "" && strings.EqualFold(components.MailEmail(p), exclude) {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, ", ")
}

// sniffContentType guesses a media type from file bytes.
func sniffContentType(b []byte) string {
	if len(b) > 512 {
		return http.DetectContentType(b[:512])
	}
	return http.DetectContentType(b)
}

// boolStr8 renders "1"/"0" (Zoho's hasAttachment convention).
func boolStr8(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
