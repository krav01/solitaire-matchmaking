package api_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIContractParsesAndResolvesLocalReferences(t *testing.T) {
	t.Parallel()

	encoded, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI contract: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("parse OpenAPI contract: %v", err)
	}
	if document["openapi"] != "3.1.0" {
		t.Fatalf("OpenAPI version = %v", document["openapi"])
	}

	for path, methods := range map[string][]string{
		"/healthz":                {"get"},
		"/readyz":                 {"get"},
		"/v1/capabilities":        {"get"},
		"/v1/tickets":             {"post"},
		"/v1/tickets/{ticket_id}": {"get", "delete"},
		"/v1/results":             {"post"},
	} {
		pathItem := objectAt(t, document, "paths", path)
		for _, method := range methods {
			if _, exists := pathItem[method]; !exists {
				t.Errorf("OpenAPI operation %s %s is missing", method, path)
			}
		}
	}
	objectAt(t, document, "webhooks", "outboxEvent", "post")
	checkReferences(t, document, document)
}

func objectAt(t *testing.T, root map[string]any, keys ...string) map[string]any {
	t.Helper()
	current := root
	for _, key := range keys {
		value, exists := current[key]
		if !exists {
			t.Fatalf("OpenAPI object %q is missing", strings.Join(keys, "/"))
		}
		next, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("OpenAPI object %q has type %T", strings.Join(keys, "/"), value)
		}
		current = next
	}

	return current
}

func checkReferences(t *testing.T, document map[string]any, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		if reference, ok := typed["$ref"].(string); ok {
			if !strings.HasPrefix(reference, "#/") {
				t.Fatalf("unsupported non-local OpenAPI reference %q", reference)
			}
			objectAt(t, document, strings.Split(strings.TrimPrefix(reference, "#/"), "/")...)
		}
		for _, nested := range typed {
			checkReferences(t, document, nested)
		}
	case []any:
		for _, nested := range typed {
			checkReferences(t, document, nested)
		}
	}
}
