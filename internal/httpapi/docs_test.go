package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDocumentationIsEmbeddedAndPublic(t *testing.T) {
	handler := newTestHandler(t, fakeAuthenticator{err: errors.New("authentication must not be called")})
	tests := []struct {
		path        string
		contentType string
		body        string
	}{
		{"/docs/", "text/html", "API 接口文档"},
		{"/docs/app.js", "text/javascript", "renderOperation"},
		{"/docs/style.css", "text/css", ".contract-grid"},
		{"/docs/openapi.json", "application/vnd.oai.openapi+json", `"openapi": "3.1.0"`},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), test.contentType) || !strings.Contains(response.Body.String(), test.body) {
			t.Fatalf("GET %s response=%d headers=%v body=%q", test.path, response.Code, response.Header(), response.Body.String())
		}
		if response.Header().Get("Content-Security-Policy") != documentationCSP || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("GET %s security headers=%v", test.path, response.Header())
		}
	}
}

func TestEmbeddedDocumentationDescribesEveryOperation(t *testing.T) {
	handler := newTestHandler(t, fakeAuthenticator{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/docs/openapi.json", nil))
	var document struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, path := range document.Paths {
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			if path[method] != nil {
				count++
			}
		}
	}
	if count != 22 {
		t.Fatalf("embedded operations=%d want=22", count)
	}
}

func TestDocumentationAssetsHaveNoExternalDependencies(t *testing.T) {
	for _, name := range []string{"docs/index.html", "docs/app.js", "docs/style.css"} {
		content, err := documentationFiles.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		value := strings.ToLower(string(content))
		if strings.Contains(value, "http://") || strings.Contains(value, "https://") {
			t.Fatalf("%s references an external dependency", name)
		}
	}
}

func TestDocumentationRoutingIsStrict(t *testing.T) {
	handler := newTestHandler(t, fakeAuthenticator{})
	tests := []struct {
		method   string
		path     string
		status   int
		location string
	}{
		{http.MethodGet, "/docs", http.StatusPermanentRedirect, "/docs/"},
		{http.MethodPost, "/docs/", http.StatusMethodNotAllowed, ""},
		{http.MethodGet, "/docs/?theme=dark", http.StatusBadRequest, ""},
		{http.MethodGet, "/docs/missing.js", http.StatusNotFound, ""},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != test.status || response.Header().Get("Location") != test.location {
			t.Fatalf("%s %s response=%d headers=%v", test.method, test.path, response.Code, response.Header())
		}
		if test.status == http.StatusMethodNotAllowed && response.Header().Get("Allow") != http.MethodGet {
			t.Fatalf("method response headers=%v", response.Header())
		}
	}
}
