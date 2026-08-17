package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ashu-choudhury/portfolio/store"
)

// ---------------------------------------------------------------------------
// Mock OpenAI-compatible server

// mockAIServer is an in-process OpenAI-compatible chat completions endpoint
// that records every request body and returns canned responses. When
// sequential is set, each call consumes the next response (useful for the
// two-session pipeline: plan first, blog second).
type mockAIServer struct {
	mu         sync.Mutex
	bodies     []string
	response   string   // full chat response JSON body to return
	sequential []string // when non-empty, consume one response per call
	status     int      // HTTP status to return (default 200)
	calls      int
	// failCalls optionally makes the first n calls fail with status 500.
	failCalls int
	srv       *httptest.Server
}

// newMockAIServer returns a mock that always answers with responseJSON.
// newSequentialMockAIServer answers call 1 with the strategist's plan and
// call 2 with the writer's blog post (the full two-session pipeline).
func newMockAIServer(t *testing.T, responseJSON string) *mockAIServer {
	return newMockAIServerSequential(t, responseJSON)
}

func newSequentialMockAIServer(t *testing.T) *mockAIServer {
	return newMockAIServerSequential(t, chatResponse(planJSON), chatResponse(blogJSON))
}

func newMockAIServerSequential(t *testing.T, responses ...string) *mockAIServer {
	m := &mockAIServer{status: http.StatusOK, sequential: responses}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.calls++
		call := m.calls
		body := readAllBody(r)
		m.bodies = append(m.bodies, body)
		fail := m.failCalls >= call
		status := m.status
		var response string
		if !fail && len(m.sequential) > 0 {
			response = m.sequential[0]
			m.sequential = m.sequential[1:]
		}
		m.mu.Unlock()

		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Errorf("expected bearer token header, got %s", r.Header.Get("Authorization"))
		}
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(m.srv.Close)
	return m
}

// requestBodies returns the raw request bodies, in order.
func (m *mockAIServer) requestBodies() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.bodies))
	copy(out, m.bodies)
	return out
}

func (m *mockAIServer) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// chatResponse builds a chat-completions response with the given content.
func chatResponse(content string) string {
	payload, _ := json.Marshal(openAIChatResponse{
		Choices: []struct {
			Message openAIMessage `json:"message"`
		}{{Message: openAIMessage{Role: "assistant", Content: content}}},
	})
	return string(payload)
}

const planJSON = `{"topic":"Docker for Android developers","title":"Running Docker on Android: A Practical Guide","angle":"A hands-on guide with examples, adb setup and gotchas","tags":["docker","android","devops"]}`

const blogJSON = `{"title":"Running Docker on Android: A Practical Guide","summary":"Learn how to run Docker workloads on Android devices with Termux.","tags":["docker","android","devops"],"body":"# Running Docker on Android\n\nThis is the full article body."}`

func readAllBody(r *http.Request) string {
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Client unit tests

func TestAIClientChatSuccess(t *testing.T) {
	mock := newMockAIServer(t, chatResponse(`{"title":"Hello","summary":"s","tags":["go"],"body":"# Hello"}`))
	client := NewAIClient(AIConfig{BaseURL: mock.srv.URL, APIKey: "test-api-key", Model: "gpt-4o-mini"})

	content, err := client.chat(context.Background(), []openAIMessage{
		{Role: "user", Content: "hi"},
	})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	if !strings.Contains(content, "Hello") {
		t.Errorf("unexpected content: %s", content)
	}

	body := mock.requestBodies()[0]
	var req openAIChatRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if req.Model != "gpt-4o-mini" {
		t.Errorf("model = %q", req.Model)
	}
	if len(req.Messages) != 1 || req.Messages[0].Content != "hi" {
		t.Errorf("unexpected messages: %+v", req.Messages)
	}
}

func TestAIClientRetriesOnTransientFailure(t *testing.T) {
	mock := newMockAIServer(t, chatResponse(`ok`))
	mock.failCalls = 2 // fail twice, succeed on the third attempt
	client := NewAIClient(AIConfig{BaseURL: mock.srv.URL, APIKey: "test-api-key", Model: "m"})

	if _, err := client.chat(context.Background(), []openAIMessage{{Role: "user", Content: "x"}}); err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if got := mock.callCount(); got != 3 {
		t.Errorf("expected 3 calls (2 failures + 1 success), got %d", got)
	}
}

func TestAIClientDoesNotRetryClientErrors(t *testing.T) {
	mock := newMockAIServer(t, chatResponse(`ok`))
	mock.status = http.StatusBadRequest
	client := NewAIClient(AIConfig{BaseURL: mock.srv.URL, APIKey: "test-api-key", Model: "m"})

	_, err := client.chat(context.Background(), []openAIMessage{{Role: "user", Content: "x"}})
	if err == nil {
		t.Fatal("expected error for 400")
	}
	if got := mock.callCount(); got != 1 {
		t.Errorf("expected a single call for a 4xx, got %d", got)
	}
}

func TestAIClientEmptyResponseFails(t *testing.T) {
	mock := newMockAIServer(t, chatResponse(`   `))
	client := NewAIClient(AIConfig{BaseURL: mock.srv.URL, APIKey: "test-api-key", Model: "m"})

	_, err := client.chat(context.Background(), []openAIMessage{{Role: "user", Content: "x"}})
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestAIClientMissingAPIKey(t *testing.T) {
	client := NewAIClient(AIConfig{BaseURL: "http://unused", APIKey: "", Model: "m"})
	_, err := client.chat(context.Background(), []openAIMessage{{Role: "user", Content: "x"}})
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("expected missing-key error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Two-session isolation: the writer must never see the strategist's history
// or conversation — only the chosen topic.

func TestTwoSessionIsolation(t *testing.T) {
	// A stateful mock: call 1 (strategist) returns the plan, call 2 (writer)
	// returns the blog — while recording every request body in order.
	var mu sync.Mutex
	idx := 0
	var bodies []string
	steps := []string{chatResponse(planJSON), chatResponse(blogJSON)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		cur := idx
		idx++
		b := readAllBody(r)
		bodies = append(bodies, b)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(steps[cur]))
	}))
	defer srv.Close()

	client := NewAIClient(AIConfig{BaseURL: srv.URL, APIKey: "test-api-key", Model: "m"})

	history := []store.Post{
		{Title: "Welcome to my blog", Summary: "A personal space.", Tags: []string{"thoughts"}},
		{Title: "Building Go Services with HTMX", Summary: "Fast web apps.", Tags: []string{"go", "htmx"}},
	}

	plan, err := client.SuggestNextTopic(context.Background(), history, "")
	if err != nil {
		t.Fatalf("SuggestNextTopic: %v", err)
	}
	if plan.Topic != "Docker for Android developers" {
		t.Errorf("unexpected plan topic: %q", plan.Topic)
	}

	_, err = client.WriteBlogPost(context.Background(), *plan)
	if err != nil {
		t.Fatalf("WriteBlogPost: %v", err)
	}

	if len(bodies) != 2 {
		t.Fatalf("expected exactly 2 LLM calls, got %d", len(bodies))
	}

	// Session 1 must see the full blog history.
	call1 := bodies[0]
	for _, s := range []string{"Welcome to my blog", "Building Go Services with HTMX"} {
		if !strings.Contains(call1, s) {
			t.Errorf("session 1 missing history %q:\n%s", s, call1)
		}
	}
	if !strings.Contains(call1, "strategist") && !strings.Contains(call1, "history") {
		t.Errorf("session 1 does not look like the strategist prompt:\n%s", call1)
	}

	// Session 2 must be a fresh chat: no history, no strategist output.
	call2 := bodies[1]
	for _, forbidden := range []string{"Welcome to my blog", "Building Go Services with HTMX", "A personal space", "strategist"} {
		if strings.Contains(call2, forbidden) {
			t.Errorf("session 2 leaked content from session 1 (%q):\n%s", forbidden, call2)
		}
	}
	if !strings.Contains(call2, "Docker for Android developers") {
		t.Errorf("session 2 missing the chosen topic:\n%s", call2)
	}

	// Each session is a plain 2-message chat (system + user).
	for i, b := range bodies {
		var req openAIChatRequest
		if err := json.Unmarshal([]byte(b), &req); err != nil {
			t.Fatalf("session %d: bad request: %v", i+1, err)
		}
		if len(req.Messages) != 2 {
			t.Errorf("session %d: expected 2 messages, got %d", i+1, len(req.Messages))
		}
	}
}

// ---------------------------------------------------------------------------
// Parse fallbacks

func TestSuggestNextTopicFallbackRawText(t *testing.T) {
	mock := newMockAIServer(t, chatResponse("I think the best topic is Kubernetes networking."))
	client := NewAIClient(AIConfig{BaseURL: mock.srv.URL, APIKey: "test-api-key", Model: "m"})

	plan, err := client.SuggestNextTopic(context.Background(), nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(plan.Topic, "Kubernetes networking") {
		t.Errorf("expected fallback topic from raw reply, got %q", plan.Topic)
	}
}

func TestWriteBlogPostFallbackRawMarkdown(t *testing.T) {
	md := "# My Post\n\nBody text here."
	mock := newMockAIServer(t, chatResponse(md))
	client := NewAIClient(AIConfig{BaseURL: mock.srv.URL, APIKey: "test-api-key", Model: "m"})

	res, err := client.WriteBlogPost(context.Background(), AIBlogPlan{Topic: "My Post", Title: "My Post"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Body != md {
		t.Errorf("expected raw markdown as body, got %q", res.Body)
	}
	if res.Slug != "my-post" {
		t.Errorf("slug = %q, want my-post", res.Slug)
	}
}

func TestWriteBlogPostJSONWithFences(t *testing.T) {
	fenced := "```json\n" + blogJSON + "\n```"
	mock := newMockAIServer(t, chatResponse(fenced))
	client := NewAIClient(AIConfig{BaseURL: mock.srv.URL, APIKey: "test-api-key", Model: "m"})

	res, err := client.WriteBlogPost(context.Background(), AIBlogPlan{Topic: "Docker", Title: "Docker"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Title != "Running Docker on Android: A Practical Guide" {
		t.Errorf("title = %q", res.Title)
	}
	if res.Slug != "running-docker-on-android-a-practical-guide" {
		t.Errorf("slug = %q", res.Slug)
	}
}

func TestCleanCodeFences(t *testing.T) {
	input := "```json\n{\"title\": \"Hello\"}\n```"
	expected := "{\"title\": \"Hello\"}"
	if got := cleanCodeFences(input); got != expected {
		t.Errorf("cleanCodeFences failed: got %q, expected %q", got, expected)
	}
}

// ---------------------------------------------------------------------------
// End-to-end: the ping endpoint runs the two sessions and auto-publishes.

func newTestServer(t *testing.T, mockURL string, seedPost bool) (*Server, *store.Memory) {
	t.Helper()
	ms := store.NewMemory()
	ctx := context.Background()
	_ = ms.SetSetting(ctx, "ai_base_url", mockURL)
	_ = ms.SetSetting(ctx, "ai_api_key", "test-api-key")
	_ = ms.SetSetting(ctx, "ai_default_model", "gpt-4o-mini")
	_ = ms.SetSetting(ctx, "ai_generate_token", "test-ping-token")

	if seedPost {
		_, _ = ms.CreatePost(ctx, store.Post{
			Slug:        "welcome",
			Title:       "Welcome to my blog",
			Summary:     "A personal space.",
			Tags:        []string{"thoughts"},
			Body:        "# Welcome",
			Published:   true,
			PublishedAt: time.Now().UTC(),
		})
	}
	srv := New(ms, nil)
	return srv, ms
}

func decodeAIJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode JSON: %v; body=%s", err, rec.Body.String())
	}
	return m
}

func TestAIGeneratePingSynchronousAutoPublish(t *testing.T) {
	mock := newSequentialMockAIServer(t)
	srv, ms := newTestServer(t, mock.srv.URL, true)
	handler := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/ai/generate?token=test-ping-token&wait=true", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeAIJSON(t, rec)
	if resp["status"] != "published" {
		t.Fatalf("expected status published, got %v: %s", resp["status"], rec.Body.String())
	}
	post, ok := resp["post"].(map[string]any)
	if !ok {
		t.Fatalf("missing post in response: %s", rec.Body.String())
	}
	if post["slug"] != "running-docker-on-android-a-practical-guide" {
		t.Errorf("slug = %v", post["slug"])
	}

	// The post must be in the store and published.
	got, err := ms.GetPost(context.Background(), "running-docker-on-android-a-practical-guide")
	if err != nil {
		t.Fatalf("published post not found: %v", err)
	}
	if !got.Published {
		t.Error("post must be published automatically")
	}
	if got.Title != "Running Docker on Android: A Practical Guide" {
		t.Errorf("title = %q", got.Title)
	}
	posts, _ := ms.ListPosts(context.Background(), false)
	if len(posts) != 2 { // seed welcome + generated
		t.Errorf("expected 2 published posts, got %d", len(posts))
	}
}

func TestAIGeneratePingAsyncFlow(t *testing.T) {
	mock := newSequentialMockAIServer(t)
	srv, _ := newTestServer(t, mock.srv.URL, true)
	handler := srv.Handler()

	// Fire the ping (no wait) — must answer immediately with a job id.
	req := httptest.NewRequest(http.MethodPost, "/api/ai/generate?token=test-ping-token", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	created := decodeAIJSON(t, rec)
	jobID, _ := created["job_id"].(string)
	if jobID == "" {
		t.Fatal("missing job_id in 202 response")
	}

	// Poll the status endpoint until the job publishes.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req := httptest.NewRequest(http.MethodGet, "/api/ai/generate/status?token=test-ping-token&job_id="+jobID, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status returned %d: %s", rec.Code, rec.Body.String())
		}
		st := decodeAIJSON(t, rec)
		if st["status"] == "published" {
			if _, ok := st["post"].(map[string]any); !ok {
				t.Fatal("published status must include the post")
			}
			return
		}
		if st["status"] == "failed" {
			t.Fatalf("job failed: %v", st["error"])
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("job did not publish within 10s")
}

func TestAIGeneratePingRequiresToken(t *testing.T) {
	mock := newSequentialMockAIServer(t)
	srv, _ := newTestServer(t, mock.srv.URL, true)
	handler := srv.Handler()

	// No token at all.
	req := httptest.NewRequest(http.MethodGet, "/api/ai/generate?wait=true", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", rec.Code)
	}

	// Wrong token.
	req = httptest.NewRequest(http.MethodGet, "/api/ai/generate?token=wrong&wait=true", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong token, got %d", rec.Code)
	}

	// Bearer header.
	req = httptest.NewRequest(http.MethodGet, "/api/ai/generate?wait=true", nil)
	req.Header.Set("Authorization", "Bearer test-ping-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with bearer token, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAIGeneratePingDisabledWithoutConfiguredToken(t *testing.T) {
	mock := newMockAIServer(t, chatResponse(planJSON))
	ms := store.NewMemory()
	ctx := context.Background()
	_ = ms.SetSetting(ctx, "ai_base_url", mock.srv.URL)
	_ = ms.SetSetting(ctx, "ai_api_key", "test-api-key")
	_, _ = ms.CreatePost(ctx, store.Post{
		Slug: "welcome", Title: "Welcome", Published: true, PublishedAt: time.Now().UTC(),
	})
	srv := New(ms, nil)
	handler := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/ai/generate?wait=true", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when no token configured, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAIGeneratePingNoHistoryAndNoTopic(t *testing.T) {
	mock := newMockAIServer(t, chatResponse(planJSON))
	srv, _ := newTestServer(t, mock.srv.URL, false) // empty blog
	handler := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/ai/generate?token=test-ping-token&wait=true", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for empty history + no topic, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAIGeneratePingWithExplicitTopic(t *testing.T) {
	// When the caller supplies a topic, only ONE LLM session runs (the
	// writer) — no strategist call.
	mock := newMockAIServer(t, chatResponse(blogJSON))
	srv, ms := newTestServer(t, mock.srv.URL, true)
	handler := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/ai/generate?token=test-ping-token&wait=true&topic=Docker", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := mock.callCount(); got != 1 {
		t.Errorf("expected a single LLM call with explicit topic, got %d", got)
	}
	if _, err := ms.GetPost(context.Background(), "running-docker-on-android-a-practical-guide"); err != nil {
		t.Errorf("post not published: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Slug deduplication

func TestUniquePostSlug(t *testing.T) {
	ms := store.NewMemory()
	ctx := context.Background()
	_, _ = ms.CreatePost(ctx, store.Post{Slug: "docker", Title: "Docker", Published: true})
	srv := New(ms, nil)

	if got := srv.uniquePostSlug(ctx, "docker"); got != "docker-2" {
		t.Errorf("uniquePostSlug(docker) = %q, want docker-2", got)
	}
	if got := srv.uniquePostSlug(ctx, "fresh"); got != "fresh" {
		t.Errorf("uniquePostSlug(fresh) = %q, want fresh", got)
	}
}

// ---------------------------------------------------------------------------
// Admin dashboard flow (draft mode — never auto-publishes)

func TestAdminAIGenerateFlow(t *testing.T) {
	mock := newSequentialMockAIServer(t)
	srv, ms := newTestServer(t, mock.srv.URL, true)
	handler := srv.Handler()

	// Create an admin session.
	ctx := context.Background()
	_ = ms.CreateSession(ctx, store.Session{
		Token: "admin-session", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})

	do := func(req *http.Request) *httptest.ResponseRecorder {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "admin-session"})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// Without a session, the admin route rejects the POST (CSRF guard fires
	// first) and redirects when a valid csrf is supplied.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/ai/generate", nil))
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusSeeOther {
		t.Errorf("expected 403/303 without session, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/ai/generate?csrf="+csrfTokenGlobal, nil))
	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected redirect to login without session (csrf ok), got %d", rec.Code)
	}

	// Start generation WITHOUT a topic so the strategist session runs first
	// (full two-session pipeline). csrf uses the legacy token.
	form := "model=gpt-4o-mini&csrf=" + csrfTokenGlobal
	req := httptest.NewRequest(http.MethodPost, "/admin/ai/generate", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	html := rec.Body.String()
	if !strings.Contains(html, "ai-job-card") {
		t.Fatalf("expected the running job card in the response")
	}
	if strings.Contains(html, "ai-spinner") == false {
		t.Errorf("expected the spinner in the job card")
	}

	// Extract the job id from the polling URL.
	re := regexp.MustCompile(`/admin/ai/generate/status\?job_id=([0-9a-f]+)`)
	m := re.FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("could not find job id in response:\n%s", html)
	}
	jobID := m[1]

	// Poll until the job finishes.
	deadline := time.Now().Add(10 * time.Second)
	doneHTML := ""
	for time.Now().Before(deadline) {
		rec = do(httptest.NewRequest(http.MethodGet, "/admin/ai/generate/status?job_id="+jobID, nil))
		doneHTML = rec.Body.String()
		if strings.Contains(doneHTML, "Generation complete") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(doneHTML, "Generation complete") {
		t.Fatalf("job did not complete in time: %s", doneHTML)
	}
	if strings.Contains(doneHTML, "hx-trigger=\"every 2s\"") {
		t.Error("completed card must stop polling")
	}

	// Load the draft into the editor.
	rec = do(httptest.NewRequest(http.MethodGet, "/admin/ai/generate/load?job_id="+jobID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("load returned %d: %s", rec.Code, rec.Body.String())
	}
	loadHTML := rec.Body.String()
	if !strings.Contains(loadHTML, "Running Docker on Android: A Practical Guide") {
		t.Errorf("loaded form missing the generated title")
	}
	if !strings.Contains(loadHTML, "running-docker-on-android-a-practical-guide") {
		t.Errorf("loaded form missing the generated slug")
	}
	if !strings.Contains(loadHTML, "name=\"body\"") {
		t.Errorf("loaded form missing the body editor")
	}
	if !strings.Contains(loadHTML, "name=\"published\" checked=\"true\"") {
		t.Errorf("loaded draft should be pre-checked as published-ready, got: %s", loadHTML)
	}

	// Draft mode: nothing was published to the store yet.
	if _, err := ms.GetPost(ctx, "running-docker-on-android-a-practical-guide"); err == nil {
		t.Error("admin generation must NOT publish automatically (draft mode)")
	}

	// The strategist session must have seen the blog history.
	bodies := mock.requestBodies()
	if len(bodies) != 2 {
		t.Fatalf("expected 2 LLM sessions, got %d", len(bodies))
	}
	if !strings.Contains(bodies[0], "Welcome to my blog") {
		t.Error("strategist session did not receive the blog history")
	}
	if strings.Contains(bodies[1], "Welcome to my blog") {
		t.Error("writer session leaked the blog history")
	}
}

func TestAdminAIGenerateReportsMissingAPIKey(t *testing.T) {
	// Force-empty the API key even if the shell has one set.
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("AI_API_KEY", "")

	ms := store.NewMemory()
	ctx := context.Background()
	_ = ms.SetSetting(ctx, "ai_generate_token", "t")
	_ = ms.CreateSession(ctx, store.Session{
		Token: "admin-session", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	srv := New(ms, nil)
	handler := srv.Handler()

	form := "topic=Docker&csrf=" + csrfTokenGlobal
	req := httptest.NewRequest(http.MethodPost, "/admin/ai/generate", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "admin-session"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "AI Generation failed") {
		t.Errorf("expected a clear error in the form, got: %s", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Token check unit tests

func TestCheckAIGenerateToken(t *testing.T) {
	ms := store.NewMemory()
	_ = ms.SetSetting(context.Background(), "ai_generate_token", "secret-token")
	srv := New(ms, nil)

	cases := []struct {
		name   string
		req    *http.Request
		status int
	}{
		{"bearer header", withHeaders(httptest.NewRequest(http.MethodGet, "/", nil), "Authorization", "Bearer secret-token"), 0},
		{"x-ai-token header", withHeaders(httptest.NewRequest(http.MethodGet, "/", nil), "X-AI-Token", "secret-token"), 0},
		{"query token", httptest.NewRequest(http.MethodGet, "/?token=secret-token", nil), 0},
		{"missing", httptest.NewRequest(http.MethodGet, "/", nil), http.StatusUnauthorized},
		{"wrong", httptest.NewRequest(http.MethodGet, "/?token=nope", nil), http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, err := srv.checkAIGenerateToken(tc.req)
			if tc.status == 0 {
				if err != nil {
					t.Fatalf("expected authorized, got error: %v", err)
				}
			} else if status != tc.status {
				t.Fatalf("expected status %d, got %d (%v)", tc.status, status, err)
			}
		})
	}
}

func withHeaders(req *http.Request, k, v string) *http.Request {
	req.Header.Set(k, v)
	return req
}

// ---------------------------------------------------------------------------
// Persisted run history

func TestAIGenJobPersistedOnPublish(t *testing.T) {
	mock := newSequentialMockAIServer(t)
	srv, ms := newTestServer(t, mock.srv.URL, true)
	handler := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/ai/generate?token=test-ping-token&wait=true", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	jobs, err := ms.ListAIGenJobs(context.Background(), 0)
	if err != nil {
		t.Fatalf("list ai jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 persisted run, got %d", len(jobs))
	}
	j := jobs[0]
	if j.Status != "published" {
		t.Errorf("status = %q, want published", j.Status)
	}
	if j.Title != "Running Docker on Android: A Practical Guide" {
		t.Errorf("title = %q", j.Title)
	}
	if j.Slug != "running-docker-on-android-a-practical-guide" {
		t.Errorf("slug = %q", j.Slug)
	}
	if j.Model != "gpt-4o-mini" {
		t.Errorf("model = %q", j.Model)
	}
	if !j.Publish {
		t.Error("ping path must set publish=true on the record")
	}
	if j.CreatedAt.IsZero() || j.FinishedAt.IsZero() {
		t.Errorf("created/finished timestamps missing: %+v", j)
	}
	if j.PostID == 0 {
		t.Errorf("published run should reference the created post id")
	}
}

func TestAIGenJobPersistedOnFailure(t *testing.T) {
	// The writer always fails (empty response), so the run must end as failed.
	mock := newMockAIServer(t, chatResponse(`   `))
	srv, ms := newTestServer(t, mock.srv.URL, true)
	handler := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/ai/generate?token=test-ping-token&wait=true", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for failed run, got %d: %s", rec.Code, rec.Body.String())
	}

	jobs, err := ms.ListAIGenJobs(context.Background(), 0)
	if err != nil {
		t.Fatalf("list ai jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 persisted run, got %d", len(jobs))
	}
	j := jobs[0]
	if j.Status != "failed" {
		t.Errorf("status = %q, want failed", j.Status)
	}
	if j.Error == "" {
		t.Error("failed run must persist the error message")
	}
	if j.FinishedAt.IsZero() {
		t.Error("failed run must record when it finished")
	}
}

func TestAdminDashboardShowsAIGenRuns(t *testing.T) {
	ms := store.NewMemory()
	ctx := context.Background()
	now := time.Now().UTC()
	_ = ms.UpsertAIGenJob(ctx, store.AIGenJob{
		ID: "run-published", Status: "published", Stage: "Published", Model: "gpt-4o-mini",
		Title: "Running Docker on Android", Slug: "running-docker-on-android",
		CreatedAt: now.Add(-2 * time.Hour), FinishedAt: now.Add(-2 * time.Hour),
	})
	_ = ms.UpsertAIGenJob(ctx, store.AIGenJob{
		ID: "run-failed", Status: "failed", Stage: "Failed", Model: "deepseek-chat",
		TopicHint: "Kubernetes", Error: "ai provider returned an empty response",
		CreatedAt: now.Add(-3 * time.Hour), FinishedAt: now.Add(-3 * time.Hour),
	})
	_ = ms.UpsertAIGenJob(ctx, store.AIGenJob{
		ID: "run-running", Status: "writing", Stage: "Agent 2 — writing…", Model: "gpt-4o-mini",
		TopicHint: "Rust", CreatedAt: now,
	})
	_ = ms.CreateSession(ctx, store.Session{
		Token: "admin-session", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	})

	srv := New(ms, nil)
	handler := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "admin-session"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard returned %d: %s", rec.Code, rec.Body.String())
	}

	html := rec.Body.String()
	for _, want := range []string{
		"Recent AI generation",
		"Running Docker on Android",
		"Kubernetes",
		"Rust",
		"published",
		"failed",
		"writing",
		"ai provider returned an empty response",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	if strings.Contains(html, "No AI runs yet") {
		t.Error("dashboard shows the empty state despite persisted runs")
	}
}

// ---------------------------------------------------------------------------
// Job registry

func TestAIJobRegistryPrunesOldJobs(t *testing.T) {
	reg := newAIJobRegistry()
	reg.register(&AIGenJob{ID: "old", CreatedAt: time.Now().Add(-2 * time.Hour)})
	// Pruning happens on the next register — old must be gone afterwards.
	reg.register(&AIGenJob{ID: "fresh", CreatedAt: time.Now()})
	if reg.get("old") != nil {
		t.Fatal("expected old job to be pruned on next register")
	}
	if reg.get("fresh") == nil {
		t.Fatal("expected fresh job to exist")
	}
}

func TestBuildHistoryList(t *testing.T) {
	list := buildHistoryList([]store.Post{
		{Title: "Post A", Tags: []string{"go"}, Summary: "About Go."},
		{Title: "Post B", Tags: nil, Summary: ""},
	})
	if !strings.Contains(list, "1. Post A — tags: go — About Go.") {
		t.Errorf("unexpected history list: %q", list)
	}
	if !strings.Contains(list, "2. Post B") {
		t.Errorf("unexpected history list: %q", list)
	}
}
