package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeRelPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"/", ""},
		{"..", ""},
		{"../../etc/passwd", "etc/passwd"},
		{"sub/dir", "sub/dir"},
		{"/sub/dir/", "sub/dir"},
		{"images/logo.png", "images/logo.png"},
	}

	for _, tt := range tests {
		got := sanitizeRelPath(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeRelPath(%q) = %q; want %q", tt.input, got, tt.want)
		}
	}
}

func TestFilesHandlerStreaming(t *testing.T) {
	wwwDir := filepath.Join("storage", "persisted", "www")
	if err := os.MkdirAll(wwwDir, 0755); err != nil {
		t.Fatalf("mkdir www: %v", err)
	}

	testFile := filepath.Join(wwwDir, "test_stream.txt")
	testContent := "Hello, public file streaming!"
	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	defer os.Remove(testFile)

	srv := &Server{}
	handler := srv.filesHandler()

	req := httptest.NewRequest(http.MethodGet, "/files/test_stream.txt", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
	if rec.Body.String() != testContent {
		t.Errorf("expected body %q, got %q", testContent, rec.Body.String())
	}
}
