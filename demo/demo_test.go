package demo_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/SomethingCreativeStudios/tlon/catalog"
	"github.com/SomethingCreativeStudios/tlon/demo"
	recorddoc "github.com/SomethingCreativeStudios/tlon/internal/record"
	"github.com/SomethingCreativeStudios/tlon/internal/recordschema"
)

func TestBundleAndRecordsValidate(t *testing.T) {
	bundle, err := demo.Bundle("playground")
	if err != nil {
		t.Fatal(err)
	}
	bundle, id, err := catalog.Normalize(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if id != "playground" {
		t.Fatalf("catalog id = %q", id)
	}
	records, err := demo.Records(140, demo.DefaultSeed)
	if err != nil {
		t.Fatal(err)
	}
	coverage := struct {
		nullGeometry, antimeridian, noTime, openStart, openEnd bool
		types                                                  map[string]bool
	}{types: map[string]bool{}}
	for _, record := range records {
		if _, err := recorddoc.Decode(record.Document); err != nil {
			t.Fatalf("%s does not decode: %v", record.ID, err)
		}
		if err := recordschema.ValidateBundle(bundle, record.Document); err != nil {
			t.Fatalf("%s does not match the demo bundle: %v", record.ID, err)
		}
		var document map[string]any
		if err := json.Unmarshal(record.Document, &document); err != nil {
			t.Fatal(err)
		}
		coverage.nullGeometry = coverage.nullGeometry || document["geometry"] == nil
		if geometry, ok := document["geometry"].(map[string]any); ok {
			coverage.antimeridian = coverage.antimeridian || geometry["type"] == "MultiPolygon"
		}
		value, hasTime := document["time"]
		coverage.noTime = coverage.noTime || !hasTime
		if temporal, ok := value.(map[string]any); ok {
			if interval, ok := temporal["interval"].([]any); ok && len(interval) == 2 {
				coverage.openStart = coverage.openStart || interval[0] == ".."
				coverage.openEnd = coverage.openEnd || interval[1] == ".."
			}
		}
		properties := document["properties"].(map[string]any)
		coverage.types[properties["type"].(string)] = true
	}
	if !coverage.nullGeometry || !coverage.antimeridian || !coverage.noTime || !coverage.openStart || !coverage.openEnd {
		t.Fatalf("missing edge-case coverage: %+v", coverage)
	}
	for _, kind := range []string{"dataset", "service", "model", "application", "collection"} {
		if !coverage.types[kind] {
			t.Errorf("record type %q was not generated", kind)
		}
	}
}

func TestRecordsAreDeterministic(t *testing.T) {
	first, err := demo.Records(12, 84)
	if err != nil {
		t.Fatal(err)
	}
	second, err := demo.Records(12, 84)
	if err != nil {
		t.Fatal(err)
	}
	different, err := demo.Records(12, 85)
	if err != nil {
		t.Fatal(err)
	}
	for index := range first {
		if first[index].ID != second[index].ID || !bytes.Equal(first[index].Document, second[index].Document) {
			t.Fatalf("record %d is not deterministic", index)
		}
	}
	if bytes.Equal(first[0].Document, different[0].Document) {
		t.Error("changing the seed did not change generated content")
	}
}

func TestRecordsRejectInvalidCounts(t *testing.T) {
	for _, count := range []int{0, -1, demo.MaximumCount + 1} {
		if _, err := demo.Records(count, demo.DefaultSeed); err == nil {
			t.Errorf("Records(%d) succeeded", count)
		}
	}
}
