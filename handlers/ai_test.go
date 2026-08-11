package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerateAIBlogPost(t *testing.T) {
	// Mock OpenAI-compatible server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Errorf("expected bearer token header, got %s", r.Header.Get("Authorization"))
		}

		jsonPayload := "```json\n{\n  \"title\": \"Building Go Services with HTMX\",\n  \"summary\": \"Learn how to combine Go and HTMX for fast web applications.\",\n  \"tags\": [\"go\", \"htmx\", \"web\"],\n  \"body\": \"# Building Go Services with HTMX\\n\\nGo and HTMX make web development enjoyable.\"\n}\n```"

		resp := openAIChatResponse{
			Choices: []struct {
				Message openAIMessage `json:"message"`
			}{
				{
					Message: openAIMessage{
						Role:    "assistant",
						Content: jsonPayload,
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	cfg := AIConfig{
		BaseURL: mockServer.URL,
		APIKey:  "test-api-key",
		Model:   "gpt-4o-mini",
	}

	result, err := GenerateAIBlogPost(context.Background(), cfg, "Go and HTMX")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Title != "Building Go Services with HTMX" {
		t.Errorf("unexpected title: %s", result.Title)
	}
	if result.Summary != "Learn how to combine Go and HTMX for fast web applications." {
		t.Errorf("unexpected summary: %s", result.Summary)
	}
	if len(result.Tags) != 3 || result.Tags[0] != "go" {
		t.Errorf("unexpected tags: %v", result.Tags)
	}
	if result.Slug != "building-go-services-with-htmx" {
		t.Errorf("unexpected slug: %s", result.Slug)
	}
}

func TestCleanCodeFences(t *testing.T) {
	input := "```json\n{\"title\": \"Hello\"}\n```"
	expected := "{\"title\": \"Hello\"}"
	cleaned := cleanCodeFences(input)
	if cleaned != expected {
		t.Errorf("cleanCodeFences failed: got %q, expected %q", cleaned, expected)
	}
}
