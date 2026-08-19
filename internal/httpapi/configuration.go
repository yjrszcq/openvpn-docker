package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	configurationservice "github.com/yjrszcq/openvpn-docker/internal/configuration"
	runtimecontrol "github.com/yjrszcq/openvpn-docker/internal/runtime"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

type ConfigApplyRequest struct {
	DesiredDigest   string `json:"desired_digest"`
	CurrentRevision uint64 `json:"current_revision"`
	Force           bool   `json:"force"`
}

func (handler *handler) routeConfiguration(response http.ResponseWriter, request *http.Request, requestID string) bool {
	path := request.URL.Path
	if handler.resources.Applied == nil || !strings.HasPrefix(path, "/api/v1/config/") {
		return false
	}
	if request.URL.RawQuery != "" {
		writeAPIError(response, http.StatusBadRequest, "invalid_query", "query parameters are not accepted", requestID)
		return true
	}
	switch path {
	case "/api/v1/config/applied":
		if request.Method != http.MethodGet {
			return rejectMethod(response, requestID, http.MethodGet)
		}
		value, err := handler.resources.Applied(request.Context())
		handler.writeConfigurationResult(response, value, err, requestID)
		return true
	case "/api/v1/config/desired":
		if request.Method == http.MethodGet {
			value, err := handler.resources.Desired(request.Context())
			handler.writeConfigurationResult(response, value, err, requestID)
			return true
		}
		if request.Method != http.MethodPut {
			return rejectMethod(response, requestID, http.MethodGet+", "+http.MethodPut)
		}
		expected, err := parseIfMatch(request.Header.Get("If-Match"))
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_if_match", "If-Match must contain one quoted digest", requestID)
			return true
		}
		var view configservice.View
		if err := decodeJSON(response, request, &view); err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_json", "request body is invalid", requestID)
			return true
		}
		value, err := handler.resources.PutDesired(request.Context(), expected, view)
		handler.writeConfigurationResult(response, value, err, requestID)
		return true
	case "/api/v1/config/plan":
		if request.Method != http.MethodGet {
			return rejectMethod(response, requestID, http.MethodGet)
		}
		value, err := handler.resources.ConfigPlan(request.Context())
		handler.writeConfigurationResult(response, value, err, requestID)
		return true
	case "/api/v1/config/apply":
		if request.Method != http.MethodPost {
			return rejectMethod(response, requestID, http.MethodPost)
		}
		var input ConfigApplyRequest
		if err := decodeJSON(response, request, &input); err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_json", "request body is invalid", requestID)
			return true
		}
		if !validDigest(input.DesiredDigest) || input.CurrentRevision == 0 {
			writeAPIError(response, http.StatusUnprocessableEntity, "invalid_configuration", "configuration apply preconditions are invalid", requestID)
			return true
		}
		value, err := handler.resources.ConfigApply(request.Context(), input)
		handler.writeConfigurationResult(response, value, err, requestID)
		return true
	}
	return false
}

func parseIfMatch(value string) (string, error) {
	if len(value) != 66 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", errors.New("invalid If-Match")
	}
	digest := value[1 : len(value)-1]
	if !validDigest(digest) {
		return "", errors.New("invalid digest")
	}
	return digest, nil
}

func validDigest(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	if _, err := strconv.ParseUint(digest[:16], 16, 64); err != nil {
		return false
	}
	for _, character := range digest {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func (handler *handler) writeConfigurationResult(response http.ResponseWriter, value any, err error, requestID string) {
	switch {
	case err == nil:
		writeJSON(response, http.StatusOK, value)
	case errors.Is(err, configservice.ErrDesiredConflict), errors.Is(err, configurationservice.ErrPlanConflict), errors.Is(err, artifact.ErrLocked), errors.Is(err, storesqlite.ErrBusy):
		writeAPIError(response, http.StatusConflict, "configuration_conflict", "configuration state changed or is busy", requestID)
	case errors.Is(err, runtimecontrol.ErrControlUnavailable), errors.Is(err, runtimecontrol.ErrControlRejected):
		writeAPIError(response, http.StatusServiceUnavailable, "runtime_unavailable", "runtime supervisor is unavailable", requestID)
	case errors.Is(err, configurationservice.ErrRecoveryRequired):
		writeAPIError(response, http.StatusConflict, "recovery_required", "interrupted operation recovery is required", requestID)
	case errors.Is(err, configservice.ErrInvalidDesired):
		writeAPIError(response, http.StatusUnprocessableEntity, "invalid_configuration", "configuration is invalid", requestID)
	default:
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "request could not be completed", requestID)
	}
}
