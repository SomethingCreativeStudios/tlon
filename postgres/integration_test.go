//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/SomethingCreativeStudios/tlon/application"
	"github.com/SomethingCreativeStudios/tlon/auth"
	"github.com/SomethingCreativeStudios/tlon/demo"
	"github.com/SomethingCreativeStudios/tlon/postgres"
	"github.com/SomethingCreativeStudios/tlon/store"
)

func TestPostGISStore(t *testing.T) {
	databaseURL := os.Getenv("TLON_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TLON_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	database, err := postgres.Open(ctx, databaseURL, postgres.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var postgresVersion, postgisVersion string
	postgresVersion, postgisVersion, err = database.DatabaseVersions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(postgresVersion, "18.") || !strings.HasPrefix(postgisVersion, "3.6") {
		t.Fatalf("unexpected database versions: PostgreSQL %s, PostGIS %s", postgresVersion, postgisVersion)
	}
	catalogID := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	bundle := integrationBundle(catalogID)
	if _, err := database.ApplyCatalog(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.DeleteCatalog(context.Background(), catalogID, true) })
	records := map[string]json.RawMessage{
		"a": record(`{"type":"Feature","geometry":{"type":"Point","coordinates":[175,0]},"time":{"interval":["2024-01-01","2024-12-31"]},"properties":{"type":"dataset","title":"Ocean Data Alpha","description":"Blue water observations","keywords":["ocean","blue"],"organization":"Alpha","score":10},"externalIds":[{"scheme":"doi","value":"abc"}]}`),
		"b": record(`{"type":"Feature","geometry":{"type":"Point","coordinates":[-175,0]},"time":{"timestamp":"2025-06-01T00:00:00Z"},"properties":{"type":"service","title":"Ocean Service Beta","keywords":["ocean","service"],"organization":"Beta","score":20}}`),
		"c": record(`{"type":"Feature","geometry":null,"properties":{"type":"dataset","title":"Unlocated Catalog","keywords":["catalog"],"organization":"Alpha"}}`),
		"d": record(`{"type":"Feature","geometry":{"type":"Point","coordinates":[50,20]},"time":{"interval":["..","2023-12-31"]},"properties":{"type":"dataset","title":"Land Data Delta","keywords":["land"],"organization":"Delta","score":30}}`),
	}
	for id, document := range records {
		if _, err := database.CreateRecord(ctx, catalogID, id, document); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	t.Run("spatial temporal text ids type and external id", func(t *testing.T) {
		result, err := database.SearchRecords(ctx, catalogID, store.Search{Bbox: []float64{170, -10, -170, 10}, Limit: 10, Facets: stringPointer("")})
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, result, "a", "b", "c")
		result, err = database.SearchRecords(ctx, catalogID, store.Search{Bbox: []float64{170, -10, -100, -170, 10, 100}, Limit: 10, Facets: stringPointer("")})
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, result, "a", "b", "c")
		result, err = database.SearchRecords(ctx, catalogID, store.Search{Datetime: "2024-06-01", Limit: 10, Facets: stringPointer("")})
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, result, "a", "c")
		result, err = database.SearchRecords(ctx, catalogID, store.Search{Datetime: "2022-06-01", Limit: 10, Facets: stringPointer("")})
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, result, "c", "d")
		result, err = database.SearchRecords(ctx, catalogID, store.Search{Q: []string{"Ocean Data"}, Types: []string{"dataset"}, Bbox: []float64{170, -10, -170, 10}, Datetime: "2024-06-01", Limit: 10, Facets: stringPointer("")})
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, result, "a")
		result, err = database.SearchRecords(ctx, catalogID, store.Search{IDs: []string{"a", "b"}, Types: []string{"service"}, Limit: 10, Facets: stringPointer("")})
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, result, "b")
		result, err = database.SearchRecords(ctx, catalogID, store.Search{ExternalIDs: []string{"doi:abc"}, Limit: 10, Facets: stringPointer("")})
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, result, "a")
		result, err = database.SearchRecords(ctx, catalogID, store.Search{Filter: "score >= 10 AND type = 'dataset'", Limit: 10, Facets: stringPointer("")})
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, result, "a", "d")
	})

	t.Run("facets before paging and facet-only", func(t *testing.T) {
		result, err := database.SearchRecords(ctx, catalogID, store.Search{Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if result.NumberMatched != 4 || len(result.Records) != 1 {
			t.Fatalf("counts = %d/%d", result.NumberMatched, len(result.Records))
		}
		keywords := result.Facets["keywords"]
		if len(keywords.Buckets) != 5 {
			t.Errorf("keyword buckets = %#v", keywords.Buckets)
		}
		types := result.Facets["resourceType"]
		if bucketCount(types, "dataset") != 3 || bucketCount(types, "service") != 1 {
			t.Errorf("filter facet = %#v", types.Buckets)
		}
		if len(result.Facets["score"].Buckets) == 0 {
			t.Error("numeric histogram is empty")
		}
		empty := ""
		result, err = database.SearchRecords(ctx, catalogID, store.Search{Limit: 0, Facets: &empty})
		if err != nil {
			t.Fatal(err)
		}
		if result.NumberMatched != 4 || len(result.Records) != 0 || result.Facets != nil {
			t.Fatalf("facet suppression = %#v", result)
		}
		selected := "keywords:2:count_desc,organization:10:value_asc"
		result, err = database.SearchRecords(ctx, catalogID, store.Search{Limit: 0, Facets: &selected})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Facets) != 2 || result.Facets["keywords"].More == nil || !*result.Facets["keywords"].More {
			t.Errorf("selected facets = %#v", result.Facets)
		}
		organizations := result.Facets["organization"]
		if len(organizations.Buckets) != 1 || bucketCount(organizations, "Alpha") != 2 {
			t.Errorf("minOccurs facet = %#v", organizations)
		}
		selected = "updated:2:value_asc"
		result, err = database.SearchRecords(ctx, catalogID, store.Search{Limit: 0, Facets: &selected})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Facets) != 1 || result.Facets["updated"].Type != store.FacetHistogram {
			t.Errorf("temporal facet = %#v", result.Facets)
		}
	})

	t.Run("stable forward and backward cursors", func(t *testing.T) {
		sortOrder := []store.SortField{{Property: "score", Direction: "asc"}, {Property: "id", Direction: "asc"}}
		first, err := database.SearchRecords(ctx, catalogID, store.Search{Limit: 2, Sort: sortOrder, Facets: stringPointer("")})
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, first, "a", "b")
		if !first.HasNext || first.HasPrev {
			t.Fatalf("first links next=%v prev=%v", first.HasNext, first.HasPrev)
		}
		second, err := database.SearchRecords(ctx, catalogID, store.Search{Limit: 2, Sort: sortOrder, Facets: stringPointer(""), Position: &store.CursorPosition{Direction: store.CursorNext, Values: first.Records[1].SortValues}})
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, second, "d", "c")
		if !second.HasPrev {
			t.Error("second page has no previous page")
		}
		back, err := database.SearchRecords(ctx, catalogID, store.Search{Limit: 2, Sort: sortOrder, Facets: stringPointer(""), Position: &store.CursorPosition{Direction: store.CursorPrev, Values: second.Records[0].SortValues}})
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, back, "a", "b")

		for _, property := range []string{"id", "title", "type", "created", "updated", "score"} {
			t.Run(property, func(t *testing.T) {
				seen := map[string]bool{}
				var position *store.CursorPosition
				for page := 0; page < 10; page++ {
					result, err := database.SearchRecords(ctx, catalogID, store.Search{Limit: 1, Sort: []store.SortField{{Property: property, Direction: "asc"}}, Position: position, Facets: stringPointer("")})
					if err != nil {
						t.Fatal(err)
					}
					if len(result.Records) != 1 {
						t.Fatalf("page %d returned %d records", page, len(result.Records))
					}
					item := result.Records[0]
					if seen[item.ID] {
						t.Fatalf("record %q repeated", item.ID)
					}
					seen[item.ID] = true
					if !result.HasNext {
						break
					}
					position = &store.CursorPosition{Direction: store.CursorNext, Values: item.SortValues}
				}
				if len(seen) != len(records) {
					t.Fatalf("traversed %d of %d records: %v", len(seen), len(records), seen)
				}
			})
		}
	})

	t.Run("replace patch conditional writes and delete", func(t *testing.T) {
		current, err := database.GetRecord(ctx, catalogID, "a")
		if err != nil {
			t.Fatal(err)
		}
		wrong := current.Version + 100
		if _, err := database.PutRecord(ctx, catalogID, "a", records["a"], &wrong); !errors.Is(err, store.ErrPrecondition) {
			t.Fatalf("wrong If-Match = %v", err)
		}
		replacement := record(`{"type":"Feature","geometry":null,"properties":{"type":"dataset","title":"Replaced","description":"remove me","score":11}}`)
		put, err := database.PutRecord(ctx, catalogID, "a", replacement, &current.Version)
		if err != nil {
			t.Fatal(err)
		}
		if put.Created || !put.Record.CreatedAt.Equal(current.CreatedAt) || put.Record.Version != current.Version+1 || !put.Record.UpdatedAt.After(current.UpdatedAt) {
			t.Fatalf("replace = %#v", put)
		}
		patched, err := database.PatchRecord(ctx, catalogID, "a", json.RawMessage(`{"properties":{"description":null,"organization":"patched"}}`), &put.Record.Version)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		_ = json.Unmarshal(patched.Document, &doc)
		properties := doc["properties"].(map[string]any)
		if _, exists := properties["description"]; exists || properties["organization"] != "patched" || properties["title"] != "Replaced" {
			t.Errorf("patched document = %#v", doc)
		}
		var successes, preconditions int
		var lock sync.Mutex
		var wait sync.WaitGroup
		for i := 0; i < 2; i++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				_, err := database.PutRecord(context.Background(), catalogID, "a", replacement, &patched.Version)
				lock.Lock()
				defer lock.Unlock()
				if err == nil {
					successes++
				} else if errors.Is(err, store.ErrPrecondition) {
					preconditions++
				} else {
					t.Errorf("concurrent put: %v", err)
				}
			}()
		}
		wait.Wait()
		if successes != 1 || preconditions != 1 {
			t.Errorf("conditional results success=%d precondition=%d", successes, preconditions)
		}
		created, err := database.PutRecord(ctx, catalogID, "put-created", replacement, nil)
		if err != nil || !created.Created {
			t.Fatalf("PUT create = %#v %v", created, err)
		}
		if err := database.DeleteRecord(ctx, catalogID, "put-created", &wrong); !errors.Is(err, store.ErrPrecondition) {
			t.Fatalf("delete mismatch = %v", err)
		}
		version := created.Record.Version
		if err := database.DeleteRecord(ctx, catalogID, "put-created", &version); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("UUIDv7 POST flow and nonempty catalog guard", func(t *testing.T) {
		app := application.New(database, auth.External{}, "https://records.example")
		created, err := app.Create(ctx, catalogID, record(`{"id":"discard-me","type":"Feature","geometry":null,"properties":{"type":"dataset","title":"Generated"}}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := uuid.Parse(created.ID)
		if err != nil || parsed.Version() != 7 {
			t.Fatalf("generated id = %q %v", created.ID, err)
		}
		if _, err := app.Create(ctx, catalogID, record(`{"type":"Feature","geometry":null,"properties":{"type":"dataset"}}`), nil); err == nil {
			t.Fatal("record that violates the catalog schema was accepted")
		}
		if _, err := app.Patch(ctx, catalogID, created.ID, json.RawMessage(`{"properties":{"title":null}}`), nil, nil); err == nil {
			t.Fatal("merge patch that violates the catalog schema was accepted")
		}
		unchanged, err := app.Record(ctx, catalogID, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		var unchangedDocument map[string]any
		if err := json.Unmarshal(unchanged.Document, &unchangedDocument); err != nil {
			t.Fatal(err)
		}
		if unchangedDocument["properties"].(map[string]any)["title"] != "Generated" {
			t.Fatalf("rejected patch changed the record: %s", unchanged.Document)
		}
		if err := database.DeleteCatalog(ctx, catalogID, false); !errors.Is(err, store.ErrCatalogNotEmpty) {
			t.Fatalf("nonempty catalog delete = %v", err)
		}
		if err := database.DeleteRecord(ctx, catalogID, created.ID, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("catalog reconfiguration validates existing records", func(t *testing.T) {
		incompatible := integrationBundle(catalogID)
		incompatible.Schema = json.RawMessage(`{"type":"object","required":["neverPresent"]}`)
		if _, err := database.ApplyCatalog(ctx, incompatible); err == nil {
			t.Fatal("incompatible catalog schema was applied over existing records")
		}
		if _, err := database.ApplyCatalog(ctx, integrationBundle(catalogID)); err != nil {
			t.Fatalf("reapply compatible catalog: %v", err)
		}
	})
}

func TestDemoSeed(t *testing.T) {
	databaseURL := os.Getenv("TLON_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TLON_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	database, err := postgres.Open(ctx, databaseURL, postgres.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	catalogID := fmt.Sprintf("demo-integration-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = database.DeleteCatalog(context.Background(), catalogID, true) })

	first, err := demo.Seed(ctx, database, demo.SeedOptions{CatalogID: catalogID, Count: 45, Seed: 71, Reset: true, PublicURL: "https://records.example"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Created != 45 || first.Replaced != 0 {
		t.Fatalf("first seed = %#v", first)
	}
	result, err := database.SearchRecords(ctx, catalogID, store.Search{Limit: 0})
	if err != nil {
		t.Fatal(err)
	}
	if result.NumberMatched != 45 || len(result.Records) != 0 {
		t.Fatalf("demo counts = %d/%d", result.NumberMatched, len(result.Records))
	}
	for name, kind := range map[string]string{"organizations": store.FacetTerm, "quality": store.FacetHistogram, "observedQuarter": store.FacetHistogram, "resourceGroups": store.FacetFilter} {
		facet, ok := result.Facets[name]
		if !ok || facet.Type != kind || len(facet.Buckets) == 0 {
			t.Errorf("facet %q = %#v", name, facet)
		}
	}

	second, err := demo.Seed(ctx, database, demo.SeedOptions{CatalogID: catalogID, Count: 45, Seed: 71, PublicURL: "https://records.example"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Created != 0 || second.Replaced != 45 {
		t.Fatalf("second seed = %#v", second)
	}
	reset, err := demo.Seed(ctx, database, demo.SeedOptions{CatalogID: catalogID, Count: 12, Seed: 72, Reset: true, PublicURL: "https://records.example"})
	if err != nil {
		t.Fatal(err)
	}
	if reset.Created != 12 || reset.Replaced != 0 {
		t.Fatalf("reset seed = %#v", reset)
	}
	result, err = database.SearchRecords(ctx, catalogID, store.Search{Limit: 0, Facets: stringPointer("")})
	if err != nil {
		t.Fatal(err)
	}
	if result.NumberMatched != 12 {
		t.Fatalf("records after reset = %d", result.NumberMatched)
	}
}

func integrationBundle(id string) store.CatalogBundle {
	return store.CatalogBundle{
		Catalog: json.RawMessage(fmt.Sprintf(`{"id":%q,"type":"Collection","itemType":"record","title":"Integration"}`, id)),
		Queryables: map[string]store.Queryable{
			"score":        {Type: "number", Path: "/properties/score"},
			"keywords":     {Type: "array", Items: &store.Queryable{Type: "string"}, Path: "/properties/keywords"},
			"organization": {Type: "string", Path: "/properties/organization"},
		},
		Sortables:        map[string]store.Sortable{"score": {Type: "number", Path: "/properties/score"}},
		DefaultSortOrder: []store.SortField{{Property: "id", Direction: "asc"}},
		Facets: map[string]store.FacetDefinition{
			"keywords":     {Type: store.FacetTerm, Property: "keywords", Default: true, BucketCount: 10, SortedBy: "count", MinOccurs: 1},
			"organization": {Type: store.FacetTerm, Property: "organization", BucketCount: 10, SortedBy: "value", MinOccurs: 2},
			"score":        {Type: store.FacetHistogram, Property: "score", Default: true, BucketType: store.BucketFixedCount, BucketCount: 5},
			"updated":      {Type: store.FacetHistogram, Property: "updated", BucketType: store.BucketFixedInterval, Interval: json.RawMessage(`"P30D"`), BucketCount: 12},
			"resourceType": {Type: store.FacetFilter, Default: true, BucketCount: 10, Filters: map[string]string{"dataset": "type = 'dataset'", "service": "type = 'service'"}},
		},
		Schema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["type","geometry","properties"],"properties":{"type":{"const":"Feature"},"geometry":{"type":["object","null"]},"properties":{"type":"object","required":["type","title"],"properties":{"type":{"type":"string"},"title":{"type":"string","minLength":1}},"additionalProperties":true}},"additionalProperties":true}`),
	}
}
func record(value string) json.RawMessage { return json.RawMessage(value) }
func stringPointer(value string) *string  { return &value }
func assertIDs(t *testing.T, result store.SearchResult, want ...string) {
	t.Helper()
	got := make([]string, len(result.Records))
	for i, item := range result.Records {
		got[i] = item.ID
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}
func bucketCount(facet store.FacetResult, value string) int64 {
	for _, bucket := range facet.Buckets {
		if bucket.Value == value {
			return bucket.Count
		}
	}
	return -1
}
