package httpapi

import (
	"embed"
	"net/http"
	"strconv"
	"strings"
)

//go:embed docs/index.html docs/app.js docs/style.css docs/openapi.json
var documentationFiles embed.FS

const documentationCSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func (handler *handler) routeDocumentation(response http.ResponseWriter, request *http.Request, requestID string) bool {
	if request.URL.Path != "/docs" && !strings.HasPrefix(request.URL.Path, "/docs/") {
		return false
	}
	response.Header().Set("Content-Security-Policy", documentationCSP)
	if request.URL.RawQuery != "" {
		writeAPIError(response, http.StatusBadRequest, "invalid_query", "query parameters are not accepted", requestID)
		return true
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writeAPIError(response, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed", requestID)
		return true
	}
	if request.URL.Path == "/docs" {
		http.Redirect(response, request, "/docs/", http.StatusPermanentRedirect)
		return true
	}

	name, contentType := "", ""
	switch request.URL.Path {
	case "/docs/":
		name, contentType = "docs/index.html", "text/html; charset=utf-8"
	case "/docs/app.js":
		name, contentType = "docs/app.js", "text/javascript; charset=utf-8"
	case "/docs/style.css":
		name, contentType = "docs/style.css", "text/css; charset=utf-8"
	case "/docs/openapi.json":
		name, contentType = "docs/openapi.json", "application/vnd.oai.openapi+json; charset=utf-8"
	default:
		writeAPIError(response, http.StatusNotFound, "not_found", "resource was not found", requestID)
		return true
	}
	content, err := documentationFiles.ReadFile(name)
	if err != nil {
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "documentation could not be loaded", requestID)
		return true
	}
	response.Header().Set("Content-Type", contentType)
	response.Header().Set("Content-Length", strconv.Itoa(len(content)))
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(content)
	return true
}
