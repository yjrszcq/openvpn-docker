package httpapi

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	clientservice "github.com/yjrszcq/openvpn-docker/internal/client"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	runtimecontrol "github.com/yjrszcq/openvpn-docker/internal/runtime"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

type createClientRequest struct {
	Name string `json:"name"`
	IPv4 string `json:"ipv4"`
}

type renameClientRequest struct {
	Name string `json:"name"`
}

type revokeClientRequest struct {
	ReleaseIPv4 bool `json:"release_ipv4"`
}

type reissueClientRequest struct {
	IPv4 string `json:"ipv4"`
}

type ipv4Request struct {
	Mode    string  `json:"mode"`
	Address *string `json:"address"`
}

type runtimeOutcome struct {
	ClientID string                           `json:"client_id,omitempty"`
	Status   string                           `json:"status"`
	Result   *runtimecontrol.DisconnectResult `json:"result,omitempty"`
}

type mutationResponse struct {
	clientservice.MutationResult
	Runtime *runtimeOutcome `json:"runtime,omitempty"`
}

type addressMutationResponse struct {
	clientservice.AddressResult
	Runtime []runtimeOutcome `json:"runtime"`
}

func (handler *handler) routeMutation(response http.ResponseWriter, request *http.Request, requestID string) bool {
	path := request.URL.Path
	if path == "/api/v1/clients" {
		if request.Method == http.MethodGet {
			return false
		}
		if request.Method != http.MethodPost {
			return rejectMethod(response, requestID, http.MethodGet+", "+http.MethodPost)
		}
		var input createClientRequest
		if !handler.decodeMutation(response, request, &input, requestID) {
			return true
		}
		result, err := handler.resources.Mutations.Create(request.Context(), clientservice.CreateRequest{Name: input.Name, IPv4: input.IPv4})
		handler.writeMutationResult(request.Context(), response, http.StatusCreated, result, err, requestID)
		return true
	}
	if !strings.HasPrefix(path, "/api/v1/clients/") {
		return false
	}
	remainder := strings.TrimPrefix(path, "/api/v1/clients/")
	parts := strings.Split(remainder, "/")
	if len(parts) < 1 || !domain.ValidUUID(parts[0]) {
		if request.Method != http.MethodGet {
			writeAPIError(response, http.StatusBadRequest, "invalid_client_id", "client ID must be a complete UUID", requestID)
			return true
		}
		return false
	}
	id, selector := parts[0], clientservice.Selector{IDPrefix: parts[0]}
	if handler.resources.Mutations == nil && !(len(parts) == 2 && parts[1] == "disconnect") {
		return false
	}
	if len(parts) == 1 {
		switch request.Method {
		case http.MethodGet:
			return false
		case http.MethodPatch:
			var input renameClientRequest
			if !handler.decodeMutation(response, request, &input, requestID) {
				return true
			}
			result, err := handler.resources.Mutations.Rename(request.Context(), selector, input.Name)
			handler.writeMutationResult(request.Context(), response, http.StatusOK, result, err, requestID)
			return true
		case http.MethodDelete:
			if !requireEmptyMutation(response, request, requestID) {
				return true
			}
			result, err := handler.resources.Mutations.Delete(request.Context(), selector)
			handler.writeMutationResult(request.Context(), response, http.StatusOK, result, err, requestID)
			return true
		default:
			return rejectMethod(response, requestID, http.MethodGet+", "+http.MethodPatch+", "+http.MethodDelete)
		}
	}
	if len(parts) != 2 {
		return false
	}
	switch parts[1] {
	case "revoke":
		if request.Method != http.MethodPost {
			return rejectMethod(response, requestID, http.MethodPost)
		}
		var input revokeClientRequest
		if !handler.decodeMutation(response, request, &input, requestID) {
			return true
		}
		result, err := handler.resources.Mutations.Revoke(request.Context(), selector, input.ReleaseIPv4)
		handler.writeMutationResult(request.Context(), response, http.StatusOK, result, err, requestID)
		return true
	case "reissue":
		if request.Method != http.MethodPost {
			return rejectMethod(response, requestID, http.MethodPost)
		}
		var input reissueClientRequest
		if !handler.decodeMutation(response, request, &input, requestID) {
			return true
		}
		result, err := handler.resources.Mutations.Reissue(request.Context(), selector, input.IPv4)
		handler.writeMutationResult(request.Context(), response, http.StatusOK, result, err, requestID)
		return true
	case "ipv4":
		if request.Method == http.MethodPut {
			var input ipv4Request
			if !handler.decodeMutation(response, request, &input, requestID) {
				return true
			}
			selection, err := ipv4Selection(input)
			if err != nil {
				writeAPIError(response, http.StatusUnprocessableEntity, "invalid_ipv4", "IPv4 selection is invalid", requestID)
				return true
			}
			result, err := handler.resources.Mutations.AddressSet(request.Context(), selector, selection)
			handler.writeAddressResult(request.Context(), response, result, err, requestID)
			return true
		}
		if request.Method == http.MethodDelete {
			if !requireEmptyMutation(response, request, requestID) {
				return true
			}
			result, err := handler.resources.Mutations.AddressRelease(request.Context(), selector)
			handler.writeAddressResult(request.Context(), response, result, err, requestID)
			return true
		}
		return rejectMethod(response, requestID, http.MethodPut+", "+http.MethodDelete)
	case "disconnect":
		if handler.resources.Disconnect == nil {
			return false
		}
		if request.Method != http.MethodPost {
			return rejectMethod(response, requestID, http.MethodPost)
		}
		if !requireEmptyMutation(response, request, requestID) {
			return true
		}
		view, err := handler.resources.Clients.Get(request.Context(), id)
		if err != nil {
			handler.writeResult(response, nil, err, requestID)
			return true
		}
		result, err := handler.resources.Disconnect(request.Context(), id, view.Name)
		handler.writeResult(response, result, err, requestID)
		return true
	case "profile":
		if request.Method == http.MethodGet {
			return false
		}
		return rejectMethod(response, requestID, http.MethodGet)
	}
	return false
}

func rejectMethod(response http.ResponseWriter, requestID, allowed string) bool {
	response.Header().Set("Allow", allowed)
	writeAPIError(response, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed", requestID)
	return true
}

func requireEmptyMutation(response http.ResponseWriter, request *http.Request, requestID string) bool {
	if request.URL.RawQuery != "" {
		writeAPIError(response, http.StatusBadRequest, "invalid_query", "query parameters are not accepted", requestID)
		return false
	}
	if request.Body == nil {
		return true
	}
	content, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil || len(content) != 0 {
		writeAPIError(response, http.StatusBadRequest, "invalid_body", "request body is not accepted", requestID)
		return false
	}
	return true
}

func (handler *handler) decodeMutation(response http.ResponseWriter, request *http.Request, destination any, requestID string) bool {
	if handler.resources.Mutations == nil {
		writeAPIError(response, http.StatusNotFound, "not_found", "resource was not found", requestID)
		return false
	}
	if request.URL.RawQuery != "" {
		writeAPIError(response, http.StatusBadRequest, "invalid_query", "query parameters are not accepted", requestID)
		return false
	}
	if err := decodeJSON(response, request, destination); err != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "request body is invalid", requestID)
		return false
	}
	return true
}

func ipv4Selection(input ipv4Request) (string, error) {
	switch input.Mode {
	case "auto", "dynamic":
		if input.Address != nil {
			return "", errors.New("address is not allowed")
		}
		return input.Mode, nil
	case "static":
		if input.Address == nil || *input.Address == "" {
			return "", errors.New("address is required")
		}
		return *input.Address, nil
	default:
		return "", errors.New("invalid mode")
	}
}

func (handler *handler) writeMutationResult(ctx context.Context, response http.ResponseWriter, status int, result clientservice.MutationResult, err error, requestID string) {
	if err != nil {
		handler.writeClientError(response, err, requestID)
		return
	}
	out := mutationResponse{MutationResult: result}
	if result.KickRequired && handler.resources.Disconnect != nil {
		disconnected, disconnectErr := handler.resources.Disconnect(ctx, result.Client.ID, result.Client.Name)
		if disconnectErr != nil {
			out.Runtime = &runtimeOutcome{Status: "unavailable"}
		} else {
			out.Runtime = &runtimeOutcome{Status: "ok", Result: &disconnected}
		}
	}
	if status == http.StatusCreated {
		response.Header().Set("Location", "/api/v1/clients/"+result.Client.ID)
	}
	writeJSON(response, status, out)
}

func (handler *handler) writeAddressResult(ctx context.Context, response http.ResponseWriter, result clientservice.AddressResult, err error, requestID string) {
	if err != nil {
		handler.writeClientError(response, err, requestID)
		return
	}
	out := addressMutationResponse{AddressResult: result, Runtime: make([]runtimeOutcome, 0, len(result.KickRequired))}
	for _, id := range result.KickRequired {
		outcome := runtimeOutcome{ClientID: id, Status: "unavailable"}
		view, viewErr := handler.resources.Clients.Get(ctx, id)
		if viewErr == nil && handler.resources.Disconnect != nil {
			value, disconnectErr := handler.resources.Disconnect(ctx, id, view.Name)
			if disconnectErr == nil {
				outcome.Status, outcome.Result = "ok", &value
			}
		}
		out.Runtime = append(out.Runtime, outcome)
	}
	writeJSON(response, http.StatusOK, out)
}

func (handler *handler) writeClientError(response http.ResponseWriter, err error, requestID string) {
	switch {
	case errors.Is(err, clientservice.ErrNotFound):
		writeAPIError(response, http.StatusNotFound, "client_not_found", "client was not found", requestID)
	case errors.Is(err, clientservice.ErrConflict), errors.Is(err, artifact.ErrLocked), errors.Is(err, storesqlite.ErrBusy), errors.Is(err, storesqlite.ErrConstraint):
		writeAPIError(response, http.StatusConflict, "client_conflict", "client state conflicts with the request", requestID)
	case errors.Is(err, clientservice.ErrInvalidRequest), errors.Is(err, clientservice.ErrInactive):
		writeAPIError(response, http.StatusUnprocessableEntity, "invalid_client", "client request is not valid for the current state", requestID)
	case errors.Is(err, pki.ErrUnavailable):
		writeAPIError(response, http.StatusServiceUnavailable, "dependency_unavailable", "client dependency is unavailable", requestID)
	default:
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "request could not be completed", requestID)
	}
}

func (handler *handler) writeProfile(response http.ResponseWriter, request *http.Request, id, requestID string) {
	content, view, err := handler.resources.Clients.Export(request.Context(), clientservice.Selector{IDPrefix: id})
	if err != nil {
		handler.writeClientError(response, err, requestID)
		return
	}
	response.Header().Set("Content-Type", "application/x-openvpn-profile")
	response.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": view.Name + ".ovpn"}))
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(content)
}
