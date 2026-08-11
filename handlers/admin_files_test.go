package handlers

import (
	"net/http"
	"net/http/httptest"
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
	srv := &Server{}
	handler := srv.filesHandler()

	req := httptest.NewRequest(http.MethodGet, "/files/test_stream.txt", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found when S3 is unconfigured, got %d", rec.Code)
	}
}
