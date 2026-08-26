package postgres

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SomethingCreativeStudios/tlon/catalog"
	storeapi "github.com/SomethingCreativeStudios/tlon/store"
)

func TestDesiredCatalogIndexes(t *testing.T) {
	bundle, _, err := catalog.Normalize(storeapi.CatalogBundle{
		Catalog: json.RawMessage(`{"id":"indexed","type":"Collection","itemType":"record"}`),
		Storage: storeapi.CatalogStorage{
			Class: storeapi.StorageTemporal,
			Indexes: []storeapi.CatalogIndex{{
				Name: "organization-score",
				Keys: []storeapi.IndexKey{{Property: "organization"}, {Property: "score", Direction: "desc"}},
			}},
		},
		Queryables: map[string]storeapi.Queryable{
			"organization": {Type: "string", Path: "/properties/organization"},
			"score":        {Type: "number", Path: "/properties/score"},
			"keywords":     {Type: "array", Items: &storeapi.Queryable{Type: "string"}, Path: "/properties/keywords"},
		},
		Facets: map[string]storeapi.FacetDefinition{
			"organizations": {Type: storeapi.FacetTerm, Property: "organization"},
			"scores":        {Type: storeapi.FacetHistogram, Property: "score"},
			"keywords":      {Type: storeapi.FacetTerm, Property: "keywords"},
			"updated":       {Type: storeapi.FacetHistogram, Property: "updated", BucketType: storeapi.BucketFixedInterval, Interval: json.RawMessage(`"P1D"`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	specs, err := desiredCatalogIndexes(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 3 {
		t.Fatalf("managed index count = %d, want 3", len(specs))
	}
	automatic := 0
	for _, spec := range specs {
		if spec.Automatic {
			automatic++
		}
		if strings.Contains(spec.CreateSQL, "r.") {
			t.Fatalf("index SQL contains a query alias: %s", spec.CreateSQL)
		}
		if !strings.Contains(spec.CreateSQL, `ON "records_temporal" (catalog_id`) {
			t.Fatalf("index SQL is not catalog scoped: %s", spec.CreateSQL)
		}
	}
	if automatic != 2 {
		t.Fatalf("automatic index count = %d, want 2", automatic)
	}
}

func TestDisableAutomaticFacetIndexes(t *testing.T) {
	disabled := false
	bundle, _, err := catalog.Normalize(storeapi.CatalogBundle{
		Catalog: json.RawMessage(`{"id":"unindexed","type":"Collection","itemType":"record"}`),
		Storage: storeapi.CatalogStorage{Class: storeapi.StorageTransactional, AutoFacetIndexes: &disabled},
		Queryables: map[string]storeapi.Queryable{
			"score": {Type: "number", Path: "/properties/score"},
		},
		Facets: map[string]storeapi.FacetDefinition{
			"scores": {Type: storeapi.FacetHistogram, Property: "score"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	specs, err := desiredCatalogIndexes(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 0 {
		t.Fatalf("automatic indexes were not disabled: %#v", specs)
	}
}
