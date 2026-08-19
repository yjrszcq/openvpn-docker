// Package httpapi exposes the versioned REST management surface.
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/apikey"
	"github.com/yjrszcq/openvpn-docker/internal/auditactor"
	"github.com/yjrszcq/openvpn-docker/internal/buildinfo"
	clientservice "github.com/yjrszcq/openvpn-docker/internal/client"
	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	configurationservice "github.com/yjrszcq/openvpn-docker/internal/configuration"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	runtimecontrol "github.com/yjrszcq/openvpn-docker/internal/runtime"
	statecontrol "github.com/yjrszcq/openvpn-docker/internal/state"
)

const maxAuthorizationBytes = 512

type Authenticator interface {
	Authenticate(context.Context, string) (apikey.Key, error)
}

type handler struct {
	authenticator Authenticator
	origins       map[string]struct{}
	resources     Resources
}

type ClientReader interface {
	List(context.Context) (clientservice.ListResult, error)
	Get(context.Context, string) (clientservice.View, error)
	Export(context.Context, clientservice.Selector) ([]byte, clientservice.View, error)
}

type ClientMutator interface {
	Create(context.Context, clientservice.CreateRequest) (clientservice.MutationResult, error)
	Rename(context.Context, clientservice.Selector, string) (clientservice.MutationResult, error)
	Revoke(context.Context, clientservice.Selector, bool) (clientservice.MutationResult, error)
	Reissue(context.Context, clientservice.Selector, string) (clientservice.MutationResult, error)
	Delete(context.Context, clientservice.Selector) (clientservice.MutationResult, error)
	AddressSet(context.Context, clientservice.Selector, string) (clientservice.AddressResult, error)
	AddressRelease(context.Context, clientservice.Selector) (clientservice.AddressResult, error)
}

type Resources struct {
	Version     func() VersionResponse
	State       func(context.Context) (statecontrol.Report, error)
	Clients     ClientReader
	Mutations   ClientMutator
	Runtime     func(context.Context) (runtimecontrol.Status, error)
	Events      func(context.Context, int) ([]runtimecontrol.Event, error)
	Disconnect  func(context.Context, string, string) (runtimecontrol.DisconnectResult, error)
	Applied     func(context.Context) (configservice.AppliedView, error)
	Desired     func(context.Context) (configservice.DesiredView, error)
	PutDesired  func(context.Context, string, configservice.View) (configservice.DesiredView, error)
	ConfigPlan  func(context.Context) (configurationservice.Plan, error)
	ConfigApply func(context.Context, ConfigApplyRequest) (configurationservice.ApplyResult, error)
}

type VersionResponse struct {
	buildinfo.Info
	Compatibility CompatibilityResponse `json:"compatibility"`
}

type CompatibilityResponse struct {
	ContractVersion          int      `json:"contract_version"`
	Adapter                  string   `json:"adapter"`
	TemplateFamily           string   `json:"template_family"`
	SupportedOpenVPNVersions []string `json:"supported_openvpn_versions"`
}

func NewVersionResponse(contract compatibility.Contract) VersionResponse {
	return VersionResponse{
		Info: buildinfo.Current(),
		Compatibility: CompatibilityResponse{
			ContractVersion: contract.Version, Adapter: contract.Adapter.Name,
			TemplateFamily:           contract.Adapter.TemplateFamily,
			SupportedOpenVPNVersions: append([]string(nil), contract.SupportedOpenVPNVersions...),
		},
	}
}

type stateResponse struct {
	Version               int                         `json:"version"`
	State                 statecontrol.Classification `json:"state"`
	DataSchema            int                         `json:"data_schema"`
	InstanceID            string                      `json:"instance_id,omitempty"`
	Revision              uint64                      `json:"revision,omitempty"`
	ScannedAt             time.Time                   `json:"scanned_at"`
	IssueCount            int                         `json:"issue_count"`
	PendingOperationCount int                         `json:"pending_operation_count"`
	Issues                []stateIssueResponse        `json:"issues,omitempty"`
}

type stateIssueResponse struct {
	ID           string                `json:"id"`
	Severity     statecontrol.Severity `json:"severity"`
	Action       string                `json:"action"`
	Target       string                `json:"target,omitempty"`
	OwnerID      string                `json:"owner_id,omitempty"`
	ArtifactKind string                `json:"artifact_kind,omitempty"`
	Detail       string                `json:"detail"`
}

type keyContextKey struct{}

// AuthenticatedKey returns the API key attached by the authentication layer.
func AuthenticatedKey(ctx context.Context) (apikey.Key, bool) {
	key, ok := ctx.Value(keyContextKey{}).(apikey.Key)
	return key, ok && key.ID != ""
}

// NewHandler constructs the authenticated HTTP management surface.
func NewHandler(authenticator Authenticator, allowedOrigins []string, resources Resources) (http.Handler, error) {
	if authenticator == nil {
		return nil, errors.New("API authenticator is required")
	}
	if resources.Mutations != nil && resources.Clients == nil {
		return nil, errors.New("API client mutations require client queries")
	}
	if resources.Disconnect != nil && resources.Clients == nil {
		return nil, errors.New("API disconnect requires client queries")
	}
	configurationResources := 0
	for _, available := range []bool{
		resources.Applied != nil,
		resources.Desired != nil,
		resources.PutDesired != nil,
		resources.ConfigPlan != nil,
		resources.ConfigApply != nil,
	} {
		if available {
			configurationResources++
		}
	}
	if configurationResources != 0 && configurationResources != 5 {
		return nil, errors.New("API configuration resources must be provided together")
	}
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		if err := validateOrigin(origin); err != nil {
			return nil, err
		}
		if _, exists := origins[origin]; exists {
			return nil, errors.New("CORS origins must be unique")
		}
		origins[origin] = struct{}{}
	}
	return &handler{authenticator: authenticator, origins: origins, resources: resources}, nil
}

func (handler *handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	requestID, err := domain.GenerateUUID()
	if err != nil {
		requestID = "00000000-0000-4000-8000-000000000000"
	}
	response.Header().Set("X-Request-ID", requestID)
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Cache-Control", "no-store")

	if request.URL.Path == "/healthz" {
		handler.health(response, request, requestID)
		return
	}
	if !versionedPath(request.URL.Path) {
		writeAPIError(response, http.StatusNotFound, "not_found", "resource was not found", requestID)
		return
	}
	if handler.preflight(response, request, requestID) {
		return
	}
	handler.applyCORS(response, request)
	key, authErr := handler.authenticate(request)
	if authErr != nil {
		if errors.Is(authErr, apikey.ErrUnauthenticated) {
			response.Header().Set("WWW-Authenticate", `Bearer realm="ovpn-api"`)
			writeAPIError(response, http.StatusUnauthorized, "unauthenticated", "API key is missing or invalid", requestID)
			return
		}
		writeAPIError(response, http.StatusServiceUnavailable, "authentication_unavailable", "API key authentication is unavailable", requestID)
		return
	}
	ctx := context.WithValue(request.Context(), keyContextKey{}, key)
	ctx = auditactor.With(ctx, auditactor.Actor{Kind: "api-key", ID: key.ID})
	request = request.WithContext(ctx)
	if handler.routeMutation(response, request, requestID) {
		return
	}
	if handler.routeConfiguration(response, request, requestID) {
		return
	}
	handler.routeRead(response, request, requestID)
}

func (handler *handler) routeRead(response http.ResponseWriter, request *http.Request, requestID string) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writeAPIError(response, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed", requestID)
		return
	}
	if request.URL.RawQuery != "" && request.URL.Path != "/api/v1/runtime/events" {
		writeAPIError(response, http.StatusBadRequest, "invalid_query", "query parameters are not accepted", requestID)
		return
	}
	ctx := request.Context()
	switch request.URL.Path {
	case "/api/v1/version":
		if handler.resources.Version != nil {
			writeJSON(response, http.StatusOK, handler.resources.Version())
			return
		}
	case "/api/v1/state", "/api/v1/state/doctor":
		if handler.resources.State != nil {
			value, err := handler.resources.State(ctx)
			handler.writeResult(response, newStateResponse(value, request.URL.Path == "/api/v1/state/doctor"), err, requestID)
			return
		}
	case "/api/v1/clients":
		if handler.resources.Clients != nil {
			value, err := handler.resources.Clients.List(ctx)
			handler.writeResult(response, value, err, requestID)
			return
		}
	case "/api/v1/runtime":
		if handler.resources.Runtime != nil {
			value, err := handler.resources.Runtime(ctx)
			handler.writeResult(response, value, err, requestID)
			return
		}
	case "/api/v1/runtime/events":
		if handler.resources.Events != nil {
			lines, err := parseLines(request)
			if err != nil {
				writeAPIError(response, http.StatusBadRequest, "invalid_query", "lines must be an integer between 0 and 1000", requestID)
				return
			}
			value, err := handler.resources.Events(ctx, lines)
			handler.writeResult(response, map[string]any{"version": 1, "events": value}, err, requestID)
			return
		}
	default:
		if handler.resources.Clients != nil && strings.HasSuffix(request.URL.Path, "/profile") {
			id := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/api/v1/clients/"), "/profile")
			if domain.ValidUUID(id) {
				handler.writeProfile(response, request, id, requestID)
				return
			}
		}
		if handler.resources.Clients != nil && strings.HasPrefix(request.URL.Path, "/api/v1/clients/") && strings.Count(strings.TrimPrefix(request.URL.Path, "/api/v1/clients/"), "/") == 0 {
			id := strings.TrimPrefix(request.URL.Path, "/api/v1/clients/")
			if domain.ValidUUID(id) {
				value, err := handler.resources.Clients.Get(ctx, id)
				handler.writeResult(response, value, err, requestID)
				return
			}
			writeAPIError(response, http.StatusBadRequest, "invalid_client_id", "client ID must be a complete UUID", requestID)
			return
		}
	}
	writeAPIError(response, http.StatusNotFound, "not_found", "resource was not found", requestID)
}

func newStateResponse(report statecontrol.Report, includeIssues bool) stateResponse {
	response := stateResponse{
		Version: report.Version, State: report.State, DataSchema: report.DataSchema,
		InstanceID: report.InstanceID, Revision: report.Revision, ScannedAt: report.ScannedAt,
		IssueCount: report.IssueCount, PendingOperationCount: report.PendingCount,
	}
	if includeIssues {
		response.Issues = make([]stateIssueResponse, len(report.Issues))
		for index, issue := range report.Issues {
			response.Issues[index] = stateIssueResponse{
				ID: issue.ID, Severity: issue.Severity, Action: issue.Action, Target: issue.Target,
				OwnerID: issue.OwnerID, ArtifactKind: issue.ArtifactKind, Detail: issue.Detail,
			}
		}
	}
	return response
}

func (handler *handler) writeResult(response http.ResponseWriter, value any, err error, requestID string) {
	switch {
	case err == nil:
		writeJSON(response, http.StatusOK, value)
	case errors.Is(err, clientservice.ErrNotFound):
		writeAPIError(response, http.StatusNotFound, "client_not_found", "client was not found", requestID)
	case errors.Is(err, clientservice.ErrInvalidRequest):
		writeAPIError(response, http.StatusBadRequest, "invalid_request", "request is invalid", requestID)
	case errors.Is(err, runtimecontrol.ErrUnavailable):
		writeAPIError(response, http.StatusServiceUnavailable, "runtime_unavailable", "OpenVPN runtime is unavailable", requestID)
	default:
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "request could not be completed", requestID)
	}
}

func parseLines(request *http.Request) (int, error) {
	if len(request.URL.Query()) > 1 {
		return 0, errors.New("unexpected query")
	}
	query := request.URL.Query()
	values, ok := query["lines"]
	if !ok && len(query) == 0 {
		return 100, nil
	}
	if !ok {
		return 0, errors.New("unexpected query")
	}
	if len(values) != 1 {
		return 0, errors.New("repeated lines")
	}
	value, err := strconv.Atoi(values[0])
	if err != nil || value < 0 || value > 1000 {
		return 0, errors.New("invalid lines")
	}
	return value, nil
}

func (handler *handler) health(response http.ResponseWriter, request *http.Request, requestID string) {
	if request.URL.RawQuery != "" {
		writeAPIError(response, http.StatusBadRequest, "invalid_query", "query parameters are not accepted", requestID)
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writeAPIError(response, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed", requestID)
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func (handler *handler) authenticate(request *http.Request) (apikey.Key, error) {
	values := request.Header.Values("Authorization")
	if len(values) != 1 || len(values[0]) > maxAuthorizationBytes || !strings.HasPrefix(values[0], "Bearer ") {
		return apikey.Key{}, apikey.ErrUnauthenticated
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return apikey.Key{}, apikey.ErrUnauthenticated
	}
	return handler.authenticator.Authenticate(request.Context(), token)
}

func (handler *handler) applyCORS(response http.ResponseWriter, request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if _, ok := handler.origins[origin]; !ok {
		return false
	}
	response.Header().Set("Access-Control-Allow-Origin", origin)
	response.Header().Add("Vary", "Origin")
	return true
}

func (handler *handler) preflight(response http.ResponseWriter, request *http.Request, requestID string) bool {
	if request.Method != http.MethodOptions || request.Header.Get("Access-Control-Request-Method") == "" {
		return false
	}
	if !handler.applyCORS(response, request) {
		writeAPIError(response, http.StatusBadRequest, "cors_origin_refused", "CORS origin is not allowed", requestID)
		return true
	}
	method := request.Header.Get("Access-Control-Request-Method")
	if !allowedCORSMethod(method) || !allowedCORSHeaders(request.Header.Get("Access-Control-Request-Headers")) {
		writeAPIError(response, http.StatusBadRequest, "cors_preflight_refused", "CORS preflight is not allowed", requestID)
		return true
	}
	response.Header().Set("Access-Control-Allow-Methods", method)
	response.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-Match")
	response.Header().Set("Access-Control-Max-Age", "600")
	response.WriteHeader(http.StatusNoContent)
	return true
}

func versionedPath(path string) bool {
	return path == "/api/v1" || strings.HasPrefix(path, "/api/v1/")
}

func validateOrigin(origin string) error {
	parsed, err := url.Parse(origin)
	if err != nil || origin == "" || origin == "*" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("CORS origins must be exact HTTP or HTTPS origins")
	}
	return nil
}

func allowedCORSMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func allowedCORSHeaders(value string) bool {
	if value == "" {
		return true
	}
	for _, header := range strings.Split(value, ",") {
		switch strings.ToLower(strings.TrimSpace(header)) {
		case "authorization", "content-type", "if-match":
		default:
			return false
		}
	}
	return true
}
