// Package zoho implements a minimal, dependency-free client for the
// Zoho Mail REST API: OAuth 2.0 authentication (authorization code flow
// with refresh-token rotation) plus the mailbox operations needed to
// build a full email client — folders, message lists, search, content,
// send/draft/reply, message state changes and attachments.
//
// Endpoints and payload shapes follow the official Zoho Mail API guide
// (https://www.zoho.com/mail/help/api/) with the quirks that matter in
// practice baked in:
//
//   - the Authorization header is "Zoho-oauthtoken <token>", not Bearer;
//   - every JSON response is wrapped in {"status":{...},"data":...};
//   - sending an email and saving a draft are the same POST — the only
//     difference is "mode": "draft" in the body;
//   - message "status" is "1" = read, "0" = unread;
//   - recipient fields are comma-joined strings.
package zoho

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DataCenter describes one Zoho region: its accounts (OAuth) host and
// its Mail API base URL.
type DataCenter struct {
	Code     string // form value: "com", "eu", "in", ...
	Accounts string // OAuth host, e.g. https://accounts.zoho.com
	Mail     string // API base, e.g. https://mail.zoho.com/api
}

// DataCenters is the full set of Zoho data centers. The Code values are
// what the user picks in the admin connect screen.
var DataCenters = map[string]DataCenter{
	"com":    {Code: "com", Accounts: "https://accounts.zoho.com", Mail: "https://mail.zoho.com/api"},
	"eu":     {Code: "eu", Accounts: "https://accounts.zoho.eu", Mail: "https://mail.zoho.eu/api"},
	"in":     {Code: "in", Accounts: "https://accounts.zoho.in", Mail: "https://mail.zoho.in/api"},
	"com.au": {Code: "com.au", Accounts: "https://accounts.zoho.com.au", Mail: "https://mail.zoho.com.au/api"},
	"jp":     {Code: "jp", Accounts: "https://accounts.zoho.jp", Mail: "https://mail.zoho.jp/api"},
	"ca":     {Code: "ca", Accounts: "https://accounts.zoho.ca", Mail: "https://mail.zohocloud.ca/api"},
	"sa":     {Code: "sa", Accounts: "https://accounts.zoho.sa", Mail: "https://mail.zoho.sa/api"},
	"com.cn": {Code: "com.cn", Accounts: "https://accounts.zoho.com.cn", Mail: "https://mail.zoho.com.cn/api"},
}

// DC returns the DataCenter for a code, defaulting to the US one.
func DC(code string) DataCenter {
	if d, ok := DataCenters[code]; ok {
		return d
	}
	return DataCenters["com"]
}

// APIError is a Zoho error envelope: {"status":{"code":N,...},"data":{"moreInfo":"..."}}.
type APIError struct {
	Code     int
	Message  string // HTTP-level description
	MoreInfo string // Zoho-level detail
}

func (e *APIError) Error() string {
	if e.MoreInfo != "" {
		return fmt.Sprintf("zoho: %s (code %d)", e.MoreInfo, e.Code)
	}
	if e.Message != "" {
		return fmt.Sprintf("zoho: %s (code %d)", e.Message, e.Code)
	}
	return fmt.Sprintf("zoho: api error (code %d)", e.Code)
}

// Account is one mailbox account in the user's Zoho profile.
type Account struct {
	AccountID        string `json:"accountId"`
	DisplayName      string `json:"displayName"`
	EmailAddress     string `json:"emailAddress"`
	AccountType      string `json:"accountType"`
	IsMailboxCreated bool   `json:"isMailboxCreated"`
	IsMailboxEnabled bool   `json:"isMailboxEnabled"`
}

// Folder is a mailbox folder (Inbox, Sent, Drafts, Spam, Trash, custom…).
type Folder struct {
	FolderID       string `json:"folderId"`
	FolderName     string `json:"folderName"`
	FolderType     string `json:"folderType"`
	IsSystemFolder bool   `json:"isSystemFolder"`
	UnreadCount    string `json:"unreadCount"`
	TotalCount     string `json:"totalCount"`
}

// MessageSummary is one row of a folder listing or search result.
type MessageSummary struct {
	MessageID      string       `json:"messageId"`
	Subject        string       `json:"subject"`
	FromAddress    string       `json:"fromAddress"`
	Sender         string       `json:"sender"`
	ToAddress      string       `json:"toAddress"`
	CcAddress      string       `json:"ccAddress"`
	Summary        string       `json:"summary"`
	ReceivedTime   string       `json:"receivedTime"` // epoch millis (server-side, reliable)
	SentDate       string       `json:"sentDateInGMT"`
	HasAttachment  string       `json:"hasAttachment"` // "0" / "1"
	Status         string       `json:"status"`        // "1" read, "0" unread
	FolderID       string       `json:"folderId"`
	Size           string       `json:"size"`
	ThreadID       string       `json:"threadId"`
	FlagID         string       `json:"flagid"`
	Priority       string       `json:"priority"`
	LabelID        string       `json:"labelId"`
	AttachmentInfo []Attachment `json:"attachmentInfo"`
}

// Attachment is a file attached to a message.
type Attachment struct {
	AttachmentID   string `json:"attachmentId"`
	AttachmentName string `json:"attachmentName"`
	AttachmentSize string `json:"attachmentSize"`
}

// MessageContent is the full body of one message.
type MessageContent struct {
	Content      string `json:"content"`
	PlainText    string `json:"plainText"`
	Subject      string `json:"subject"`
	Sender       string `json:"sender"`
	FromAddress  string `json:"fromAddress"`
	ToAddress    string `json:"toAddress"`
	CcAddress    string `json:"ccAddress"`
	BccAddress   string `json:"bccAddress"`
	ReceivedTime string `json:"receivedTime"`
	SentDate     string `json:"sentDateInGMT"`
	MessageID    string `json:"messageId"`
	Size         string `json:"size"`
}

// MessageDetails is the metadata of one message (headers etc.).
type MessageDetails struct {
	MessageID    string `json:"messageId"`
	Subject      string `json:"subject"`
	FromAddress  string `json:"fromAddress"`
	ToAddress    string `json:"toAddress"`
	CcAddress    string `json:"ccAddress"`
	ReceivedTime string `json:"receivedTime"`
	SentDate     string `json:"sentDateInGMT"`
	Size         string `json:"size"`
	FlagID       string `json:"flagid"`
}

// SendRequest is the payload for send / save-draft / reply.
type SendRequest struct {
	FromAddress string
	ToAddress   []string
	CcAddress   []string
	BccAddress  []string
	Subject     string
	Content     string // HTML (mailFormat=html) or plain text
	MailFormat  string // "html" or "plaintext"
	Draft       bool   // true → save as draft instead of sending
	DraftID     string // set when updating an existing draft
	Attachments []Attachment
	ReplyTo     string
}

// UploadFile is a client-supplied file to attach to a draft/message.
type UploadFile struct {
	Name    string
	Content io.Reader
}

// Client talks to one Zoho Mail account.
//
// Token persistence is the caller's job: set SetTokenCallback to receive
// every new access token (and its expiry), and restore AccessToken /
// AccessExpiry / RefreshToken on construction. Access tokens live one
// hour; the client refreshes automatically (once per request) when one
// expires or Zoho answers 401/440.
type Client struct {
	DataCenter   DataCenter
	ClientID     string
	ClientSecret string
	AccountID    string

	AccessToken  string
	AccessExpiry time.Time
	RefreshToken string

	HTTP *http.Client

	mu         sync.Mutex
	onTokens   func(accessToken string, expiry time.Time)
	refreshing bool
}

// New builds a client for the given data-center code and OAuth app.
func New(dcCode, clientID, clientSecret string) *Client {
	return &Client{
		DataCenter:   DC(dcCode),
		ClientID:     clientID,
		ClientSecret: clientSecret,
		HTTP:         &http.Client{Timeout: 60 * time.Second},
	}
}

// SetTokenCallback registers a hook invoked whenever a fresh access
// token is obtained (initial exchange or refresh) so callers can persist
// it. The callback runs under the client's internal lock.
func (c *Client) SetTokenCallback(fn func(accessToken string, expiry time.Time)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onTokens = fn
}

// HasRefreshToken reports whether the client can refresh itself.
func (c *Client) HasRefreshToken() bool {
	return c.RefreshToken != ""
}

// ---------------------------------------------------------------------------
// OAuth 2.0

// AuthURL builds the browser authorization URL for the given redirect
// URI. scope is the space/comma separated Zoho scope list; access_type
// is "offline" to receive a refresh token.
func (c *Client) AuthURL(redirectURI, state, scope, accessType string) string {
	q := url.Values{}
	q.Set("client_id", c.ClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", scope)
	if accessType != "" {
		q.Set("access_type", accessType)
	}
	q.Set("prompt", "consent")
	if state != "" {
		q.Set("state", state)
	}
	return c.DataCenter.Accounts + "/oauth/v2/auth?" + q.Encode()
}

// tokenResponse is the OAuth token endpoint payload.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	APIDomain    string `json:"api_domain"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

// ExchangeCode trades an authorization code for access + refresh tokens.
func (c *Client) ExchangeCode(ctx context.Context, code, redirectURI string) error {
	form := url.Values{}
	form.Set("code", code)
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.DataCenter.Accounts+"/oauth/v2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var tr tokenResponse
	if err := doJSON(c.HTTP, req, &tr); err != nil {
		return err
	}
	if tr.AccessToken == "" {
		return fmt.Errorf("zoho: token exchange failed: %s", tr.Error)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		c.RefreshToken = tr.RefreshToken
	}
	c.AccessExpiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	if c.onTokens != nil {
		c.onTokens(c.AccessToken, c.AccessExpiry)
	}
	return nil
}

// refresh fetches a new access token with the refresh token.
func (c *Client) refresh(ctx context.Context) error {
	c.mu.Lock()
	refreshToken := c.RefreshToken
	c.mu.Unlock()
	if refreshToken == "" {
		return fmt.Errorf("zoho: no refresh token — re-connect the account")
	}

	form := url.Values{}
	form.Set("refresh_token", refreshToken)
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.DataCenter.Accounts+"/oauth/v2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var tr tokenResponse
	if err := doJSON(c.HTTP, req, &tr); err != nil {
		return err
	}
	if tr.AccessToken == "" {
		return fmt.Errorf("zoho: token refresh failed: %s", tr.Error)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.AccessToken = tr.AccessToken
	c.AccessExpiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	if c.onTokens != nil {
		c.onTokens(c.AccessToken, c.AccessExpiry)
	}
	return nil
}

// ensureToken makes sure a non-expired access token is available.
func (c *Client) ensureToken(ctx context.Context) error {
	c.mu.Lock()
	ok := c.AccessToken != "" && time.Now().Before(c.AccessExpiry.Add(-30*time.Second))
	c.mu.Unlock()
	if ok {
		return nil
	}
	c.mu.Lock()
	if c.refreshing {
		c.mu.Unlock()
		// Another goroutine is refreshing; wait briefly and re-check.
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			c.mu.Lock()
			done := c.AccessToken != "" && time.Now().Before(c.AccessExpiry.Add(-30*time.Second))
			c.mu.Unlock()
			if done {
				return nil
			}
		}
		return fmt.Errorf("zoho: timed out waiting for token refresh")
	}
	c.refreshing = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.refreshing = false
		c.mu.Unlock()
	}()
	return c.refresh(ctx)
}

// ---------------------------------------------------------------------------
// HTTP plumbing

// envelope is the common {"status":…,"data":…} wrapper.
type envelope struct {
	Status struct {
		Code        int    `json:"code"`
		Description string `json:"description"`
	} `json:"status"`
	Data json.RawMessage `json:"data"`
}

// doJSON performs a JSON request and decodes the response body into out
// (which may be a plain struct for the token endpoint or an *envelope
// consumer). It returns a parsed *APIError on non-2xx responses.
func doJSON(hc *http.Client, req *http.Request, out any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Status struct {
				Code        int    `json:"code"`
				Description string `json:"description"`
			} `json:"status"`
			Data struct {
				MoreInfo string `json:"moreInfo"`
			} `json:"data"`
		}
		_ = json.Unmarshal(body, &e)
		return &APIError{Code: e.Status.Code, Message: e.Status.Description, MoreInfo: e.Data.MoreInfo}
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

// apiGet performs an authenticated GET and decodes the "data" payload
// into out. It transparently refreshes the token and retries once on
// 401/440.
func (c *Client) apiGet(ctx context.Context, path string, query url.Values, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.ensureToken(ctx); err != nil {
			return err
		}
		u := c.DataCenter.Mail + path
		if query != nil {
			u += "?" + query.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Zoho-oauthtoken "+c.AccessToken)
		req.Header.Set("Accept", "application/json")

		resp, err := c.HTTP.Do(req)
		if err != nil {
			return err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == 440 {
			if err := c.refresh(ctx); err != nil {
				return err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var e struct {
				Status struct {
					Code        int    `json:"code"`
					Description string `json:"description"`
				} `json:"status"`
				Data struct {
					MoreInfo string `json:"moreInfo"`
				} `json:"data"`
			}
			_ = json.Unmarshal(body, &e)
			return &APIError{Code: e.Status.Code, Message: e.Status.Description, MoreInfo: e.Data.MoreInfo}
		}
		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			return fmt.Errorf("zoho: bad response from %s: %w", path, err)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(env.Data, out)
	}
	return fmt.Errorf("zoho: token refresh did not resolve authentication")
}

// apiSend performs an authenticated request with a JSON body and decodes
// the "data" payload into out (nil to discard).
func (c *Client) apiSend(ctx context.Context, method, path string, query url.Values, payload any, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.ensureToken(ctx); err != nil {
			return err
		}
		u := c.DataCenter.Mail + path
		if query != nil {
			u += "?" + query.Encode()
		}
		var body io.Reader
		var contentType string
		if payload != nil {
			b, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			body = bytes.NewReader(b)
			contentType = "application/json"
		}
		req, err := http.NewRequestWithContext(ctx, method, u, body)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Zoho-oauthtoken "+c.AccessToken)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.HTTP.Do(req)
		if err != nil {
			return err
		}
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == 440 {
			if err := c.refresh(ctx); err != nil {
				return err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var e struct {
				Status struct {
					Code        int    `json:"code"`
					Description string `json:"description"`
				} `json:"status"`
				Data struct {
					MoreInfo string `json:"moreInfo"`
				} `json:"data"`
			}
			_ = json.Unmarshal(respBody, &e)
			return &APIError{Code: e.Status.Code, Message: e.Status.Description, MoreInfo: e.Data.MoreInfo}
		}
		var env envelope
		if err := json.Unmarshal(respBody, &env); err != nil {
			return fmt.Errorf("zoho: bad response from %s: %w", path, err)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(env.Data, out)
	}
	return fmt.Errorf("zoho: token refresh did not resolve authentication")
}

// rawGet performs an authenticated GET and returns the raw bytes
// (used for attachment downloads).
func (c *Client) rawGet(ctx context.Context, path string, query url.Values) ([]byte, string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.ensureToken(ctx); err != nil {
			return nil, "", err
		}
		u := c.DataCenter.Mail + path
		if query != nil {
			u += "?" + query.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("Authorization", "Zoho-oauthtoken "+c.AccessToken)

		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, "", err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if readErr != nil {
			return nil, "", readErr
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == 440 {
			if err := c.refresh(ctx); err != nil {
				return nil, "", err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var e struct {
				Status struct {
					Code        int    `json:"code"`
					Description string `json:"description"`
				} `json:"status"`
				Data struct {
					MoreInfo string `json:"moreInfo"`
				} `json:"data"`
			}
			_ = json.Unmarshal(body, &e)
			return nil, "", &APIError{Code: e.Status.Code, Message: e.Status.Description, MoreInfo: e.Data.MoreInfo}
		}
		return body, resp.Header.Get("Content-Disposition"), nil
	}
	return nil, "", fmt.Errorf("zoho: token refresh did not resolve authentication")
}

// ---------------------------------------------------------------------------
// Mailbox operations

// Accounts lists the user's mail accounts; callers pick the accountId.
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	var out []Account
	if err := c.apiGet(ctx, "/accounts", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Folders lists every folder in the mailbox (Inbox, Sent, Drafts, Spam,
// Trash, Templates, custom folders, …).
func (c *Client) Folders(ctx context.Context) ([]Folder, error) {
	path := "/accounts/" + c.AccountID + "/folders"
	var out []Folder
	if err := c.apiGet(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListMessages returns one page of messages in a folder.
// start is 1-based; limit is capped at 200 by Zoho; status is
// "read", "unread" or "all" (default all).
func (c *Client) ListMessages(ctx context.Context, folderID string, start, limit int, status string) ([]MessageSummary, error) {
	q := url.Values{}
	if folderID != "" {
		q.Set("folderId", folderID)
	}
	if start > 1 {
		q.Set("start", strconv.Itoa(start))
	}
	if limit > 0 {
		if limit > 200 {
			limit = 200
		}
		q.Set("limit", strconv.Itoa(limit))
	}
	if status != "" && status != "all" {
		q.Set("status", status)
	}
	q.Set("includeto", "true")
	var out []MessageSummary
	if err := c.apiGet(ctx, "/accounts/"+c.AccountID+"/messages/view", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SearchMessages queries the mailbox with Zoho's search syntax, e.g.
// "from:someone@x.com subject:hello" or "in:Spam".
func (c *Client) SearchMessages(ctx context.Context, query string) ([]MessageSummary, error) {
	q := url.Values{}
	q.Set("searchKey", query)
	q.Set("includeto", "true")
	var out []MessageSummary
	if err := c.apiGet(ctx, "/accounts/"+c.AccountID+"/messages/search", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// MessageContent fetches the body of one message. When html is true the
// HTML representation is preferred (content field); otherwise the plain
// text.
func (c *Client) MessageContent(ctx context.Context, folderID, messageID string, html bool) (*MessageContent, error) {
	q := url.Values{}
	if html {
		q.Set("mode", "html")
	}
	path := "/accounts/" + c.AccountID + "/folders/" + folderID + "/messages/" + messageID + "/content"
	var out MessageContent
	if err := c.apiGet(ctx, path, q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MessageDetails fetches the metadata of one message.
func (c *Client) MessageDetails(ctx context.Context, folderID, messageID string) (*MessageDetails, error) {
	path := "/accounts/" + c.AccountID + "/folders/" + folderID + "/messages/" + messageID + "/details"
	var out MessageDetails
	if err := c.apiGet(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AttachmentInfo lists the attachments of one message.
func (c *Client) AttachmentInfo(ctx context.Context, folderID, messageID string) ([]Attachment, error) {
	path := "/accounts/" + c.AccountID + "/folders/" + folderID + "/messages/" + messageID + "/attachmentinfo"
	var raw struct {
		Attachment  []Attachment `json:"attachment"`
		Attachments []Attachment `json:"attachments"`
	}
	if err := c.apiGet(ctx, path, nil, &raw); err != nil {
		return nil, err
	}
	if len(raw.Attachment) > 0 {
		return raw.Attachment, nil
	}
	return raw.Attachments, nil
}

// DownloadAttachment returns the bytes and suggested filename of one
// attachment.
func (c *Client) DownloadAttachment(ctx context.Context, folderID, messageID, attachmentID string) ([]byte, string, error) {
	path := "/accounts/" + c.AccountID + "/folders/" + folderID + "/messages/" + messageID + "/attachments/" + attachmentID
	body, disposition, err := c.rawGet(ctx, path, nil)
	if err != nil {
		return nil, "", err
	}
	name := ""
	if i := strings.Index(disposition, "filename="); i >= 0 {
		name = strings.Trim(strings.TrimSpace(disposition[i+len("filename="):]), `";'`)
		if u, err := url.QueryUnescape(name); err == nil {
			name = u
		}
	}
	return body, name, nil
}

// InlineImage downloads an inline (cid:) image embedded in a message.
func (c *Client) InlineImage(ctx context.Context, folderID, messageID, ref string) ([]byte, error) {
	q := url.Values{}
	q.Set("attachmentId", ref)
	path := "/accounts/" + c.AccountID + "/folders/" + folderID + "/messages/" + messageID + "/inline"
	body, _, err := c.rawGet(ctx, path, q)
	if err != nil {
		// Fall back to treating the reference as an attachment id.
		body, _, err = c.rawGet(ctx, "/accounts/"+c.AccountID+"/folders/"+folderID+"/messages/"+messageID+"/attachments/"+ref, nil)
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}

// sendPayload is the JSON body of the send / draft / reply endpoint.
type sendPayload struct {
	FromAddress string       `json:"fromAddress,omitempty"`
	ToAddress   string       `json:"toAddress,omitempty"`
	CcAddress   string       `json:"ccAddress,omitempty"`
	BccAddress  string       `json:"bccAddress,omitempty"`
	Subject     string       `json:"subject,omitempty"`
	Content     string       `json:"content,omitempty"`
	MailFormat  string       `json:"mailFormat,omitempty"`
	Encoding    string       `json:"encoding,omitempty"`
	AskReceipt  string       `json:"askReceipt,omitempty"`
	Mode        string       `json:"mode,omitempty"`
	DraftID     string       `json:"draftId,omitempty"`
	ReplyTo     string       `json:"replyTo,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

func (c *Client) buildPayload(req *SendRequest) sendPayload {
	format := req.MailFormat
	if format == "" {
		format = "html"
	}
	p := sendPayload{
		FromAddress: req.FromAddress,
		ToAddress:   strings.Join(req.ToAddress, ","),
		CcAddress:   strings.Join(req.CcAddress, ","),
		BccAddress:  strings.Join(req.BccAddress, ","),
		Subject:     req.Subject,
		Content:     req.Content,
		MailFormat:  format,
		Encoding:    "utf-8",
		AskReceipt:  "no",
		Attachments: req.Attachments,
		ReplyTo:     req.ReplyTo,
	}
	if req.Draft {
		p.Mode = "draft"
		p.DraftID = req.DraftID
	}
	return p
}

// Send delivers a message (or saves it as a draft when req.Draft) and
// returns the new messageId.
func (c *Client) Send(ctx context.Context, req *SendRequest) (string, error) {
	var out struct {
		MessageID string `json:"messageId"`
	}
	if err := c.apiSend(ctx, http.MethodPost, "/accounts/"+c.AccountID+"/messages", nil, c.buildPayload(req), &out); err != nil {
		return "", err
	}
	return out.MessageID, nil
}

// Reply sends a reply or reply-all to an existing message. action is
// "reply" or "replyall". Zoho builds the quoted original server-side.
func (c *Client) Reply(ctx context.Context, messageID, action string, req *SendRequest) (string, error) {
	q := url.Values{}
	q.Set("action", action)
	var out struct {
		MessageID string `json:"messageId"`
	}
	path := "/accounts/" + c.AccountID + "/messages/" + messageID
	if err := c.apiSend(ctx, http.MethodPost, path, q, c.buildPayload(req), &out); err != nil {
		return "", err
	}
	return out.MessageID, nil
}

// UpdateMessages changes message state. Valid modes: markAsRead,
// markAsUnread, moveMessage (needs destFolderID), archive, unarchive,
// spam, notspam, trash, untrash, flag, unflag. Zoho answers success even
// for unknown ids, so errors here are only transport/validation errors.
func (c *Client) UpdateMessages(ctx context.Context, mode string, messageIDs []string, destFolderID string) error {
	payload := map[string]any{
		"mode":      mode,
		"messageId": messageIDs,
	}
	if destFolderID != "" {
		payload["destfolderId"] = destFolderID
	}
	return c.apiSend(ctx, http.MethodPut, "/accounts/"+c.AccountID+"/updatemessage", nil, payload, nil)
}

// DeleteMessage hard-deletes one message from a folder.
func (c *Client) DeleteMessage(ctx context.Context, folderID, messageID string) error {
	path := "/accounts/" + c.AccountID + "/folders/" + folderID + "/messages/" + messageID
	return c.apiSend(ctx, http.MethodDelete, path, nil, nil, nil)
}

// UploadAttachments uploads files to Zoho's temporary attachment store;
// the returned refs can then be passed in a SendRequest.
func (c *Client) UploadAttachments(ctx context.Context, files []UploadFile) ([]Attachment, error) {
	if len(files) == 0 {
		return nil, nil
	}
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range files {
		fw, err := mw.CreateFormFile("attachments", f.Name)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(fw, f.Content); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.DataCenter.Mail+"/accounts/"+c.AccountID+"/messages/attachments", &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Zoho-oauthtoken "+c.AccessToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == 440 {
		if err := c.refresh(ctx); err != nil {
			return nil, err
		}
		return c.UploadAttachments(ctx, files)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Status struct {
				Code        int    `json:"code"`
				Description string `json:"description"`
			} `json:"status"`
			Data struct {
				MoreInfo string `json:"moreInfo"`
			} `json:"data"`
		}
		_ = json.Unmarshal(body, &e)
		return nil, &APIError{Code: e.Status.Code, Message: e.Status.Description, MoreInfo: e.Data.MoreInfo}
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("zoho: bad attachment upload response: %w", err)
	}
	var out []Attachment
	if err := json.Unmarshal(env.Data, &out); err != nil {
		var alt struct {
			Attachments []Attachment `json:"attachments"`
		}
		if err2 := json.Unmarshal(env.Data, &alt); err2 == nil && len(alt.Attachments) > 0 {
			return alt.Attachments, nil
		}
		return nil, fmt.Errorf("zoho: unexpected attachment upload response: %w", err)
	}
	return out, nil
}

// ReceivedAt parses the epoch-millis receivedTime field into a time.
func (s *MessageSummary) ReceivedAt() time.Time {
	ms, _ := strconv.ParseInt(s.ReceivedTime, 10, 64)
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// IsRead reports whether the message has been read ("1").
func (s *MessageSummary) IsRead() bool { return s.Status == "1" }

// HasAttachments reports whether the summary flags attachments.
func (s *MessageSummary) HasAttachments() bool { return s.HasAttachment == "1" }
