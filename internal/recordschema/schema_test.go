package recordschema

import (
	"encoding/json"
	"testing"

	"github.com/SomethingCreativeStudios/tlon/store"
)

func TestValidate(t *testing.T) {
	schema := json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["properties"],"properties":{"properties":{"type":"object","required":["title"],"properties":{"title":{"type":"string","minLength":1}}}}}`)
	if err := Validate(schema, json.RawMessage(`{"properties":{"title":"valid"}}`)); err != nil {
		t.Fatal(err)
	}
	if err := Validate(schema, json.RawMessage(`{"properties":{}}`)); err == nil {
		t.Fatal("missing required title was accepted")
	}
}

func TestValidateConfiguredTypes(t *testing.T) {
	bundle := store.CatalogBundle{
		Schema: json.RawMessage(`{"type":"object"}`),
		Queryables: map[string]store.Queryable{
			"score":    {Type: "number", Path: "/properties/score"},
			"keywords": {Type: "array", Items: &store.Queryable{Type: "string"}, Path: "/properties/keywords"},
		},
		Sortables: map[string]store.Sortable{"when": {Type: "string", Format: "date-time", Path: "/properties/when"}},
	}
	valid := json.RawMessage(`{"properties":{"score":2.5,"keywords":["a","b"],"when":"2026-01-01T00:00:00Z"}}`)
	if err := ValidateBundle(bundle, valid); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []json.RawMessage{
		json.RawMessage(`{"properties":{"score":"high"}}`),
		json.RawMessage(`{"properties":{"keywords":["a",2]}}`),
		json.RawMessage(`{"properties":{"when":"yesterday"}}`),
	} {
		if err := ValidateBundle(bundle, invalid); err == nil {
			t.Errorf("invalid configured value accepted: %s", invalid)
		}
	}
}

func TestCompileRejectsInvalidAndRemoteReferences(t *testing.T) {
	if _, err := Compile(json.RawMessage(`{"type":7}`)); err == nil {
		t.Fatal("invalid schema was accepted")
	}
	if _, err := Compile(json.RawMessage(`{"$ref":"https://example.com/schema.json"}`)); err == nil {
		t.Fatal("remote reference was accepted")
	}
}
