package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/apikey"
	"github.com/yjrszcq/openvpn-docker/internal/auditactor"
	"github.com/yjrszcq/openvpn-docker/internal/buildinfo"
	clientservice "github.com/yjrszcq/openvpn-docker/internal/client"
	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	runtimecontrol "github.com/yjrszcq/openvpn-docker/internal/runtime"
	statecontrol "github.com/yjrszcq/openvpn-docker/internal/state"
)

type fakeAuthenticator struct {
	key apikey.Key
	err error
}

type fakeClients struct {
	list clientservice.ListResult
	view clientservice.View
	err  error
}

func (fake fakeClients) List(context.Context) (clientservice.ListResult, error) {
	return fake.list, fake.err
}
func (fake fakeClients) Get(context.Context, string) (clientservice.View, error) {
	return fake.view, fake.err
}
func (fake fakeClients) Export(context.Context, clientservice.Selector) ([]byte, clientservice.View, error) {
	return []byte("client\n"), fake.view, fake.err
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
	handler, err := NewHandler(fakeAuthenticator{key: apikey.Key{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}, []string{"https://console.example"}, Resources{})
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
	handler, err := NewHandler(fakeAuthenticator{}, []string{"https://console.example"}, Resources{})
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
		if _, err := NewHandler(fakeAuthenticator{}, []string{origin}, Resources{}); err == nil {
			t.Fatalf("origin %q was accepted", origin)
		}
	}
	if _, err := NewHandler(nil, nil, Resources{}); err == nil {
		t.Fatal("nil authenticator was accepted")
	}
}

func TestReadResourceRoutingAndErrors(t *testing.T) {
	key := apikey.Key{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}
	clientID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	resources := Resources{
		Version: func() VersionResponse { return VersionResponse{Info: buildinfo.Current()} },
		Clients: fakeClients{list: clientservice.ListResult{Version: 1}, view: clientservice.View{ID: clientID, Name: "laptop"}},
		Runtime: func(context.Context) (runtimecontrol.Status, error) {
			return runtimecontrol.Status{}, runtimecontrol.ErrUnavailable
		},
		Events: func(_ context.Context, lines int) ([]runtimecontrol.Event, error) {
			return []runtimecontrol.Event{{"lines": lines}}, nil
		},
	}
	handler, err := NewHandler(fakeAuthenticator{key: key}, nil, resources)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path   string
		status int
		body   string
	}{
		{"/api/v1/version", http.StatusOK, `"data_schema":4`},
		{"/api/v1/clients", http.StatusOK, `"clients":null`},
		{"/api/v1/clients/" + clientID, http.StatusOK, `"name":"laptop"`},
		{"/api/v1/clients/not-a-uuid", http.StatusBadRequest, `"kind":"invalid_client_id"`},
		{"/api/v1/runtime", http.StatusServiceUnavailable, `"kind":"runtime_unavailable"`},
		{"/api/v1/runtime/events?lines=12", http.StatusOK, `"lines":12`},
		{"/api/v1/runtime/events?other=12", http.StatusBadRequest, `"kind":"invalid_query"`},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.Header.Set("Authorization", "Bearer valid-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status || !strings.Contains(response.Body.String(), test.body) {
			t.Fatalf("GET %s response=%d body=%q", test.path, response.Code, response.Body.String())
		}
	}
}

func TestReadResourcesRejectMethods(t *testing.T) {
	handler, err := NewHandler(fakeAuthenticator{key: apikey.Key{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}, nil, Resources{Version: func() VersionResponse { return VersionResponse{Info: buildinfo.Current()} }})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/version", nil)
	request.Header.Set("Authorization", "Bearer valid-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("response=%d headers=%v", response.Code, response.Header())
	}
}

func newTestHandler(t *testing.T, authenticator Authenticator) http.Handler {
	t.Helper()
	handler, err := NewHandler(authenticator, nil, Resources{})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestVersionAndStateResponsesUseAPIContract(t *testing.T) {
	contract := compatibility.Contract{Version: 1, SupportedOpenVPNVersions: []string{"2.7.6"}, Adapter: compatibility.Adapter{Name: "openvpn-2.7", TemplateFamily: "openvpn-2.7"}}
	version := NewVersionResponse(contract)
	if version.Compatibility.Adapter != "openvpn-2.7" || len(version.Compatibility.SupportedOpenVPNVersions) != 1 {
		t.Fatalf("version response=%+v", version)
	}
	report := statecontrol.Report{Version: 1, DataSchema: 4, Issues: []statecontrol.Issue{{ID: "TEST", OwnerID: "owner", ArtifactKind: "profile"}}, IssueCount: 1}
	summary := newStateResponse(report, false)
	doctor := newStateResponse(report, true)
	if summary.Issues != nil || len(doctor.Issues) != 1 || doctor.Issues[0].OwnerID != "owner" {
		t.Fatalf("summary=%+v doctor=%+v", summary, doctor)
	}
}

func TestAuthenticatedIdentityReachesResources(t *testing.T) {
	keyID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	resources := Resources{State: func(ctx context.Context) (statecontrol.Report, error) {
		key, keyOK := AuthenticatedKey(ctx)
		actor, actorOK := auditactor.From(ctx)
		if !keyOK || key.ID != keyID || !actorOK || actor.Kind != "api-key" || actor.ID != keyID {
			t.Fatalf("key=%+v keyOK=%t actor=%+v actorOK=%t", key, keyOK, actor, actorOK)
		}
		return statecontrol.Report{Version: 1}, nil
	}}
	handler, err := NewHandler(fakeAuthenticator{key: apikey.Key{ID: keyID}}, nil, resources)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	request.Header.Set("Authorization", "Bearer valid-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("response=%d body=%q", response.Code, response.Body.String())
	}
}
