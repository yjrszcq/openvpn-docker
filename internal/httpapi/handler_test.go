package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/apikey"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

type fakeAuthenticator struct {
	key apikey.Key
	err error
}

func (fake fakeAuthenticator) Authenticate(_ context.Context, token string) (apikey.Key, error) {
	if fake.err != nil {
		return apikey.Key{}, fake.err
	}
	if token != "valid-token" {
		return apikey.Key{}, apikey.ErrUnauthenticated
	}
	return fake.key, nil
}

func TestHealthIsMinimalAndUnauthenticated(t *testing.T) {
	handler := newTestHandler(t, fakeAuthenticator{})
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("health response=%d %q", response.Code, response.Body.String())
	}
	if !domain.ValidUUID(response.Header().Get("X-Request-ID")) || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("health headers=%v", response.Header())
	}
}

func TestHealthRejectsMethodsAndQueries(t *testing.T) {
	handler := newTestHandler(t, fakeAuthenticator{})
	for _, test := range []struct {
		method string
		target string
		status int
	}{
		{http.MethodHead, "/healthz", http.StatusMethodNotAllowed},
		{http.MethodGet, "/healthz?details=true", http.StatusBadRequest},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(test.method, test.target, nil))
		if response.Code != test.status || !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("%s %s response=%d headers=%v", test.method, test.target, response.Code, response.Header())
		}
	}
}

func TestVersionedRoutesRequireOneBoundedBearerCredential(t *testing.T) {
	handler := newTestHandler(t, fakeAuthenticator{key: apikey.Key{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}})
	for _, authorization := range []string{"", "Basic abc", "Bearer ", "Bearer bad token", "Bearer " + strings.Repeat("x", maxAuthorizationBytes)} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" || !strings.Contains(response.Body.String(), `"kind":"unauthenticated"`) {
			t.Fatalf("authorization length=%d response=%d headers=%v body=%q", len(authorization), response.Code, response.Header(), response.Body.String())
		}
	}
}

func TestAuthenticationStorageFailureIsSanitized(t *testing.T) {
	handler := newTestHandler(t, fakeAuthenticator{err: errors.New("SELECT secret_digest FROM private_path")})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	request.Header.Set("Authorization", "Bearer valid-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"kind":"authentication_unavailable"`) || strings.Contains(response.Body.String(), "SELECT") {
		t.Fatalf("response=%d body=%q", response.Code, response.Body.String())
	}
}

func TestAuthenticatedUnknownRouteAndCORS(t *testing.T) {
	handler, err := NewHandler(fakeAuthenticator{key: apikey.Key{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}, []string{"https://console.example"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/future", nil)
	request.Header.Set("Authorization", "Bearer valid-token")
	request.Header.Set("Origin", "https://console.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || response.Header().Get("Access-Control-Allow-Origin") != "https://console.example" || response.Header().Get("Vary") != "Origin" {
		t.Fatalf("response=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
}

func TestCORSPreflightUsesExactOriginsAndHeaders(t *testing.T) {
	handler, err := NewHandler(fakeAuthenticator{}, []string{"https://console.example"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/clients", nil)
	request.Header.Set("Origin", "https://console.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatalf("preflight response=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}

	request = httptest.NewRequest(http.MethodOptions, "/api/v1/clients", nil)
	request.Header.Set("Origin", "https://other.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("refused preflight response=%d headers=%v", response.Code, response.Header())
	}
}

func TestOriginValidation(t *testing.T) {
	for _, origin := range []string{"*", "https://example.test/path", "file://example.test", "https://user@example.test"} {
		if _, err := NewHandler(fakeAuthenticator{}, []string{origin}); err == nil {
			t.Fatalf("origin %q was accepted", origin)
		}
	}
	if _, err := NewHandler(nil, nil); err == nil {
		t.Fatal("nil authenticator was accepted")
	}
}

func newTestHandler(t *testing.T, authenticator Authenticator) http.Handler {
	t.Helper()
	handler, err := NewHandler(authenticator, nil)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}
