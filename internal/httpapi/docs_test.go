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
		{"/docs/style.css", "text/css", ".parameter-table"},
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

func TestDocumentationShowsExplicitRequestContracts(t *testing.T) {
	javascript, err := documentationFiles.ReadFile("docs/app.js")
	if err != nil {
		t.Fatal(err)
	}
	application := string(javascript)
	for _, expected := range []string{
		`name: "Authorization"`,
		`location: "header"`,
		`type: "string"`,
		`format: "Bearer ovpn_v1.<uuid>.<secret>"`,
		`["字段", "位置", "类型", "必填", "格式 / 示例 / 约束", "用途说明"]`,
		`"Header 参数"`,
		`"Path 参数"`,
		`"Query 参数"`,
		`"JSON Body 字段"`,
		`"contract-tabs"`,
		`tabs.setAttribute("role", "tablist")`,
		`tab.setAttribute("role", "tab")`,
		`tab.setAttribute("aria-controls", panes[index].id)`,
		`tab.addEventListener("click", () => activate(index))`,
		`activate(0)`,
	} {
		if !strings.Contains(application, expected) {
			t.Fatalf("documentation JavaScript does not contain %q", expected)
		}
	}
	if strings.Contains(application, "Host: vpn-admin.example.com") {
		t.Fatal("documentation request example still contains a synthetic Host header")
	}

	stylesheet, err := documentationFiles.ReadFile("docs/style.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(stylesheet)
	if !strings.Contains(styles, `.contract-tab[aria-selected="true"]`) || !strings.Contains(styles, ".contract-pane[hidden]") {
		t.Fatal("documentation contracts must render as switchable request and response tabs")
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
