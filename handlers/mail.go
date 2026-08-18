package handlers

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/ashu-choudhury/portfolio/components"
	"github.com/ashu-choudhury/portfolio/zoho"
)

// ---------------------------------------------------------------------------
// Zoho Mail settings keys (settings table, same pattern as the AI config)

const (
	settingZohoClientID     = "zoho_client_id"
	settingZohoClientSecret = "zoho_client_secret"
	settingZohoDataCenter   = "zoho_data_center"
	settingZohoRefreshToken = "zoho_refresh_token"
	settingZohoAccessToken  = "zoho_access_token"
	settingZohoAccessExpiry = "zoho_access_expiry"
	settingZohoAccountID    = "zoho_account_id"
	settingZohoEmail        = "zoho_email"
	settingZohoOAuthState   = "zoho_oauth_state"
)

// zohoMailScope is the OAuth scope set for a full email client: read +
// write messages, list folders, read accounts, handle attachments.
const zohoMailScope = "ZohoMail.messages.ALL,ZohoMail.accounts.READ,ZohoMail.folders.READ,ZohoMail.attachments.ALL"

// errZohoNotConfigured signals missing client credentials.
var errZohoNotConfigured = errors.New("zoho mail is not configured")

// ---------------------------------------------------------------------------
// Client factory

// zohoClient builds a zoho.Client from persisted settings, restoring the
// stored OAuth tokens and wiring persistence for every refreshed token.
func (s *Server) zohoClient(ctx context.Context) (*zoho.Client, error) {
	cid, _ := s.Store.GetSetting(ctx, settingZohoClientID)
	secret, _ := s.Store.GetSetting(ctx, settingZohoClientSecret)
	if cid == "" || secret == "" {
		return nil, errZohoNotConfigured
	}
	dc, _ := s.Store.GetSetting(ctx, settingZohoDataCenter)
	if dc == "" {
		dc = "com"
	}
	c := zoho.New(dc, cid, secret)
	c.AccountID, _ = s.Store.GetSetting(ctx, settingZohoAccountID)
	c.AccessToken, _ = s.Store.GetSetting(ctx, settingZohoAccessToken)
	c.RefreshToken, _ = s.Store.GetSetting(ctx, settingZohoRefreshToken)
	if exp, err := time.Parse(time.RFC3339, mustSetting(ctx, s, settingZohoAccessExpiry)); err == nil {
		c.AccessExpiry = exp
	}
	c.SetTokenCallback(func(access string, expiry time.Time) {
		_ = s.Store.SetSetting(rctx(), settingZohoAccessToken, access)
		_ = s.Store.SetSetting(rctx(), settingZohoAccessExpiry, expiry.UTC().Format(time.RFC3339))
	})
	return c, nil
}

func mustSetting(ctx context.Context, s *Server, key string) string {
	v, _ := s.Store.GetSetting(ctx, key)
	return v
}

// zohoConnected reports whether an account has completed OAuth.
func (s *Server) zohoConnected(ctx context.Context) bool {
	return mustSetting(ctx, s, settingZohoRefreshToken) != ""
}

// mailRedirectURI derives the OAuth callback URI from the incoming
// request, so localhost and production both work without config. It must
// match a redirect URI registered in the Zoho API Console.
func mailRedirectURI(r *http.Request) string {
	scheme := "https"
	if fp := r.Header.Get("X-Forwarded-Proto"); fp != "" {
		scheme = strings.TrimSpace(strings.Split(fp, ",")[0])
	} else if strings.HasPrefix(r.Host, "localhost") || strings.HasPrefix(r.Host, "127.") || strings.HasPrefix(r.Host, "0.0.0.0") {
		scheme = "http"
	}
	return scheme + "://" + r.Host + "/admin/mail/oauth/callback"
}

// ---------------------------------------------------------------------------
// Page + OAuth handlers

// handleAdminMail renders the mail client (or the connect screen).
func (s *Server) handleAdminMail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cid, _ := s.Store.GetSetting(ctx, settingZohoClientID)
	secret, _ := s.Store.GetSetting(ctx, settingZohoClientSecret)
	dc, _ := s.Store.GetSetting(ctx, settingZohoDataCenter)
	email, _ := s.Store.GetSetting(ctx, settingZohoEmail)

	if !s.zohoConnected(ctx) || cid == "" || secret == "" {
		d := components.MailConnectData{
			ClientID:     cid,
			ClientSecret: secret,
			DataCenter:   dc,
			RedirectURI:  mailRedirectURI(r),
			Connected:    false,
			Email:        email,
		}
		s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Mail", Active: "mail"}, components.AdminMailConnect(d))
		return
	}

	c, err := s.zohoClient(ctx)
	if err != nil {
		s.renderMailConnectError(w, r, email, "Could not build the Zoho client: "+err.Error())
		return
	}
	folders, err := c.Folders(ctx)
	if err != nil {
		s.renderMailConnectError(w, r, email, "Could not load folders from Zoho: "+err.Error())
		return
	}

	active := r.URL.Query().Get("folder")
	if active == "" {
		active = components.MailFolderIDByType(folders, "Inbox")
	}
	start := atoiDefault(r.URL.Query().Get("start"), 1)
	msgs, err := c.ListMessages(ctx, active, start, mailPageSize, "all")
	if err != nil {
		s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Mail", Active: "mail"},
			components.AdminMailShell(components.MailListData{Folders: folders, ActiveFolder: active, Error: "Could not load messages: " + err.Error(), Email: email}))
		return
	}

	d := components.MailListData{
		Folders:          folders,
		ActiveFolder:     active,
		ActiveFolderName: components.MailFolderName(folders, active),
		Messages:         msgs,
		Start:            start,
		Limit:            mailPageSize,
		HasMore:          len(msgs) == mailPageSize,
		Email:            email,
	}
	s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Mail", Active: "mail"}, components.AdminMailShell(d))
}

// renderMailConnectError shows the connect screen with an error message
// (used when a connected account starts failing).
func (s *Server) renderMailConnectError(w http.ResponseWriter, r *http.Request, email, msg string) {
	cid, _ := s.Store.GetSetting(r.Context(), settingZohoClientID)
	secret, _ := s.Store.GetSetting(r.Context(), settingZohoClientSecret)
	dc, _ := s.Store.GetSetting(r.Context(), settingZohoDataCenter)
	s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Mail", Active: "mail"},
		components.AdminMailConnect(components.MailConnectData{
			ClientID:     cid,
			ClientSecret: secret,
			DataCenter:   dc,
			RedirectURI:  mailRedirectURI(r),
			Connected:    true,
			Email:        email,
			Error:        msg,
		}))
}

// handleAdminMailConnect saves the OAuth client credentials.
func (s *Server) handleAdminMailConnect(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	cid := strings.TrimSpace(r.FormValue("client_id"))
	secret := strings.TrimSpace(r.FormValue("client_secret"))
	dc := strings.TrimSpace(r.FormValue("data_center"))
	if dc == "" {
		dc = "com"
	}
	if cid == "" || secret == "" {
		http.Redirect(w, r, "/admin/mail?err="+url.QueryEscape("Client ID and Client Secret are required"), http.StatusSeeOther)
		return
	}
	_ = s.Store.SetSetting(ctx, settingZohoClientID, cid)
	_ = s.Store.SetSetting(ctx, settingZohoClientSecret, secret)
	_ = s.Store.SetSetting(ctx, settingZohoDataCenter, dc)
	s.TriggerBackup(ctx)
	http.Redirect(w, r, "/admin/mail/oauth/start", http.StatusSeeOther)
}

// handleAdminMailOAuthStart redirects the admin to Zoho's consent screen.
func (s *Server) handleAdminMailOAuthStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.zohoClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/admin/mail?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	state := randomHexToken(16)
	_ = s.Store.SetSetting(ctx, settingZohoOAuthState, state)
	authURL := c.AuthURL(mailRedirectURI(r), state, zohoMailScope, "offline")
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

// handleAdminMailOAuthCallback exchanges the authorization code for
// tokens and stores the account.
func (s *Server) handleAdminMailOAuthCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if e := r.URL.Query().Get("error"); e != "" {
		http.Redirect(w, r, "/admin/mail?err="+url.QueryEscape("Zoho denied access: "+e), http.StatusSeeOther)
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" {
		http.Redirect(w, r, "/admin/mail?err="+url.QueryEscape("No authorization code received"), http.StatusSeeOther)
		return
	}
	saved, _ := s.Store.GetSetting(ctx, settingZohoOAuthState)
	if saved == "" || subtle.ConstantTimeCompare([]byte(saved), []byte(state)) != 1 {
		http.Redirect(w, r, "/admin/mail?err="+url.QueryEscape("OAuth state mismatch — please try connecting again"), http.StatusSeeOther)
		return
	}

	c, err := s.zohoClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/admin/mail?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if err := c.ExchangeCode(ctx, code, mailRedirectURI(r)); err != nil {
		log.Printf("admin mail: token exchange: %v", err)
		http.Redirect(w, r, "/admin/mail?err="+url.QueryEscape("Token exchange failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	_ = s.Store.SetSetting(ctx, settingZohoRefreshToken, c.RefreshToken)

	accounts, err := c.Accounts(ctx)
	if err != nil {
		log.Printf("admin mail: fetch accounts: %v", err)
		http.Redirect(w, r, "/admin/mail?err="+url.QueryEscape("Connected, but could not list accounts: "+err.Error()), http.StatusSeeOther)
		return
	}
	for _, a := range accounts {
		if a.AccountID == "" {
			continue
		}
		_ = s.Store.SetSetting(ctx, settingZohoAccountID, a.AccountID)
		if a.EmailAddress != "" {
			_ = s.Store.SetSetting(ctx, settingZohoEmail, a.EmailAddress)
		}
		break
	}
	s.TriggerBackup(ctx)
	http.Redirect(w, r, "/admin/mail", http.StatusSeeOther)
}

// handleAdminMailDisconnect clears all Zoho credentials.
func (s *Server) handleAdminMailDisconnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	for _, k := range []string{
		settingZohoRefreshToken, settingZohoAccessToken, settingZohoAccessExpiry,
		settingZohoAccountID, settingZohoEmail, settingZohoOAuthState,
	} {
		_ = s.Store.SetSetting(ctx, k, "")
	}
	s.TriggerBackup(ctx)
	http.Redirect(w, r, "/admin/mail", http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// Small helpers

// mailPageSize is how many messages a list page shows.
const mailPageSize = 30

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
		return n
	}
	return def
}

// splitAddresses splits a comma-separated recipient list.
func splitAddresses(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// looksLikeHTML reports whether the body already contains raw HTML markup
// (in which case it is sent verbatim).
func looksLikeHTML(body string) bool {
	lower := strings.ToLower(body)
	for _, tag := range []string{"<p", "<div", "<br", "<a ", "<span", "<table", "<b>", "<i>", "<ul", "<li", "<img", "<h1", "<h2", "<h3"} {
		if strings.Contains(lower, tag) {
			return true
		}
	}
	return false
}

// mailBodyToHTML prepares the compose body for sending: raw HTML is passed
// through unchanged, everything else (plain text / Markdown) is rendered to
// formatted HTML so recipients always get a properly formatted message.
func mailBodyToHTML(body string) string {
	if looksLikeHTML(body) {
		return body
	}
	return renderMarkdown(body)
}

// contentDisposition builds a safe Content-Disposition header value.
func contentDisposition(filename string) string {
	filename = path.Base(filename)
	if filename == "" || filename == "." {
		return "attachment"
	}
	return "attachment; filename=\"" + strings.ReplaceAll(filename, "\"", "") + "\""
}
