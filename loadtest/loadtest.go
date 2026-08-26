// Package loadtest creates deterministic, high-volume data for examining
// Tlon query plans and index behavior. It is operational tooling, not an OGC
// API surface.
package loadtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	storeapi "github.com/SomethingCreativeStudios/tlon/store"
)

const (
	DefaultCatalogPrefix = "load"
	DefaultCount         = 1_000_000
	DefaultBatchSize     = 5_000
	DefaultSeed          = int64(42)
	DefaultSpanDays      = 365 * 5
	MaximumCount         = 100_000_000

	SelectionBoth          = "both"
	SelectionTransactional = storeapi.StorageTransactional
	SelectionTemporal      = storeapi.StorageTemporal
)

type Storage interface {
	storeapi.Store
	BulkCreateRecords(context.Context, string, []storeapi.RecordInput) (int, error)
	AnalyzeCatalog(context.Context, string) error
}

type Options struct {
	CatalogPrefix string
	Storage       string
	Count         int
	BatchSize     int
	Seed          int64
	Start         time.Time
	SpanDays      int
	Reset         bool
	Progress      func(Progress)
}

type Progress struct {
	CatalogID   string
	Storage     string
	Completed   int
	Total       int
	Batch       int
	Elapsed     time.Duration
	RecordsPerS float64
}

type CatalogResult struct {
	CatalogID       string
	Storage         string
	Records         int
	Duration        time.Duration
	AnalyzeDuration time.Duration
	RecordsPerS     float64
}

type Result struct {
	Catalogs []CatalogResult
	Records  int
	Duration time.Duration
}

// Seed creates one catalog per requested storage class and fills it in bounded
// batches. Existing non-empty catalogs are refused unless Reset is explicit.
func Seed(ctx context.Context, storage Storage, options Options) (Result, error) {
	if storage == nil {
		return Result{}, errors.New("load-test store is required")
	}
	options = defaults(options)
	if err := validateOptions(options); err != nil {
		return Result{}, err
	}
	classes := []string{options.Storage}
	if options.Storage == SelectionBoth {
		classes = []string{storeapi.StorageTransactional, storeapi.StorageTemporal}
	}
	started := time.Now()
	result := Result{Catalogs: make([]CatalogResult, 0, len(classes))}
	for _, class := range classes {
		catalogID := options.CatalogPrefix + "-" + class
		if options.Reset {
			if err := storage.DeleteCatalog(ctx, catalogID, true); err != nil && !errors.Is(err, storeapi.ErrNotFound) {
				return result, fmt.Errorf("reset load catalog %q: %w", catalogID, err)
			}
		} else {
			_, err := storage.GetCatalog(ctx, catalogID)
			switch {
			case err == nil:
				emptyFacets := ""
				existing, err := storage.SearchRecords(ctx, catalogID, storeapi.Search{Limit: 0, Facets: &emptyFacets})
				if err != nil {
					return result, fmt.Errorf("inspect load catalog %q: %w", catalogID, err)
				}
				if existing.NumberMatched != 0 {
					return result, fmt.Errorf("load catalog %q already contains %d records; use --reset", catalogID, existing.NumberMatched)
				}
			case errors.Is(err, storeapi.ErrNotFound):
			default:
				return result, fmt.Errorf("inspect load catalog %q: %w", catalogID, err)
			}
		}
		bundle, err := Bundle(catalogID, class)
		if err != nil {
			return result, err
		}
		if _, err := storage.ApplyCatalog(ctx, bundle); err != nil {
			return result, fmt.Errorf("apply load catalog %q: %w", catalogID, err)
		}

		catalogStart := time.Now()
		for offset := 0; offset < options.Count; offset += options.BatchSize {
			batchCount := min(options.BatchSize, options.Count-offset)
			batch, err := Records(class, offset, batchCount, options.Count, options.Seed, options.Start, options.SpanDays)
			if err != nil {
				return result, err
			}
			created, err := storage.BulkCreateRecords(ctx, catalogID, batch)
			if err != nil {
				return result, fmt.Errorf("load %s records %d-%d: %w", class, offset+1, offset+batchCount, err)
			}
			result.Records += created
			if options.Progress != nil {
				elapsed := time.Since(catalogStart)
				completed := offset + created
				options.Progress(Progress{CatalogID: catalogID, Storage: class, Completed: completed, Total: options.Count, Batch: created, Elapsed: elapsed, RecordsPerS: rate(completed, elapsed)})
			}
		}
		duration := time.Since(catalogStart)
		analyzeStart := time.Now()
		if err := storage.AnalyzeCatalog(ctx, catalogID); err != nil {
			return result, fmt.Errorf("analyze load catalog %q: %w", catalogID, err)
		}
		result.Catalogs = append(result.Catalogs, CatalogResult{CatalogID: catalogID, Storage: class, Records: options.Count, Duration: duration, AnalyzeDuration: time.Since(analyzeStart), RecordsPerS: rate(options.Count, duration)})
	}
	result.Duration = time.Since(started)
	return result, nil
}

func defaults(options Options) Options {
	if options.CatalogPrefix == "" {
		options.CatalogPrefix = DefaultCatalogPrefix
	}
	if options.Storage == "" {
		options.Storage = SelectionBoth
	}
	if options.Count == 0 {
		options.Count = DefaultCount
	}
	if options.BatchSize == 0 {
		options.BatchSize = DefaultBatchSize
	}
	if options.Start.IsZero() {
		options.Start = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if options.SpanDays == 0 {
		options.SpanDays = DefaultSpanDays
	}
	return options
}

func validateOptions(options Options) error {
	switch options.Storage {
	case SelectionBoth, SelectionTransactional, SelectionTemporal:
	default:
		return fmt.Errorf("storage must be %q, %q, or %q", SelectionBoth, SelectionTransactional, SelectionTemporal)
	}
	if options.Count < 1 || options.Count > MaximumCount {
		return fmt.Errorf("count must be between 1 and %d per catalog", MaximumCount)
	}
	if options.BatchSize < 1 || options.BatchSize > 100_000 {
		return fmt.Errorf("batch size must be between 1 and 100000")
	}
	if options.SpanDays < 1 || options.SpanDays > 365*200 {
		return fmt.Errorf("span days must be between 1 and %d", 365*200)
	}
	return nil
}

func rate(count int, duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	return float64(count) / duration.Seconds()
}

// Bundle returns the catalog configuration shared by the generated data.
func Bundle(catalogID, storageClass string) (storeapi.CatalogBundle, error) {
	catalog, err := json.Marshal(map[string]any{
		"id": catalogID, "type": "Collection", "itemType": "record",
		"title":       "Tlon " + storageClass + " load-test catalog",
		"description": "Deterministic high-volume records for query-plan and index experiments.",
		"extent": map[string]any{
			"spatial":  map[string]any{"bbox": []any{[]float64{-180, -90, 180, 90}}},
			"temporal": map[string]any{"interval": []any{[]string{"2020-01-01T00:00:00Z", ".."}}},
		},
	})
	if err != nil {
		return storeapi.CatalogBundle{}, err
	}
	return storeapi.CatalogBundle{
		Catalog: catalog,
		Storage: storeapi.CatalogStorage{
			Class: storageClass,
			Indexes: []storeapi.CatalogIndex{
				{
					Name: "category-organization-score",
					Keys: []storeapi.IndexKey{{Property: "category"}, {Property: "organization"}, {Property: "score"}},
				},
				{
					Name: "organization-updated",
					Keys: []storeapi.IndexKey{{Property: "organization"}, {Property: "updated", Direction: "desc"}, {Property: "id"}},
				},
			},
		},
		Queryables: map[string]storeapi.Queryable{
			"organization": {Title: "Organization", Type: "string", Path: "/properties/organization"},
			"keywords":     {Title: "Keywords", Type: "array", Items: &storeapi.Queryable{Type: "string"}, Path: "/properties/keywords"},
			"score":        {Title: "Score", Type: "number", Path: "/properties/score"},
			"status":       {Title: "Status", Type: "string", Path: "/properties/status"},
			"source":       {Title: "Source", Type: "string", Path: "/properties/source"},
			"category":     {Title: "Category", Type: "string", Path: "/properties/category"},
			"observed":     {Title: "Observed", Type: "string", Format: "date-time", Path: "/properties/observed"},
		},
		Sortables: map[string]storeapi.Sortable{
			"title":    {Type: "string", Path: "/properties/title"},
			"score":    {Type: "number", Path: "/properties/score"},
			"observed": {Type: "string", Format: "date-time", Path: "/properties/observed"},
			"updated":  {Type: "string", Format: "date-time", Path: "/properties/updated"},
		},
		DefaultSortOrder: []storeapi.SortField{{Property: "updated", Direction: "desc"}},
		Facets: map[string]storeapi.FacetDefinition{
			"organizations": {Type: storeapi.FacetTerm, Property: "organization", Default: true, BucketCount: 20, SortedBy: "count", MinOccurs: 1},
			"categories":    {Type: storeapi.FacetTerm, Property: "category", Default: true, BucketCount: 20, SortedBy: "count", MinOccurs: 1},
			"statuses":      {Type: storeapi.FacetTerm, Property: "status", BucketCount: 10, SortedBy: "count", MinOccurs: 1},
			"scores":        {Type: storeapi.FacetHistogram, Property: "score", Default: true, BucketType: storeapi.BucketFixedCount, BucketCount: 10},
			"resourceTypes": {Type: storeapi.FacetFilter, BucketCount: 10, Filters: map[string]string{"observations": "type = 'observation'", "datasets": "type = 'dataset'", "services": "type = 'service'", "highScore": "score >= 90"}},
		},
		Schema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["type","geometry","properties"],"properties":{"type":{"const":"Feature"},"geometry":{"type":["object","null"]},"time":{"type":["object","null"]},"properties":{"type":"object","required":["type","title","organization","score","status","source","category","observed"],"additionalProperties":true}},"additionalProperties":true}`),
	}, nil
}
