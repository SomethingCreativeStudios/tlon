package record

import (
	"encoding/json"
	"testing"
	"time"
)

func TestManagedFieldsAndLinks(t *testing.T) {
	raw := json.RawMessage(`{"id":"submitted","type":"Feature","geometry":null,"properties":{"type":"dataset","created":"2000-01-01T00:00:00Z","updated":"2000-01-01T00:00:00Z"},"links":[{"rel":"self","href":"https://spoofed"},{"rel":"enclosure","href":"https://data.example/file"}],"conformsTo":["https://example.test/profile"]}`)
	doc, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	ForCreate(doc)
	stored, err := Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := created.Add(time.Minute)
	output, err := Materialize(stored, "https://records.example/base/", "catalog a", "record/1", created, updated)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != "record/1" {
		t.Errorf("id = %v", got["id"])
	}
	properties := got["properties"].(map[string]any)
	if properties["created"] != created.Format(time.RFC3339Nano) || properties["updated"] != updated.Format(time.RFC3339Nano) {
		t.Errorf("managed timestamps = %#v", properties)
	}
	links := got["links"].([]any)
	if len(links) != 4 {
		t.Fatalf("links = %#v", links)
	}
	if links[0].(map[string]any)["rel"] != "enclosure" {
		t.Errorf("user link was not preserved: %#v", links)
	}
	if links[1].(map[string]any)["href"] != "https://records.example/base/collections/catalog%20a/items/record%2F1" {
		t.Errorf("self link = %v", links[1])
	}
	conformance := got["conformsTo"].([]any)
	if len(conformance) != 2 {
		t.Errorf("conformsTo = %#v", conformance)
	}
}

func TestPutIDRules(t *testing.T) {
	matching, _ := Decode(json.RawMessage(`{"id":"a","type":"Feature","geometry":null,"properties":{"type":"dataset"}}`))
	if err := ForPut(matching, "a"); err != nil {
		t.Fatal(err)
	}
	if _, exists := matching["id"]; exists {
		t.Error("managed id was retained in storage document")
	}
	different, _ := Decode(json.RawMessage(`{"id":"b","type":"Feature","geometry":null,"properties":{"type":"dataset"}}`))
	if err := ForPut(different, "a"); err == nil {
		t.Fatal("different body id accepted")
	}
}

func TestSanitizeMergePatch(t *testing.T) {
	clean, err := SanitizePatch(json.RawMessage(`{"id":"evil","links":[{"rel":"self","href":"bad"},{"rel":"license","href":"ok"}],"properties":{"created":null,"updated":"bad","title":null}}`))
	if err != nil {
		t.Fatal(err)
	}
	var patch map[string]any
	_ = json.Unmarshal(clean, &patch)
	if _, ok := patch["id"]; ok {
		t.Error("id survived")
	}
	properties := patch["properties"].(map[string]any)
	if _, ok := properties["created"]; ok {
		t.Error("created survived")
	}
	links := patch["links"].([]any)
	if len(links) != 1 || links[0].(map[string]any)["rel"] != "license" {
		t.Errorf("links = %#v", links)
	}
}

func TestDatetimeAndOpenInterval(t *testing.T) {
	start, end, err := ParseDatetime("../2026-01-31")
	if err != nil {
		t.Fatal(err)
	}
	if start != nil || end == nil {
		t.Fatalf("bounds = %v %v", start, end)
	}
	if _, _, err := ParseDatetime("2026-02-01/2026-01-01"); err == nil {
		t.Fatal("reverse interval accepted")
	}
}

func TestRejectInvalidGeometryAndTime(t *testing.T) {
	for _, raw := range []string{
		`{"type":"Feature","geometry":{"type":"Point","coordinates":[181,0]},"properties":{"type":"dataset"}}`,
		`{"type":"Feature","geometry":{"type":"LineString","coordinates":[[0,0]]},"properties":{"type":"dataset"}}`,
		`{"type":"Feature","geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,1]]]},"properties":{"type":"dataset"}}`,
		`{"type":"Feature","geometry":{"type":"Unknown","coordinates":[]},"properties":{"type":"dataset"}}`,
		`{"type":"Feature","geometry":null,"time":{},"properties":{"type":"dataset"}}`,
		`{"type":"Feature","geometry":null,"time":{"date":"2026-01-01","timestamp":"2026-01-01T00:00:00Z"},"properties":{"type":"dataset"}}`,
	} {
		if _, err := Decode(json.RawMessage(raw)); err == nil {
			t.Errorf("invalid record was accepted: %s", raw)
		}
	}
}

func TestDeriveCatalogUsesTwoDimensionalBoundsFromSixDimensionalBbox(t *testing.T) {
	derived, err := DeriveCatalog(json.RawMessage(`{"extent":{"spatial":{"bbox":[[-10,-20,-100,30,40,100]]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if derived.Geometry == nil || *derived.Geometry != `{"coordinates":[[[-10,-20],[30,-20],[30,40],[-10,40],[-10,-20]]],"type":"Polygon"}` {
		t.Fatalf("geometry = %v", derived.Geometry)
	}
}
