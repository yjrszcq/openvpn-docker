// Package httpapi exposes the versioned REST management surface.
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/yjrszcq/openvpn-docker/internal/apikey"
	"github.com/yjrszcq/openvpn-docker/internal/auditactor"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

const maxAuthorizationBytes = 512

type Authenticator interface {
	Authenticate(context.Context, string) (apikey.Key, error)
}

type handler struct {
	authenticator Authenticator
	origins       map[string]struct{}
}

type keyContextKey struct{}

// AuthenticatedKey returns the API key attached by the authentication layer.
func AuthenticatedKey(ctx context.Context) (apikey.Key, bool) {
	key, ok := ctx.Value(keyContextKey{}).(apikey.Key)
	return key, ok && key.ID != ""
}

// NewHandler constructs the HTTP foundation. Resource handlers are added in
// later phases; every versioned route is authenticated before route lookup.
func NewHandler(authenticator Authenticator, allowedOrigins []string) (http.Handler, error) {
	if authenticator == nil {
		return nil, errors.New("API authenticator is required")
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
	return &handler{authenticator: authenticator, origins: origins}, nil
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
	writeAPIError(response, http.StatusNotFound, "not_found", "resource was not found", requestID)
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
