package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONStrictness(t *testing.T) {
	type input struct {
		Name string `json:"name"`
	}
	tests := []struct {
		name        string
		contentType string
		body        string
		wantError   bool
	}{
		{"valid", "application/json", `{"name":"laptop"}`, false},
		{"utf8", "application/json; charset=UTF-8", `{"name":"laptop"}`, false},
		{"missing content type", "", `{"name":"laptop"}`, true},
		{"unknown parameter", "application/json; profile=x", `{"name":"laptop"}`, true},
		{"unknown field", "application/json", `{"name":"laptop","extra":true}`, true},
		{"duplicate field", "application/json", `{"name":"a","name":"b"}`, true},
		{"nested duplicate", "application/json", `{"name":"a","extra":{"x":1,"x":2}}`, true},
		{"null", "application/json", `{"name":null}`, true},
		{"trailing document", "application/json", `{"name":"a"}{"name":"b"}`, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/clients", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			var value input
			err := decodeJSON(response, request, &value)
			if (err != nil) != test.wantError {
				t.Fatalf("decode error=%v value=%+v", err, value)
			}
		})
	}
}

func TestDecodeJSONLimitsBody(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/clients", strings.NewReader(`{"name":"`+strings.Repeat("x", maxRequestBodyBytes)+`"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	var value struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(response, request, &value); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("oversized body error=%v", err)
	}
}
