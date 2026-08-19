package httpapi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAPIContractCoversImplementedOperations(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(openAPIDocument(t), &document); err != nil {
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
	if err := json.Unmarshal(openAPIDocument(t), &document); err != nil {
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

func TestOpenAPIMutationsUseOperationSpecificSuccessExamples(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(openAPIDocument(t), &document); err != nil {
		t.Fatal(err)
	}
	paths := object(t, document["paths"], "paths")
	tests := []struct {
		method   string
		path     string
		contains string
	}{
		{"patch", "/api/v1/clients/{client_id}", `"name":"alice-notebook"`},
		{"post", "/api/v1/clients/{client_id}/reissue", `"profile_redistribution_required":true`},
		{"delete", "/api/v1/clients/{client_id}", `"status":"deleted"`},
		{"delete", "/api/v1/clients/{client_id}/ipv4", `"address":null`},
	}
	for _, test := range tests {
		operation := object(t, object(t, paths[test.path], test.path)[test.method], test.method+" "+test.path)
		response := referencedObject(t, document, object(t, operation["responses"], "responses")["200"], "success response")
		media := object(t, object(t, response["content"], "response content")["application/json"], "response media")
		example, err := json.Marshal(media["example"])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(example), test.contains) {
			t.Fatalf("%s %s example %s lacks %s", test.method, test.path, example, test.contains)
		}
	}
}

func TestOpenAPISchemasDescribeEveryFieldInBothLanguages(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(openAPIDocument(t), &document); err != nil {
		t.Fatal(err)
	}
	components := object(t, document["components"], "components")
	schemas := object(t, components["schemas"], "components.schemas")
	for name, rawSchema := range schemas {
		assertSchemaDescriptions(t, object(t, rawSchema, "schema "+name), "components.schemas."+name)
	}
}

func assertSchemaDescriptions(t *testing.T, schema map[string]any, location string) {
	t.Helper()
	if schema["$ref"] == nil {
		if description, ok := schema["description"].(string); !ok || description == "" {
			t.Fatalf("%s has no English description", location)
		}
		if description, ok := schema["x-description-zh"].(string); !ok || description == "" {
			t.Fatalf("%s has no Chinese description", location)
		}
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		for name, rawProperty := range properties {
			assertSchemaDescriptions(t, object(t, rawProperty, location+"."+name), location+"."+name)
		}
	}
	if items, ok := schema["items"].(map[string]any); ok && items["$ref"] == nil && (items["properties"] != nil || items["type"] == "array") {
		assertSchemaDescriptions(t, items, location+"[]")
	}
	for _, keyword := range []string{"oneOf", "anyOf"} {
		variants, _ := schema[keyword].([]any)
		for index, rawVariant := range variants {
			variant := object(t, rawVariant, fmt.Sprintf("%s.%s[%d]", location, keyword, index))
			if variant["$ref"] == nil && (variant["properties"] != nil || variant["type"] == "array") {
				assertSchemaDescriptions(t, variant, fmt.Sprintf("%s.%s[%d]", location, keyword, index))
			}
		}
	}
}

func TestFrontendGuidesDocumentEachOperationIndependently(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(openAPIDocument(t), &document); err != nil {
		t.Fatal(err)
	}
	paths := object(t, document["paths"], "paths")
	englishContractText, chineseContractText := localizedContractText(document)
	for _, guide := range []string{
		filepath.Join("..", "..", "docs", "cn", "v4", "api.md"),
		filepath.Join("..", "..", "docs", "en", "v4", "api.md"),
	} {
		content, err := os.ReadFile(guide)
		if err != nil {
			t.Fatalf("read %s: %v", guide, err)
		}
		markdown := string(content)
		requestParameters := "#### Request parameters"
		requestExample := "#### Request example"
		responsesHeading := "#### Responses"
		responseFields := "Response fields:"
		if strings.Contains(guide, string(filepath.Separator)+"cn"+string(filepath.Separator)) {
			requestParameters = "#### 请求参数"
			requestExample = "#### 请求示例"
			responsesHeading = "#### 返回"
			responseFields = "返回字段："
		}
		count := 0
		for path, rawPathItem := range paths {
			pathItem := object(t, rawPathItem, path)
			for _, method := range []string{"get", "post", "put", "patch", "delete"} {
				rawOperation := pathItem[method]
				if rawOperation == nil {
					continue
				}
				count++
				heading := "`" + strings.ToUpper(method) + " " + path + "`"
				if occurrences := strings.Count(markdown, heading); occurrences != 1 {
					t.Fatalf("%s contains %d headings for %s", guide, occurrences, heading)
				}
				start := strings.Index(markdown, heading)
				section := markdown[start:]
				if end := strings.Index(section, "\n### "); end >= 0 {
					section = section[:end]
				}
				for _, marker := range []string{requestParameters, requestExample, responsesHeading, responseFields} {
					if !strings.Contains(section, marker) {
						t.Fatalf("%s section %s lacks %q", guide, heading, marker)
					}
				}
				if path != "/healthz" && !strings.Contains(section, "| `Authorization` | `header` | `string` |") {
					t.Fatalf("%s section %s lacks the detailed Authorization row", guide, heading)
				}
				operation := object(t, rawOperation, method+" "+path)
				parameters := append([]any{}, pathItemParameters(pathItem)...)
				parameters = append(parameters, operationParameters(operation)...)
				for _, rawParameter := range parameters {
					parameter := referencedObject(t, document, rawParameter, method+" "+path+" parameter")
					row := "| `" + parameter["name"].(string) + "` | `" + parameter["in"].(string) + "` |"
					if !strings.Contains(section, row) {
						t.Fatalf("%s section %s lacks parameter row %q", guide, heading, row)
					}
				}
				if rawBody := operation["requestBody"]; rawBody != nil {
					body := referencedObject(t, document, rawBody, method+" "+path+" request body")
					content := object(t, body["content"], method+" "+path+" request content")
					media := object(t, content["application/json"], method+" "+path+" request media")
					schema := referencedObject(t, document, media["schema"], method+" "+path+" request schema")
					for field := range object(t, schema["properties"], method+" "+path+" request properties") {
						row := "| `" + field + "` | `body` |"
						if !strings.Contains(section, row) {
							t.Fatalf("%s section %s lacks request body row %q", guide, heading, row)
						}
					}
				}
				for status := range object(t, operation["responses"], method+" "+path+" responses") {
					if !strings.Contains(section, "##### `"+status+" ") {
						t.Fatalf("%s section %s lacks response status %s", guide, heading, status)
					}
				}
			}
		}
		if count != 22 || strings.Count(markdown, "\n### ") != 22 || strings.Count(markdown, requestParameters) != 22 || strings.Count(markdown, requestExample) != 22 || strings.Count(markdown, responsesHeading) != 22 {
			t.Fatalf("%s operation sections=%d OpenAPI operations=%d", guide, strings.Count(markdown, "\n### "), count)
		}
		if strings.Contains(markdown, "Host: vpn-admin.example.com") {
			t.Fatalf("%s still contains a synthetic Host header", guide)
		}
		if strings.Contains(markdown, "| - |\n") {
			t.Fatalf("%s contains a field without a purpose description", guide)
		}
		if strings.Contains(guide, string(filepath.Separator)+"cn"+string(filepath.Separator)) {
			for _, value := range englishContractText {
				if strings.Contains(markdown, value) {
					t.Fatalf("%s contains untranslated English contract text %q", guide, value)
				}
			}
		} else {
			for _, value := range chineseContractText {
				if strings.Contains(markdown, value) {
					t.Fatalf("%s contains Chinese contract text %q", guide, value)
				}
			}
		}
		for _, grouped := range []string{"### System", "### Clients", "### Runtime", "### Configuration", "### 请求内容", "### 通用返回模型"} {
			if strings.Contains(markdown, grouped) {
				t.Fatalf("%s still contains grouped contract heading %q", guide, grouped)
			}
		}
	}
}

func localizedContractText(value any) (english, chinese []string) {
	switch typed := value.(type) {
	case map[string]any:
		if summary, ok := typed["summary"].(string); ok && summary != "" {
			english = append(english, summary)
		}
		if description, ok := typed["description"].(string); ok && description != "" {
			english = append(english, description)
		}
		if description, ok := typed["x-description-zh"].(string); ok && description != "" {
			chinese = append(chinese, description)
		}
		for _, nested := range typed {
			nestedEnglish, nestedChinese := localizedContractText(nested)
			english = append(english, nestedEnglish...)
			chinese = append(chinese, nestedChinese...)
		}
	case []any:
		for _, nested := range typed {
			nestedEnglish, nestedChinese := localizedContractText(nested)
			english = append(english, nestedEnglish...)
			chinese = append(chinese, nestedChinese...)
		}
	}
	return english, chinese
}

func pathItemParameters(pathItem map[string]any) []any {
	parameters, _ := pathItem["parameters"].([]any)
	return parameters
}

func operationParameters(operation map[string]any) []any {
	parameters, _ := operation["parameters"].([]any)
	return parameters
}

func openAPIDocument(t *testing.T) []byte {
	t.Helper()
	content, err := documentationFiles.ReadFile("docs/openapi.json")
	if err != nil {
		t.Fatalf("read embedded OpenAPI document: %v", err)
	}
	return content
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
