package handlers

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ashu-choudhury/portfolio/store"
	"github.com/ashu-choudhury/portfolio/zoho"
)

func seedMockZohoCredentials(t *testing.T, ms *store.Memory, mockAccountsURL, mockMailURL string) {
	t.Helper()
	ctx := context.Background()
	_ = ms.SetSetting(ctx, settingZohoClientID, "test-client-id")
	_ = ms.SetSetting(ctx, settingZohoClientSecret, "test-client-secret")
	_ = ms.SetSetting(ctx, settingZohoRefreshToken, "test-refresh-token")
	_ = ms.SetSetting(ctx, settingZohoAccessToken, "test-access-token")
	_ = ms.SetSetting(ctx, settingZohoAccessExpiry, "2030-01-01T00:00:00Z")
	_ = ms.SetSetting(ctx, settingZohoAccountID, "42")
	_ = ms.SetSetting(ctx, settingZohoEmail, "me@ashuchoudhury.in")
	zoho.India = zoho.DataCenter{
		Accounts: mockAccountsURL,
		Mail:     mockMailURL + "/api",
	}
}

func TestAdminMailEndpointsE2E(t *testing.T) {
	ms, s := newAdminTestServer(t)
	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/oauth/v2/token"):
			w.Write([]byte(`{"access_token":"test-access-token","expires_in":3600}`))
		case r.URL.Path == "/api/accounts":
			w.Write([]byte(`{"status":{"code":200,"description":"success"},"data":[{"accountId":"42","primaryEmailAddress":"me@ashuchoudhury.in"}]}`))
		case r.URL.Path == "/api/accounts/42/folders":
			w.Write([]byte(`{"status":{"code":200,"description":"success"},"data":[{"folderId":"1","folderName":"Inbox","folderType":"Inbox"},{"folderId":"2","folderName":"Sent","folderType":"Sent"}]}`))
		case r.URL.Path == "/api/accounts/42/messages/view":
			w.Write([]byte(`{"status":{"code":200,"description":"success"},"data":[{"messageId":"101","subject":"Test Email","fromAddress":"sender@example.com","receivedTime":"1700000000000","status":"0"}]}`))
		case r.URL.Path == "/api/accounts/42/folders/1/messages/101/content":
			w.Write([]byte(`{"status":{"code":200,"description":"success"},"data":{"messageId":101,"content":"<p>Hello world</p>","plainText":"Hello world"}}`))
		case r.URL.Path == "/api/accounts/42/folders/1/messages/101/details":
			w.Write([]byte(`{"status":{"code":200,"description":"success"},"data":{"subject":"Test Email","fromAddress":"sender@example.com","toAddress":"me@ashuchoudhury.in","receivedTime":"1700000000000"}}`))
		case r.URL.Path == "/api/accounts/42/messages/attachments":
			w.Write([]byte(`{"status":{"code":200,"description":"success"},"data":[{"attachmentId":"att99","attachmentName":"hello.txt"}]}`))
		case r.URL.Path == "/api/accounts/42/messages":
			w.Write([]byte(`{"status":{"code":200,"description":"success"},"data":{"messageId":"201"}}`))
		case r.URL.Path == "/api/accounts/42/messages/updatemessage":
			w.Write([]byte(`{"status":{"code":200,"description":"success"},"data":"updated"}`))
		default:
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":{"code":200,"description":"success"}}`))
		}
	}))
	defer mockSrv.Close()

	seedMockZohoCredentials(t, ms, mockSrv.URL, mockSrv.URL)

	h := s.Handler()

	// 1. GET /admin/mail (Inbox list)
	req := httptest.NewRequest("GET", "/admin/mail", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "admin-session"})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-token-123"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /admin/mail returned status %d, body: %s", rr.Code, rr.Body.String())
	}

	// 2. GET /admin/mail/folder/1
	req = httptest.NewRequest("GET", "/admin/mail/folder/1", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "admin-session"})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-token-123"})
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /admin/mail/folder/1 returned status %d", rr.Code)
	}

	// 3. GET /admin/mail/message/1/101
	req = httptest.NewRequest("GET", "/admin/mail/message/1/101", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "admin-session"})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-token-123"})
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /admin/mail/message/1/101 returned status %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Hello world") {
		t.Errorf("expected message content in response")
	}

	// 4. GET /admin/mail/compose
	req = httptest.NewRequest("GET", "/admin/mail/compose", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "admin-session"})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-token-123"})
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /admin/mail/compose returned status %d", rr.Code)
	}

	// 5. POST /admin/mail/send with multipart attachment
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("to", "recipient@example.com")
	_ = mw.WriteField("subject", "Test with Attachment")
	_ = mw.WriteField("body", "Please see attached file.")
	_ = mw.WriteField("folder_id", "1")
	_ = mw.WriteField("csrf", "csrf-token-123")
	fw, _ := mw.CreateFormFile("attachments", "test.txt")
	_, _ = fw.Write([]byte("attachment content"))
	_ = mw.Close()

	req = httptest.NewRequest("POST", "/admin/mail/send", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CSRF-Token", "csrf-token-123")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "admin-session"})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-token-123"})
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /admin/mail/send returned status %d, body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Message sent") {
		t.Errorf("expected 'Message sent' in response, got: %s", rr.Body.String())
	}

	// 6. POST /admin/mail/action (mark_read)
	form := url.Values{}
	form.Set("action", "mark_read")
	form.Set("message_ids", "101")
	form.Set("folder_id", "1")
	form.Set("csrf", "csrf-token-123")
	req = httptest.NewRequest("POST", "/admin/mail/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", "csrf-token-123")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "admin-session"})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-token-123"})
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /admin/mail/action returned status %d", rr.Code)
	}

	// 7. GET /admin/mail/search?q=Test
	req = httptest.NewRequest("GET", "/admin/mail/search?q=Test", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "admin-session"})
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /admin/mail/search returned status %d", rr.Code)
	}
}
