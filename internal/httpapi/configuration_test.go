package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/apikey"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	configurationservice "github.com/yjrszcq/openvpn-docker/internal/configuration"
	runtimecontrol "github.com/yjrszcq/openvpn-docker/internal/runtime"
)

const testConfigurationDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestConfigurationRoutesAndPreconditions(t *testing.T) {
	var putDigest string
	var applyInput ConfigApplyRequest
	resources := testConfigurationResources()
	resources.PutDesired = func(_ context.Context, digest string, view configservice.View) (configservice.DesiredView, error) {
		putDigest = digest
		return configservice.DesiredView{Digest: digest, Config: view}, nil
	}
	resources.ConfigApply = func(_ context.Context, input ConfigApplyRequest) (configurationservice.ApplyResult, error) {
		applyInput = input
		return configurationservice.ApplyResult{Version: 1}, nil
	}
	handler := newConfigurationHandler(t, resources)

	for _, test := range []struct {
		method string
		path   string
		body   string
		header string
		want   string
		etag   bool
	}{
		{http.MethodGet, "/api/v1/config/applied", "", "", `"revision":4`, false},
		{http.MethodGet, "/api/v1/config/desired", "", "", `"digest":"` + testConfigurationDigest + `"`, true},
		{http.MethodGet, "/api/v1/config/plan", "", "", `"version":1`, false},
		{http.MethodPut, "/api/v1/config/desired", `{"version":1}`, `"` + testConfigurationDigest + `"`, `"version":1`, true},
		{http.MethodPost, "/api/v1/config/apply", `{"desired_digest":"` + testConfigurationDigest + `","current_revision":4,"force":true}`, "", `"version":1`, false},
	} {
		request := authenticatedConfigurationRequest(test.method, test.path, test.body)
		if test.header != "" {
			request.Header.Set("If-Match", test.header)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.want) {
			t.Fatalf("%s %s response=%d body=%q", test.method, test.path, response.Code, response.Body.String())
		}
		if test.etag && response.Header().Get("ETag") != `"`+testConfigurationDigest+`"` {
			t.Fatalf("%s %s ETag=%q", test.method, test.path, response.Header().Get("ETag"))
		}
	}
	if putDigest != testConfigurationDigest {
		t.Fatalf("put digest=%q", putDigest)
	}
	if applyInput.DesiredDigest != testConfigurationDigest || applyInput.CurrentRevision != 4 || !applyInput.Force {
		t.Fatalf("apply input=%+v", applyInput)
	}
}

func TestConfigurationRoutesRejectMalformedPreconditionsAndJSON(t *testing.T) {
	handler := newConfigurationHandler(t, testConfigurationResources())
	for _, test := range []struct {
		method string
		path   string
		body   string
		header string
		status int
		kind   string
	}{
		{http.MethodPut, "/api/v1/config/desired", `{}`, "", http.StatusBadRequest, "invalid_if_match"},
		{http.MethodPut, "/api/v1/config/desired", `{"unknown":true}`, `"` + testConfigurationDigest + `"`, http.StatusBadRequest, "invalid_json"},
		{http.MethodPost, "/api/v1/config/apply", `{"desired_digest":"` + strings.ToUpper(testConfigurationDigest) + `","current_revision":4}`, "", http.StatusUnprocessableEntity, "invalid_configuration"},
		{http.MethodPost, "/api/v1/config/apply", `{"desired_digest":"` + testConfigurationDigest + `","current_revision":4,"extra":true}`, "", http.StatusBadRequest, "invalid_json"},
	} {
		request := authenticatedConfigurationRequest(test.method, test.path, test.body)
		if test.header != "" {
			request.Header.Set("If-Match", test.header)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status || !strings.Contains(response.Body.String(), `"kind":"`+test.kind+`"`) {
			t.Fatalf("%s %s response=%d body=%q", test.method, test.path, response.Code, response.Body.String())
		}
	}
}

func TestConfigurationErrorsAreSanitizedAndClassified(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		kind   string
	}{
		{configservice.ErrDesiredConflict, http.StatusConflict, "configuration_conflict"},
		{configservice.ErrInvalidDesired, http.StatusUnprocessableEntity, "invalid_configuration"},
		{runtimecontrol.ErrControlUnavailable, http.StatusServiceUnavailable, "runtime_unavailable"},
		{errors.New("open /private/config.yaml: permission denied"), http.StatusInternalServerError, "internal_error"},
	} {
		resources := testConfigurationResources()
		resources.ConfigApply = func(context.Context, ConfigApplyRequest) (configurationservice.ApplyResult, error) {
			return configurationservice.ApplyResult{}, test.err
		}
		handler := newConfigurationHandler(t, resources)
		request := authenticatedConfigurationRequest(http.MethodPost, "/api/v1/config/apply", `{"desired_digest":"`+testConfigurationDigest+`","current_revision":4}`)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status || !strings.Contains(response.Body.String(), `"kind":"`+test.kind+`"`) || strings.Contains(response.Body.String(), "/private/") {
			t.Fatalf("error=%v response=%d body=%q", test.err, response.Code, response.Body.String())
		}
	}
}

func TestConfigurationResourcesMustBeComplete(t *testing.T) {
	_, err := NewHandler(fakeAuthenticator{}, nil, Resources{Desired: func(context.Context) (configservice.DesiredView, error) {
		return configservice.DesiredView{}, nil
	}})
	if err == nil {
		t.Fatal("partial configuration resources were accepted")
	}
}

func testConfigurationResources() Resources {
	return Resources{
		Applied: func(context.Context) (configservice.AppliedView, error) {
			return configservice.AppliedView{Revision: 4, Digest: testConfigurationDigest}, nil
		},
		Desired: func(context.Context) (configservice.DesiredView, error) {
			return configservice.DesiredView{Digest: testConfigurationDigest}, nil
		},
		PutDesired: func(_ context.Context, digest string, view configservice.View) (configservice.DesiredView, error) {
			return configservice.DesiredView{Digest: digest, Config: view}, nil
		},
		ConfigPlan: func(context.Context) (configurationservice.Plan, error) {
			return configurationservice.Plan{Version: 1}, nil
		},
		ConfigApply: func(context.Context, ConfigApplyRequest) (configurationservice.ApplyResult, error) {
			return configurationservice.ApplyResult{Version: 1}, nil
		},
	}
}

func newConfigurationHandler(t *testing.T, resources Resources) http.Handler {
	t.Helper()
	handler, err := NewHandler(fakeAuthenticator{key: apikey.Key{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}, nil, resources)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func authenticatedConfigurationRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer valid-token")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}
