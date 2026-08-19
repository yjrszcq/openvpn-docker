package httpapi

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

//go:embed docs/openapi.json
var openAPIContract []byte

func TestOpenAPIContractCoversImplementedOperations(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(openAPIContract, &document); err != nil {
		t.Fatalf("parse OpenAPI contract: %v", err)
	}
	if document["openapi"] != "3.1.0" {
		t.Fatalf("openapi=%v", document["openapi"])
	}
	want := map[string][]string{
		"/healthz":                               {"get"},
		"/api/v1/version":                        {"get"},
		"/api/v1/state":                          {"get"},
		"/api/v1/state/doctor":                   {"get"},
		"/api/v1/clients":                        {"get", "post"},
		"/api/v1/clients/{client_id}":            {"get", "patch", "delete"},
		"/api/v1/clients/{client_id}/profile":    {"get"},
		"/api/v1/clients/{client_id}/revoke":     {"post"},
		"/api/v1/clients/{client_id}/reissue":    {"post"},
		"/api/v1/clients/{client_id}/ipv4":       {"put", "delete"},
		"/api/v1/clients/{client_id}/disconnect": {"post"},
		"/api/v1/runtime":                        {"get"},
		"/api/v1/runtime/events":                 {"get"},
		"/api/v1/config/applied":                 {"get"},
		"/api/v1/config/desired":                 {"get", "put"},
		"/api/v1/config/plan":                    {"get"},
		"/api/v1/config/apply":                   {"post"},
	}
	paths := object(t, document["paths"], "paths")
	if len(paths) != len(want) {
		t.Fatalf("paths=%d want=%d", len(paths), len(want))
	}
	operationIDs := map[string]struct{}{}
	operationCount := 0
	for path, methods := range want {
		pathItem := object(t, paths[path], "path "+path)
		for _, method := range methods {
			operation := object(t, pathItem[method], method+" "+path)
			operationCount++
			operationID, ok := operation["operationId"].(string)
			if !ok || operationID == "" {
				t.Fatalf("%s %s has no operationId", method, path)
			}
			if _, exists := operationIDs[operationID]; exists {
				t.Fatalf("duplicate operationId %q", operationID)
			}
			operationIDs[operationID] = struct{}{}
			if summary, ok := operation["summary"].(string); !ok || summary == "" {
				t.Fatalf("%s %s has no summary", method, path)
			}
			responses := object(t, operation["responses"], method+" "+path+" responses")
			success := responses[successStatus(method, path)]
			if success == nil {
				t.Fatalf("%s %s has no success response", method, path)
			}
			response := referencedObject(t, document, success, method+" "+path+" success response")
			if len(object(t, response["content"], method+" "+path+" success content")) == 0 {
				t.Fatalf("%s %s success response has no content", method, path)
			}
			headers := object(t, response["headers"], method+" "+path+" success headers")
			if headers["X-Request-ID"] == nil {
				t.Fatalf("%s %s success response has no X-Request-ID header", method, path)
			}
			if path != "/healthz" && responses["401"] == nil {
				t.Fatalf("%s %s has no 401 response", method, path)
			}
		}
	}
	if operationCount != 22 {
		t.Fatalf("operations=%d want=22", operationCount)
	}
	resolveLocalReferences(t, document, document, "#")
}

func TestOpenAPIMutationsDocumentRequestBodies(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(openAPIContract, &document); err != nil {
		t.Fatal(err)
	}
	paths := object(t, document["paths"], "paths")
	for _, operation := range []string{
		"post /api/v1/clients",
		"patch /api/v1/clients/{client_id}",
		"post /api/v1/clients/{client_id}/revoke",
		"post /api/v1/clients/{client_id}/reissue",
		"put /api/v1/clients/{client_id}/ipv4",
		"put /api/v1/config/desired",
		"post /api/v1/config/apply",
	} {
		parts := strings.SplitN(operation, " ", 2)
		value := object(t, object(t, paths[parts[1]], parts[1])[parts[0]], operation)
		body := object(t, value["requestBody"], operation+" requestBody")
		if body["required"] != true {
			t.Fatalf("%s request body is not required", operation)
		}
		media := object(t, object(t, body["content"], operation+" content")["application/json"], operation+" application/json")
		if media["schema"] == nil || media["example"] == nil && media["examples"] == nil {
			t.Fatalf("%s lacks request schema or example", operation)
		}
	}
}

func object(t *testing.T, value any, label string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want object", label, value)
	}
	return result
}

func successStatus(method, path string) string {
	if method == "post" && path == "/api/v1/clients" {
		return "201"
	}
	return "200"
}

func referencedObject(t *testing.T, root map[string]any, value any, label string) map[string]any {
	t.Helper()
	result := object(t, value, label)
	reference, ok := result["$ref"].(string)
	if !ok {
		return result
	}
	current := any(root)
	for _, segment := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
		current = object(t, current, reference)[segment]
		if current == nil {
			t.Fatalf("unresolved reference in %s: %q", label, reference)
		}
	}
	return object(t, current, label)
}

func resolveLocalReferences(t *testing.T, root map[string]any, value any, location string) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		if reference, ok := typed["$ref"].(string); ok {
			if !strings.HasPrefix(reference, "#/") {
				t.Fatalf("external reference at %s: %q", location, reference)
			}
			current := any(root)
			for _, segment := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
				current = object(t, current, reference)[segment]
				if current == nil {
					t.Fatalf("unresolved reference at %s: %q", location, reference)
				}
			}
		}
		for key, nested := range typed {
			resolveLocalReferences(t, root, nested, location+"/"+key)
		}
	case []any:
		for index, nested := range typed {
			resolveLocalReferences(t, root, nested, fmt.Sprintf("%s/%d", location, index))
		}
	}
}
