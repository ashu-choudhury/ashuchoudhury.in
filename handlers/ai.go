package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// AIConfig holds connection and model settings for OpenAI-compatible APIs.
type AIConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

// AIBlogResult represents the structured response returned by the AI.
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

const systemPrompt = `You are an advanced AI content generator specialized in writing high-quality blog articles in professional Markdown format. Given a topic, you must generate a fully structured blog post with appropriate Markdown elements, including headings (#, ##, ###), paragraphs, lists (bulleted and numbered), blockquotes, code blocks with language identifiers, and bold/italic emphasis. Avoid raw HTML tags or conversational explanations—output only a valid JSON object. Ensure content is engaging, deeply informative, accurate, and well-organized for an online developer audience.`

// DefaultAIConfig returns settings resolved from database settings or environment.
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

// GenerateAIBlogPost calls an OpenAI-compatible API to generate a blog post.
func GenerateAIBlogPost(ctx context.Context, config AIConfig, topic string) (*AIBlogResult, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return nil, errors.New("please provide a topic or prompt for AI generation")
	}

	if config.APIKey == "" {
		return nil, errors.New("AI API key is missing — set OPENAI_API_KEY in environment or configure it in Admin Settings")
	}

	endpoint := config.BaseURL
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint = endpoint + "/chat/completions"
	}

	userContent := fmt.Sprintf(`Write a detailed, professional blog post in Markdown about: %s.

Return ONLY a valid JSON object with the following exact keys:
"title": "A catchy, clear title for the post",
"summary": "A concise 1-2 sentence summary of the article",
"tags": ["tag1", "tag2", "tag3"],
"body": "The complete blog post written in rich, professional Markdown format..."`, topic)

	reqPayload := openAIChatRequest{
		Model: config.Model,
		Messages: []openAIMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userContent},
		},
		Temperature: 0.7,
	}

	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal ai payload: %w", err)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create ai request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+config.APIKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ai request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read ai response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ai endpoint returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp openAIChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal ai chat response: %w", err)
	}

	if chatResp.Error != nil && chatResp.Error.Message != "" {
		return nil, fmt.Errorf("ai provider error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 || chatResp.Choices[0].Message.Content == "" {
		return nil, errors.New("ai provider returned empty response content")
	}

	rawContent := chatResp.Choices[0].Message.Content
	cleanJSON := cleanCodeFences(rawContent)

	var result AIBlogResult
	if err := json.Unmarshal([]byte(cleanJSON), &result); err != nil {
		// Fallback: if JSON parsing fails, put raw text as body
		result = AIBlogResult{
			Title:   prettyTopicTitle(topic),
			Summary: firstSentence(rawContent),
			Tags:    []string{"tech", "article"},
			Body:    rawContent,
		}
	}

	if result.Title == "" {
		result.Title = prettyTopicTitle(topic)
	}
	if result.Slug == "" {
		result.Slug = slugify(result.Title)
	}

	return &result, nil
}

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
	if len(t) == 0 {
		return "Untitled Article"
	}
	if len(t) > 80 {
		return t[:80] + "…"
	}
	return strings.Title(t)
}

func firstSentence(s string) string {
	if i := strings.IndexAny(s, ".!?\n"); i >= 0 {
		return strings.TrimSpace(s[:i+1])
	}
	if len(s) > 150 {
		return s[:150] + "…"
	}
	return s
}
