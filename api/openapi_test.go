package api

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestOpenAPIContract(t *testing.T) {
	spec, err := GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.Validate(t.Context()); err != nil {
		t.Fatalf("invalid embedded OpenAPI: %v", err)
	}
	required := []string{"/", "/api", "/conformance", "/collections", "/collections/{catalogId}", "/collections/{catalogId}/items", "/collections/{catalogId}/items/{recordId}", "/collections/{catalogId}/queryables", "/collections/{catalogId}/sortables", "/collections/{catalogId}/facets", "/collections/{catalogId}/schema"}
	for _, path := range required {
		if spec.Paths.Find(path) == nil {
			t.Errorf("missing path %s", path)
		}
	}
	items := spec.Paths.Find("/collections/{catalogId}/items")
	if items.Post == nil {
		t.Error("record POST is absent")
	}
	item := spec.Paths.Find("/collections/{catalogId}/items/{recordId}")
	if item.Put == nil || item.Patch == nil || item.Delete == nil {
		t.Error("record mutation operations are incomplete")
	}
	for name, operation := range map[string]*openapi3.Operation{"POST": items.Post, "PUT": item.Put, "PATCH": item.Patch, "DELETE": item.Delete} {
		if operation == nil || operation.Security == nil || len(*operation.Security) != 1 {
			t.Errorf("%s does not advertise bearer security", name)
		}
	}
	facets := spec.Paths.Find("/collections/{catalogId}/facets").Get.Responses.Value("200")
	if facets == nil || facets.Value.Content.Get("application/facets+json") == nil {
		t.Error("facets response must use application/facets+json")
	}
	limit := spec.Components.Parameters["Limit"].Value.Schema.Value
	if limit.Min == nil || *limit.Min != 0 {
		t.Errorf("facet-capable limit minimum = %v, want 0", limit.Min)
	}
	if len(spec.Security) > 0 {
		t.Error("reads must not have global security")
	}
	schema := spec.Paths.Find("/collections/{catalogId}/schema").Get
	if schema.Parameters.GetByInAndName("query", "type") == nil {
		t.Error("record mutation schema type parameter is absent")
	}
}
