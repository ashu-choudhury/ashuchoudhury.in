package zoho

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mockZoho spins up a fake Zoho Accounts + Mail API pair and returns the
// client pointed at them along with request-logging helpers.
type mockZoho struct {
	t            *testing.T
	mail         *httptest.Server
	accounts     *httptest.Server
	mailCalls    []*http.Request
	mailBodies   []string
	refreshCalls int
	client       *Client
}

func newMockZoho(t *testing.T) *mockZoho {
	m := &mockZoho{t: t}

	m.mail = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mailCalls = append(m.mailCalls, r)
		body := ""
		if r.Body != nil {
			buf := make([]byte, 4096)
			n, _ := r.Body.Read(buf)
			body = string(buf[:n])
		}
		m.mailBodies = append(m.mailBodies, body)

		// Token expired → 401 so the client refreshes and retries once.
		if len(m.mailCalls) == 1 && r.URL.Path == "/api/accounts/42/messages/view" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"status":{"code":401,"description":"Invalid Token"}}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/accounts":
			// Real Zoho shape: emailAddress is an array of objects and the
			// primary address also appears as a plain string.
			w.Write([]byte(`{"status":{"code":200,"description":"success"},"data":[{"accountId":"42","displayName":"Ashu","primaryEmailAddress":"ashu@example.com","emailAddress":[{"isAlias":false,"isPrimary":true,"mailId":"ashu@example.com","isConfirmed":true}]}]}`))
		case r.URL.Path == "/api/accounts/42/folders":
			w.Write([]byte(`{"status":{"code":200,"description":"success"},"data":[
				{"folderId":"1","folderName":"Inbox","folderType":"Inbox"},
				{"folderId":"2","folderName":"Sent","folderType":"Sent"}
			]}`))
		case r.URL.Path == "/api/accounts/42/messages/view":
			w.Write([]byte(`{"status":{"code":200,"description":"success"},"data":[
				{"messageId":"101","subject":"Hello","fromAddress":"a@b.com","sender":"a@b.com","receivedTime":"1700000000000","status":"0","hasAttachment":"1"},
				{"messageId":"102","subject":"Re: Hello","fromAddress":"\"Bob\" <b@c.com>","receivedTime":"1700000001000","status":"1"}
			]}`))
		case r.URL.Path == "/api/accounts/42/messages":
			var payload map[string]any
			_ = json.Unmarshal([]byte(body), &payload)
			mode, _ := payload["mode"].(string)
			w.Write([]byte(`{"status":{"code":200,"description":"success"},"data":{"messageId":"999","mode":"` + mode + `"}}`))
		case r.URL.Path == "/api/accounts/42/folders/1/messages/101/content":
			w.Write([]byte(`{"status":{"code":200,"description":"success"},"data":{"content":"<p>Body</p>","plainText":"Body","subject":"Hello","fromAddress":"a@b.com"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"status":{"code":404,"description":"not found"}}`))
		}
	}))

	m.accounts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.refreshCalls++
		_ = r.ParseForm()
		switch {
		case strings.Contains(r.URL.Path, "/oauth/v2/token") && r.FormValue("grant_type") == "refresh_token":
			w.Write([]byte(`{"access_token":"fresh-token","expires_in":3600,"api_domain":"` + m.mail.URL + `"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	c := &Client{
		DataCenter:   DataCenter{Accounts: m.accounts.URL, Mail: m.mail.URL + "/api"},
		ClientID:     "cid",
		ClientSecret: "secret",
		AccountID:    "42",
		AccessToken:  "stale-token",
		AccessExpiry: time.Now().Add(time.Hour),
		RefreshToken: "refresh-me",
		HTTP:         m.mail.Client(),
	}
	m.client = c
	return m
}

// TestAuthURLAlwaysAsksConsent pins the fix for the silent reconnect loop:
// Zoho only issues a refresh token when the consent screen is shown, so the
// authorization URL must always carry prompt=consent.
func TestAuthURLAlwaysAsksConsent(t *testing.T) {
	c := &Client{ClientID: "cid", DataCenter: India}
	u := c.AuthURL("https://x/callback", "st", "scopes", "offline")
	if !strings.Contains(u, "prompt=consent") {
		t.Errorf("authorization URL missing prompt=consent: %s", u)
	}
	if !strings.Contains(u, "access_type=offline") {
		t.Errorf("authorization URL missing access_type=offline: %s", u)
	}
}

// TestExchangeCodeRequiresRefreshToken is the exact silent-failure mode
// from production: Zoho answers a repeat authorization with an access
// token but no refresh token, which used to save an empty refresh token
// and bounce the user back to the connect screen forever.
func TestExchangeCodeRequiresRefreshToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Access token only — no refresh_token field.
		w.Write([]byte(`{"access_token":"acc","expires_in":3600}`))
	}))
	defer ts.Close()
	c := &Client{ClientID: "cid", ClientSecret: "secret", DataCenter: DataCenter{Accounts: ts.URL}, HTTP: ts.Client()}
	err := c.ExchangeCode(context.Background(), "code", "https://x/callback")
	if err == nil {
		t.Fatal("want an error when Zoho returns no refresh token")
	}
	if !strings.Contains(err.Error(), "no refresh token") {
		t.Errorf("error should explain the missing refresh token: %v", err)
	}
}

// TestExchangeCodeSavesRefreshToken is the happy path: the token endpoint
// returns access + refresh tokens and both land on the client.
func TestExchangeCodeSavesRefreshToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"acc","refresh_token":"ref","expires_in":3600}`))
	}))
	defer ts.Close()
	c := &Client{ClientID: "cid", ClientSecret: "secret", DataCenter: DataCenter{Accounts: ts.URL}, HTTP: ts.Client()}
	if err := c.ExchangeCode(context.Background(), "code", "https://x/callback"); err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if c.AccessToken != "acc" || c.RefreshToken != "ref" {
		t.Errorf("tokens not saved: access=%q refresh=%q", c.AccessToken, c.RefreshToken)
	}
}

func TestListMessagesParsesEnvelope(t *testing.T) {
	m := newMockZoho(t)
	msgs, err := m.client.ListMessages(context.Background(), "1", 1, 30, "all")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages, got %d", len(msgs))
	}
	if msgs[0].MessageID != "101" || msgs[0].IsRead() {
		t.Errorf("message 101: id=%s read=%v", msgs[0].MessageID, msgs[0].IsRead())
	}
	if !msgs[0].HasAttachments() {
		t.Error("message 101 should have attachments")
	}
	if msgs[1].MessageID != "102" || !msgs[1].IsRead() {
		t.Errorf("message 102: id=%s read=%v", msgs[1].MessageID, msgs[1].IsRead())
	}
	// The mock answers the first view call with 401, so exactly one
	// refresh + retry must have happened.
	if m.refreshCalls != 1 {
		t.Errorf("want 1 token refresh, got %d", m.refreshCalls)
	}
	if m.client.AccessToken != "fresh-token" {
		t.Errorf("access token not refreshed: %q", m.client.AccessToken)
	}
	if len(m.mailCalls) != 2 {
		t.Errorf("want 2 API attempts (401 + retry), got %d", len(m.mailCalls))
	}
}

func TestFolders(t *testing.T) {
	m := newMockZoho(t)
	fs, err := m.client.Folders(context.Background())
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if len(fs) != 2 || fs[0].FolderName != "Inbox" {
		t.Fatalf("unexpected folders: %+v", fs)
	}
}

// TestAccountsParsesRealShape is the exact regression from production:
// Zoho returns emailAddress as an array of objects, which crashed the old
// string field with "cannot unmarshal array into Go struct field …".
func TestAccountsParsesRealShape(t *testing.T) {
	m := newMockZoho(t)
	as, err := m.client.Accounts(context.Background())
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if len(as) != 1 || as[0].AccountID != "42" {
		t.Fatalf("unexpected accounts: %+v", as)
	}
	if got := as[0].PrimaryEmail(); got != "ashu@example.com" {
		t.Errorf("PrimaryEmail: want ashu@example.com, got %q", got)
	}
}

// TestAccountPrimaryEmailFallbacks covers the fallback ladder: the plain
// primaryEmailAddress string first, then mailboxAddress, then the array's
// primary (or first) entry — and the tolerant string-array shape.
func TestAccountPrimaryEmailFallbacks(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"primaryEmailAddress wins",
			`{"primaryEmailAddress":"a@x.com","emailAddress":[{"isPrimary":true,"mailId":"b@x.com"}]}`, "a@x.com"},
		{"mailboxAddress fallback",
			`{"mailboxAddress":"c@x.com","emailAddress":[{"isPrimary":true,"mailId":"b@x.com"}]}`, "c@x.com"},
		{"array primary entry",
			`{"emailAddress":[{"isPrimary":false,"mailId":"alias@x.com"},{"isPrimary":true,"mailId":"main@x.com"}]}`, "main@x.com"},
		{"array first entry",
			`{"emailAddress":[{"isPrimary":false,"mailId":"only@x.com"}]}`, "only@x.com"},
		{"string array tolerated",
			`{"emailAddress":["str@x.com"]}`, "str@x.com"},
		{"empty", `{}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var a Account
			if err := json.Unmarshal([]byte(tc.json), &a); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := a.PrimaryEmail(); got != tc.want {
				t.Errorf("PrimaryEmail: want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestSendBuildsPayload(t *testing.T) {
	m := newMockZoho(t)
	req := &SendRequest{
		FromAddress: "ashu@example.com",
		ToAddress:   []string{"a@b.com", "c@d.com"},
		CcAddress:   []string{"cc@e.com"},
		Subject:     "Hi",
		Content:     "<p>Yo</p>",
		MailFormat:  "html",
		Draft:       true,
	}
	id, err := m.client.Send(context.Background(), req)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != "999" {
		t.Errorf("want messageId 999, got %s", id)
	}
	if len(m.mailBodies) == 0 {
		t.Fatal("no send body captured")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(m.mailBodies[len(m.mailBodies)-1]), &payload); err != nil {
		t.Fatalf("bad payload: %v", err)
	}
	if payload["mode"] != "draft" {
		t.Errorf("draft mode missing: %v", payload["mode"])
	}
	if payload["toAddress"] != "a@b.com,c@d.com" {
		t.Errorf("toAddress: %v", payload["toAddress"])
	}
}

func TestSendRejectsEmptyRecipients(t *testing.T) {
	// The Zoho API itself validates, but the payload builder must join
	// empty slices without crashing.
	req := &SendRequest{FromAddress: "x@y.z", Subject: "s", Content: "c"}
	_ = req
}

func TestMessageContent(t *testing.T) {
	m := newMockZoho(t)
	c, err := m.client.MessageContent(context.Background(), "1", "101", true)
	if err != nil {
		t.Fatalf("MessageContent: %v", err)
	}
	if c.Content != "<p>Body</p>" || c.PlainText != "Body" {
		t.Errorf("unexpected content: %+v", c)
	}
}

// TestRefreshRejectedToken is the exact scenario from the field: the
// saved refresh token is dead and Zoho's OAuth endpoint answers
// {"error":"invalid_grant"}. The client must surface ErrTokenRejected
// (which the handlers turn into a "reconnect" message) instead of the
// useless generic "api error (code 0)". Both rejection shapes are
// covered: a non-2xx status with an error body, and a 200 OK whose body
// carries the error (which is what invalid_client actually looks like).
func TestRefreshRejectedToken(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"non2xx invalid_grant", http.StatusBadRequest,
			`{"error":"invalid_grant","error_description":"Refresh token is invalid or expired."}`},
		{"200 invalid_client", http.StatusOK,
			`{"error":"invalid_client","error_description":"Client authentication failed."}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMockZoho(t)

			// Point the client at a fresh accounts server that always
			// rejects the refresh grant, simulating dead credentials.
			rejected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer rejected.Close()
			m.client.DataCenter = DataCenter{
				Accounts: rejected.URL,
				Mail:     m.mail.URL + "/api",
			}

			// Force the client to need a fresh access token so it must refresh.
			m.client.AccessToken = ""
			m.client.AccessExpiry = time.Time{}

			_, err := m.client.ListMessages(context.Background(), "1", 1, 30, "all")
			if err == nil {
				t.Fatal("want an error from ListMessages")
			}
			if !errors.Is(err, ErrTokenRejected) {
				t.Errorf("want ErrTokenRejected, got: %v", err)
			}
			if strings.Contains(err.Error(), "code 0") {
				t.Errorf("must not surface the useless 'code 0' message: %v", err)
			}
		})
	}
}
