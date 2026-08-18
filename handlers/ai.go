package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ashu-choudhury/portfolio/store"
)

// AIConfig holds connection and model settings for OpenAI-compatible APIs.
type AIConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

// AIBlogPlan is the output of session 1 (the topic strategist): a single
// decision about what the next blog post should be.
type AIBlogPlan struct {
	Topic string   `json:"topic"`
	Title string   `json:"title"`
	Angle string   `json:"angle"`
	Tags  []string `json:"tags"`
}

// AIBlogResult is the output of session 2 (the writer): a complete blog post.
type AIBlogResult struct {
	Title   string   `json:"title"`
	Slug    string   `json:"slug"`
	Summary string   `json:"summary"`
	Tags    []string `json:"tags"`
	Body    string   `json:"body"`
}

type openAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Strategist prompt — session 1. Sees the full blog history and returns a
// single topic decision. This conversation is ended before session 2 starts.
const strategistSystemPrompt = `You are the content strategist for a personal developer blog. You are given the list of blog posts already published on this site. Your job is to choose the single best topic for the NEXT blog post — one that fits the site's voice, fills a gap in what has already been written, and would genuinely interest the author's audience.

Rules:
- Never repeat or closely duplicate a topic that already exists in the history.
- Choose exactly one topic. No alternatives, no ranges, no discussion.
- Pick a topic the author can write about authoritatively (software development, accessibility, tools, projects, engineering lessons).
- Respond with ONLY a single valid JSON object, nothing else.`

const strategistUserTemplate = `Here is every post already published on this blog (title, tags, summary):

%s

Think carefully about what is missing and what would be the strongest next post. Then give your single answer as valid JSON:

{"topic": "one-sentence subject of the post", "title": "a catchy, concrete final title", "angle": "what the post should look like: structure, focus, examples, tone", "tags": ["3-5 relevant tags"]}

Return ONLY the JSON object.`

// Writer prompt — session 2. A brand-new chat that never sees session 1's
// conversation. It receives only the chosen topic/title/angle/tags and must
// produce the complete post.
const writerSystemPrompt = `You are an expert technical blog writer. You write complete, detailed, well-structured blog posts in professional Markdown. Given a topic, title and angle, you produce the entire article: engaging introduction, clear sections with headings (##, ###), paragraphs, lists, blockquotes, code blocks with language identifiers, and a conclusion. Never write raw HTML or conversational filler — only the article itself.`

const writerUserTemplate = `Topic: %s
Title: %s
Angle / what the post should look like: %s
Suggested tags: %s

Write the complete blog post now, as detailed and useful as possible.

Return ONLY a single valid JSON object with these exact keys:
{"title": "the final title", "summary": "one concise 1-2 sentence summary for the blog index", "tags": ["3-5 tags"], "body": "the entire article in Markdown"}

Do not wrap the JSON in prose or code fences.`

// AIClient talks to any OpenAI-compatible chat completions endpoint.
type AIClient struct {
	cfg    AIConfig
	client *http.Client
}

// NewAIClient builds a client for the given configuration.
func NewAIClient(cfg AIConfig) *AIClient {
	return &AIClient{
		cfg:    cfg,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

// maxAIRetries is how many times a single chat call is retried after a
// transient failure (network error, 5xx, empty response).
const maxAIRetries = 2

// chat runs one chat-completion request. Each call is stateless — callers
// build the full message list themselves, which is what keeps session 2
// completely isolated from session 1.
func (c *AIClient) chat(ctx context.Context, messages []openAIMessage) (string, error) {
	if c.cfg.APIKey == "" {
		return "", errors.New("AI API key is missing — set OPENAI_API_KEY/AI_API_KEY in the environment or configure it in Admin Settings")
	}

	endpoint := strings.TrimSuffix(c.cfg.BaseURL, "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}

	payload := openAIChatRequest{
		Model:       c.cfg.Model,
		Messages:    messages,
		Temperature: 0.7,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal ai payload: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= maxAIRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", fmt.Errorf("create ai request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("ai request failed: %w", err)
			log.Printf("ai chat attempt %d: %v", attempt+1, lastErr)
			continue
		}

		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("read ai response: %w", readErr)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("ai endpoint returned status %d: %s", resp.StatusCode, truncate(string(respBody), 300))
			// Retry server-side failures (5xx), not client errors (4xx).
			if resp.StatusCode < 500 {
				return "", lastErr
			}
			log.Printf("ai chat attempt %d: %v", attempt+1, lastErr)
			continue
		}

		var chatResp openAIChatResponse
		if err := json.Unmarshal(respBody, &chatResp); err != nil {
			lastErr = fmt.Errorf("unmarshal ai chat response: %w", err)
			continue
		}
		if chatResp.Error != nil && chatResp.Error.Message != "" {
			lastErr = fmt.Errorf("ai provider error: %s", chatResp.Error.Message)
			continue
		}
		if len(chatResp.Choices) == 0 || strings.TrimSpace(chatResp.Choices[0].Message.Content) == "" {
			lastErr = errors.New("ai provider returned an empty response")
			continue
		}
		return chatResp.Choices[0].Message.Content, nil
	}

	if lastErr == nil {
		lastErr = errors.New("ai request failed after retries")
	}
	return "", lastErr
}

// emailRefineSystemPrompt is the system prompt for the compose-pane
// "Refine with AI" button. It asks for a polished rewrite that keeps every
// concrete detail and returns plain, well-paragraphed text (which the mail
// client renders as formatted HTML on send) — never markdown symbols.
const emailRefineSystemPrompt = `You are an expert email writer. Given a draft email, rewrite it into a polished, professional message that preserves the writer's meaning, tone and every concrete detail (names, numbers, links, dates). Improve clarity, flow, grammar and formatting.

Formatting rules:
- Start with an appropriate greeting (e.g. "Hi [name]," or "Hello,") and end with a short sign-off ("Thanks," / "Best regards," and the writer's name if present).
- Keep paragraphs short, separated by a blank line. Never use markdown symbols (no **, *, #, or backticks).
- Use a bullet list only when the draft used one; otherwise use plain paragraphs.
- Never invent facts, names, URLs or details that are not in the draft.
- Reply with ONLY the rewritten email text — no preamble, no quotes, no explanation.`

// RefineEmail rewrites a draft email into a polished, well-formatted
// version using the configured model. subject (optional) gives context.
// Returns plain text ready to paste into the compose box.
func (c *AIClient) RefineEmail(ctx context.Context, subject, draft string) (string, error) {
	draft = strings.TrimSpace(draft)
	if draft == "" {
		return "", errors.New("nothing to refine — the message body is empty")
	}
	user := "Draft email body:\n\n" + draft
	if s := strings.TrimSpace(subject); s != "" {
		user = "Email subject: " + s + "\n\n" + user
	}
	content, err := c.chat(ctx, []openAIMessage{
		{Role: "system", Content: emailRefineSystemPrompt},
		{Role: "user", Content: user},
	})
	if err != nil {
		return "", err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", errors.New("AI returned an empty rewrite — try again")
	}
	return content, nil
}

// SuggestNextTopic runs session 1: given the site's blog history (and an
// optional hint), it returns one topic decision. The session ends here.
func (c *AIClient) SuggestNextTopic(ctx context.Context, history []store.Post, hint string) (*AIBlogPlan, error) {
	hist := buildHistoryList(history)
	if hist == "" {
		hist = "(no posts published yet — the blog is brand new)"
	}

	user := fmt.Sprintf(strategistUserTemplate, hist)
	if strings.TrimSpace(hint) != "" {
		user += "\n\nThe author would like the next post to be about: " + strings.TrimSpace(hint) +
			"\n\nIf the hint already names a concrete topic, return that topic with a strong title and angle. " +
			"Otherwise pick the best topic that matches the hint."
	}

	content, err := c.chat(ctx, []openAIMessage{
		{Role: "system", Content: strategistSystemPrompt},
		{Role: "user", Content: user},
	})
	if err != nil {
		return nil, err
	}

	var plan AIBlogPlan
	if err := json.Unmarshal([]byte(cleanCodeFences(content)), &plan); err != nil {
		// Fallback: treat the raw reply as the topic.
		plan = AIBlogPlan{Topic: strings.TrimSpace(content), Title: prettyTopicTitle(content)}
		log.Printf("ai strategist: could not parse JSON (%v) — using raw reply as topic", err)
	}

	plan.Topic = strings.TrimSpace(plan.Topic)
	plan.Title = strings.TrimSpace(plan.Title)
	if plan.Topic == "" && plan.Title != "" {
		plan.Topic = plan.Title
	}
	if plan.Title == "" {
		plan.Title = prettyTopicTitle(plan.Topic)
	}
	if plan.Topic == "" {
		return nil, errors.New("AI strategist did not return a topic")
	}
	if len(plan.Tags) == 0 {
		plan.Tags = []string{"tech", "article"}
	}
	return &plan, nil
}

// WriteBlogPost runs session 2: a brand-new chat that only knows the chosen
// plan (topic/title/angle/tags) — never the strategist's conversation or the
// blog history. It returns the complete blog post.
func (c *AIClient) WriteBlogPost(ctx context.Context, plan AIBlogPlan) (*AIBlogResult, error) {
	if strings.TrimSpace(plan.Topic) == "" && strings.TrimSpace(plan.Title) == "" {
		return nil, errors.New("no topic or title to write about")
	}

	user := fmt.Sprintf(writerUserTemplate,
		plan.Topic, plan.Title, plan.Angle, strings.Join(plan.Tags, ", "))

	content, err := c.chat(ctx, []openAIMessage{
		{Role: "system", Content: writerSystemPrompt},
		{Role: "user", Content: user},
	})
	if err != nil {
		return nil, err
	}

	var result AIBlogResult
	if err := json.Unmarshal([]byte(cleanCodeFences(content)), &result); err != nil {
		// Fallback: the model talked instead of returning JSON — keep the
		// raw content as the body so the run still succeeds.
		result = AIBlogResult{
			Title:   plan.Title,
			Summary: firstSentence(content),
			Tags:    plan.Tags,
			Body:    content,
		}
		log.Printf("ai writer: could not parse JSON (%v) — using raw reply as body", err)
	}

	result.Title = strings.TrimSpace(result.Title)
	if result.Title == "" {
		result.Title = plan.Title
	}
	if result.Slug == "" {
		result.Slug = slugify(result.Title)
	}
	if result.Slug == "" {
		result.Slug = slugify(plan.Topic)
	}
	if result.Slug == "" {
		result.Slug = "ai-generated-post"
	}
	if len(result.Tags) == 0 {
		result.Tags = plan.Tags
	}
	if len(result.Tags) == 0 {
		result.Tags = []string{"tech", "article"}
	}
	if strings.TrimSpace(result.Body) == "" {
		return nil, errors.New("AI writer returned an empty blog post")
	}
	return &result, nil
}

// buildHistoryList renders the blog history as a compact numbered list of
// titles, tags and summaries — the context given to the strategist.
func buildHistoryList(posts []store.Post) string {
	var b strings.Builder
	for i, p := range posts {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%d. %s", i+1, p.Title)
		if len(p.Tags) > 0 {
			fmt.Fprintf(&b, " — tags: %s", strings.Join(p.Tags, ", "))
		}
		if s := strings.TrimSpace(p.Summary); s != "" {
			b.WriteString(" — " + s)
		}
	}
	return b.String()
}

// DefaultAIConfig returns settings resolved from database settings or
// environment variables, falling back to OpenAI's defaults.
func (s *Server) DefaultAIConfig(ctx context.Context) AIConfig {
	baseURL, _ := s.Store.GetSetting(ctx, "ai_base_url")
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}
	if baseURL == "" {
		baseURL = os.Getenv("AI_BASE_URL")
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	apiKey, _ := s.Store.GetSetting(ctx, "ai_api_key")
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("AI_API_KEY")
	}

	model, _ := s.Store.GetSetting(ctx, "ai_default_model")
	if model == "" {
		model = os.Getenv("OPENAI_MODEL")
	}
	if model == "" {
		model = "gpt-4o-mini"
	}

	return AIConfig{
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
	}
}

// AIGenerateToken returns the shared secret required to ping the automatic
// AI blog generation endpoint, from settings or environment.
func (s *Server) AIGenerateToken(ctx context.Context) string {
	v, _ := s.Store.GetSetting(ctx, "ai_generate_token")
	if v == "" {
		v = os.Getenv("AI_GENERATE_TOKEN")
	}
	return strings.TrimSpace(v)
}

// ConfiguredAIModels returns the list of model choices for the admin dropdown.
func (s *Server) ConfiguredAIModels(ctx context.Context) []string {
	raw, _ := s.Store.GetSetting(ctx, "ai_models")
	if raw == "" {
		raw = os.Getenv("OPENAI_MODELS")
	}
	if raw == "" {
		return []string{"gpt-4o-mini", "gpt-4o", "deepseek-chat", "gemini-2.5-flash", "claude-3-5-sonnet"}
	}
	var list []string
	for _, m := range strings.Split(raw, ",") {
		m = strings.TrimSpace(m)
		if m != "" {
			list = append(list, m)
		}
	}
	if len(list) == 0 {
		return []string{"gpt-4o-mini", "gpt-4o", "deepseek-chat", "gemini-2.5-flash"}
	}
	return list
}

// ---------------------------------------------------------------------------
// Parsing helpers

// cleanCodeFences strips a ```json … ``` (or bare ``` … ```) wrapper that some
// models add around their JSON answer.
func cleanCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	}
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
	}
	return strings.TrimSpace(s)
}

func prettyTopicTitle(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return "Untitled Article"
	}
	if len(t) > 80 {
		return t[:80] + "…"
	}
	// Capitalize the first letter of each word, but keep acronyms/all-caps
	// words as-is.
	words := strings.Fields(t)
	for i, w := range words {
		if strings.ToUpper(w) == w && len(w) > 1 {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ".!?\n"); i >= 0 {
		return strings.TrimSpace(s[:i+1])
	}
	if len(s) > 150 {
		return s[:150] + "…"
	}
	return s
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
