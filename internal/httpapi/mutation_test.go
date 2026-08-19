package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/apikey"
	clientservice "github.com/yjrszcq/openvpn-docker/internal/client"
	runtimecontrol "github.com/yjrszcq/openvpn-docker/internal/runtime"
)

type fakeMutator struct {
	called  string
	id      string
	value   string
	result  clientservice.MutationResult
	address clientservice.AddressResult
	err     error
}

func (fake *fakeMutator) Create(_ context.Context, value clientservice.CreateRequest) (clientservice.MutationResult, error) {
	fake.called, fake.value = "create", value.Name+":"+value.IPv4
	return fake.result, fake.err
}
func (fake *fakeMutator) Rename(_ context.Context, selector clientservice.Selector, value string) (clientservice.MutationResult, error) {
	fake.called, fake.id, fake.value = "rename", selector.IDPrefix, value
	return fake.result, fake.err
}
func (fake *fakeMutator) Revoke(_ context.Context, selector clientservice.Selector, release bool) (clientservice.MutationResult, error) {
	fake.called, fake.id, fake.value = "revoke", selector.IDPrefix, map[bool]string{true: "release", false: "retain"}[release]
	return fake.result, fake.err
}
func (fake *fakeMutator) Reissue(_ context.Context, selector clientservice.Selector, value string) (clientservice.MutationResult, error) {
	fake.called, fake.id, fake.value = "reissue", selector.IDPrefix, value
	return fake.result, fake.err
}
func (fake *fakeMutator) Delete(_ context.Context, selector clientservice.Selector) (clientservice.MutationResult, error) {
	fake.called, fake.id = "delete", selector.IDPrefix
	return fake.result, fake.err
}
func (fake *fakeMutator) AddressSet(_ context.Context, selector clientservice.Selector, value string) (clientservice.AddressResult, error) {
	fake.called, fake.id, fake.value = "address-set", selector.IDPrefix, value
	return fake.address, fake.err
}
func (fake *fakeMutator) AddressRelease(_ context.Context, selector clientservice.Selector) (clientservice.AddressResult, error) {
	fake.called, fake.id = "address-release", selector.IDPrefix
	return fake.address, fake.err
}

func TestClientMutationRoutes(t *testing.T) {
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	tests := []struct {
		method, path, body, called, value string
		status                            int
	}{
		{http.MethodPost, "/api/v1/clients", `{"name":"laptop","ipv4":"auto"}`, "create", "laptop:auto", http.StatusCreated},
		{http.MethodPatch, "/api/v1/clients/" + id, `{"name":"renamed"}`, "rename", "renamed", http.StatusOK},
		{http.MethodPost, "/api/v1/clients/" + id + "/revoke", `{"release_ipv4":true}`, "revoke", "release", http.StatusOK},
		{http.MethodPost, "/api/v1/clients/" + id + "/reissue", `{"ipv4":"dynamic"}`, "reissue", "dynamic", http.StatusOK},
		{http.MethodDelete, "/api/v1/clients/" + id, "", "delete", "", http.StatusOK},
		{http.MethodPut, "/api/v1/clients/" + id + "/ipv4", `{"mode":"static","address":"10.42.0.10"}`, "address-set", "10.42.0.10", http.StatusOK},
		{http.MethodDelete, "/api/v1/clients/" + id + "/ipv4", "", "address-release", "", http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.called, func(t *testing.T) {
			mutator := &fakeMutator{result: clientservice.MutationResult{Version: 1}, address: clientservice.AddressResult{Version: 1}}
			handler := mutationHandler(t, mutator, fakeClients{})
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer valid-token")
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status || mutator.called != test.called || mutator.value != test.value {
				t.Fatalf("response=%d body=%q called=%q value=%q", response.Code, response.Body.String(), mutator.called, mutator.value)
			}
			if test.status == http.StatusCreated && response.Header().Get("Location") == "" {
				t.Fatalf("created response has no Location: headers=%v", response.Header())
			}
		})
	}
}

func TestMutationRoutesDeclareMethods(t *testing.T) {
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	handler := mutationHandler(t, &fakeMutator{}, fakeClients{})
	for _, test := range []struct{ method, path, allow string }{
		{http.MethodPatch, "/api/v1/clients", "GET, POST"},
		{http.MethodPost, "/api/v1/clients/" + id, "GET, PATCH, DELETE"},
		{http.MethodDelete, "/api/v1/clients/" + id + "/revoke", "POST"},
		{http.MethodPatch, "/api/v1/clients/" + id + "/ipv4", "PUT, DELETE"},
		{http.MethodPost, "/api/v1/clients/" + id + "/profile", "GET"},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		request.Header.Set("Authorization", "Bearer valid-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != test.allow {
			t.Fatalf("%s %s response=%d allow=%q", test.method, test.path, response.Code, response.Header().Get("Allow"))
		}
	}
}

func TestCommittedMutationReportsRuntimeUnavailable(t *testing.T) {
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	mutator := &fakeMutator{result: clientservice.MutationResult{Version: 1, KickRequired: true, Client: clientservice.View{ID: id, Name: "laptop"}}}
	handler, err := NewHandler(fakeAuthenticator{key: apikey.Key{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}, nil, Resources{
		Clients: fakeClients{view: clientservice.View{ID: id, Name: "laptop"}}, Mutations: mutator,
		Disconnect: func(context.Context, string, string) (runtimecontrol.DisconnectResult, error) {
			return runtimecontrol.DisconnectResult{}, runtimecontrol.ErrUnavailable
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/clients/"+id+"/revoke", strings.NewReader(`{"release_ipv4":false}`))
	request.Header.Set("Authorization", "Bearer valid-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"runtime":{"status":"unavailable"}`) {
		t.Fatalf("response=%d body=%q", response.Code, response.Body.String())
	}
}

func TestDisconnectMapsRuntimeUnavailable(t *testing.T) {
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	handler, err := NewHandler(fakeAuthenticator{key: apikey.Key{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}, nil, Resources{
		Clients: fakeClients{view: clientservice.View{ID: id, Name: "laptop"}},
		Disconnect: func(context.Context, string, string) (runtimecontrol.DisconnectResult, error) {
			return runtimecontrol.DisconnectResult{}, runtimecontrol.ErrUnavailable
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/clients/"+id+"/disconnect", nil)
	request.Header.Set("Authorization", "Bearer valid-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"kind":"runtime_unavailable"`) {
		t.Fatalf("response=%d body=%q", response.Code, response.Body.String())
	}
}

func TestProfileDownloadHeadersAndErrors(t *testing.T) {
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	handler := mutationHandler(t, &fakeMutator{}, fakeClients{view: clientservice.View{ID: id, Name: "laptop"}})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clients/"+id+"/profile", nil)
	request.Header.Set("Authorization", "Bearer valid-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "client\n" || response.Header().Get("Content-Type") != "application/x-openvpn-profile" || !strings.Contains(response.Header().Get("Content-Disposition"), "laptop.ovpn") {
		t.Fatalf("response=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}

	handler = mutationHandler(t, &fakeMutator{err: clientservice.ErrInvalidRequest}, fakeClients{})
	request = httptest.NewRequest(http.MethodPost, "/api/v1/clients", strings.NewReader(`{"name":"bad name","ipv4":"auto"}`))
	request.Header.Set("Authorization", "Bearer valid-token")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || strings.Contains(response.Body.String(), "bad name") {
		t.Fatalf("response=%d body=%q", response.Code, response.Body.String())
	}
}

func TestMutationRejectsInvalidJSONAndIPv4(t *testing.T) {
	mutator := &fakeMutator{}
	handler := mutationHandler(t, mutator, fakeClients{})
	for _, body := range []string{`{"name":"a","unknown":true}`, `{"name":"a","name":"b"}`} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/clients", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer valid-token")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || mutator.called != "" {
			t.Fatalf("response=%d called=%q", response.Code, mutator.called)
		}
	}
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	request := httptest.NewRequest(http.MethodPut, "/api/v1/clients/"+id+"/ipv4", strings.NewReader(`{"mode":"auto","address":"10.42.0.10"}`))
	request.Header.Set("Authorization", "Bearer valid-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("response=%d body=%q", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/clients/not-a-uuid/revoke", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer valid-token")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"kind":"invalid_client_id"`) {
		t.Fatalf("response=%d body=%q", response.Code, response.Body.String())
	}
}

func TestBodylessMutationRejectsChunkedBodyAndQuery(t *testing.T) {
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	mutator := &fakeMutator{}
	handler := mutationHandler(t, mutator, fakeClients{})
	for _, target := range []string{"/api/v1/clients/" + id, "/api/v1/clients/" + id + "?force=true"} {
		request := httptest.NewRequest(http.MethodDelete, target, strings.NewReader("x"))
		request.ContentLength = -1
		request.Header.Set("Authorization", "Bearer valid-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || mutator.called != "" {
			t.Fatalf("target=%s response=%d called=%q", target, response.Code, mutator.called)
		}
	}
}

func mutationHandler(t *testing.T, mutator ClientMutator, clients ClientReader) http.Handler {
	t.Helper()
	handler, err := NewHandler(fakeAuthenticator{key: apikey.Key{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}, nil, Resources{Clients: clients, Mutations: mutator})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}
