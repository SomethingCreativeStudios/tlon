// Package demo provides deterministic catalog and record fixtures for manually
// exercising a live Tlon deployment. It uses the same catalog and mutation
// boundaries as production callers; it does not write fixture SQL.
package demo

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/SomethingCreativeStudios/tlon/application"
	"github.com/SomethingCreativeStudios/tlon/auth"
	"github.com/SomethingCreativeStudios/tlon/store"
)

const (
	DefaultCatalogID = "demo"
	DefaultCount     = 120
	DefaultSeed      = int64(42)
	MaximumCount     = 10000
)

//go:embed catalog.json
var assets embed.FS

// Record is a deterministic client-ID record suitable for a PUT operation.
type Record struct {
	ID       string
	Document json.RawMessage
}

type SeedOptions struct {
	CatalogID string
	Count     int
	Seed      int64
	Reset     bool
	PublicURL string
}

type SeedResult struct {
	CatalogID string
	Created   int
	Replaced  int
	Seed      int64
}

// Bundle returns the checked-in demo catalog, optionally replacing its ID.
func Bundle(catalogID string) (store.CatalogBundle, error) {
	if catalogID == "" {
		catalogID = DefaultCatalogID
	}
	body, err := assets.ReadFile("catalog.json")
	if err != nil {
		return store.CatalogBundle{}, fmt.Errorf("read demo catalog: %w", err)
	}
	var bundle store.CatalogBundle
	if err := json.Unmarshal(body, &bundle); err != nil {
		return store.CatalogBundle{}, fmt.Errorf("decode demo catalog: %w", err)
	}
	var document map[string]any
	if err := json.Unmarshal(bundle.Catalog, &document); err != nil {
		return store.CatalogBundle{}, fmt.Errorf("decode demo catalog document: %w", err)
	}
	document["id"] = catalogID
	bundle.Catalog, err = json.Marshal(document)
	if err != nil {
		return store.CatalogBundle{}, fmt.Errorf("encode demo catalog document: %w", err)
	}
	return bundle, nil
}

// Records produces stable IDs and documents. A different seed varies content
// while retaining IDs, making repeated seed runs convenient upserts.
func Records(count int, seed int64) ([]Record, error) {
	if count < 1 || count > MaximumCount {
		return nil, fmt.Errorf("demo record count must be between 1 and %d", MaximumCount)
	}
	generator := sequence{state: uint64(seed)}
	result := make([]Record, 0, count)
	for index := 0; index < count; index++ {
		id := fmt.Sprintf("demo-%04d", index+1)
		document := sampleDocument(id, index, &generator)
		raw, err := json.Marshal(document)
		if err != nil {
			return nil, fmt.Errorf("encode demo record %s: %w", id, err)
		}
		result = append(result, Record{ID: id, Document: raw})
	}
	return result, nil
}

// Seed applies the catalog and upserts generated records through the reusable
// application layer. Reset removes only the selected demo catalog first.
func Seed(ctx context.Context, storage store.Store, options SeedOptions) (SeedResult, error) {
	if storage == nil {
		return SeedResult{}, errors.New("demo seed store is required")
	}
	if options.CatalogID == "" {
		options.CatalogID = DefaultCatalogID
	}
	if options.Count == 0 {
		options.Count = DefaultCount
	}
	if options.PublicURL == "" {
		options.PublicURL = "http://localhost:8080"
	}
	publicURL, err := url.Parse(options.PublicURL)
	if err != nil || (publicURL.Scheme != "http" && publicURL.Scheme != "https") || publicURL.Host == "" {
		return SeedResult{}, errors.New("demo public URL must be an absolute HTTP(S) URL")
	}
	bundle, err := Bundle(options.CatalogID)
	if err != nil {
		return SeedResult{}, err
	}
	records, err := Records(options.Count, options.Seed)
	if err != nil {
		return SeedResult{}, err
	}
	if options.Reset {
		if err := storage.DeleteCatalog(ctx, options.CatalogID, true); err != nil && !errors.Is(err, store.ErrNotFound) {
			return SeedResult{}, fmt.Errorf("reset demo catalog %q: %w", options.CatalogID, err)
		}
	}
	if _, err := storage.ApplyCatalog(ctx, bundle); err != nil {
		return SeedResult{}, fmt.Errorf("apply demo catalog %q: %w", options.CatalogID, err)
	}
	app := application.New(storage, auth.External{}, options.PublicURL)
	result := SeedResult{CatalogID: options.CatalogID, Seed: options.Seed}
	for _, record := range records {
		put, err := app.Put(ctx, options.CatalogID, record.ID, record.Document, nil, nil)
		if err != nil {
			return result, fmt.Errorf("seed record %q: %w", record.ID, err)
		}
		if put.Created {
			result.Created++
		} else {
			result.Replaced++
		}
	}
	return result, nil
}

type sequence struct{ state uint64 }

// next is SplitMix64: tiny, deterministic, and independent of Go's math/rand
// implementation details.
func (s *sequence) next() uint64 {
	s.state += 0x9e3779b97f4a7c15
	value := s.state
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func (s *sequence) index(length int) int { return int(s.next() % uint64(length)) }

type place struct {
	name      string
	longitude float64
	latitude  float64
}

var (
	organizations = []string{"NOAA", "USGS", "NASA", "Environment Canada", "Copernicus", "OpenStreetMap", "World Bank", "Natural Earth"}
	topics        = []string{"climate", "oceans", "elevation", "transport", "biodiversity", "population", "weather", "land cover", "wildfire", "water quality"}
	recordTypes   = []string{"dataset", "dataset", "dataset", "service", "model", "application", "collection"}
	statuses      = []string{"active", "active", "active", "planned", "deprecated"}
	licenses      = []string{"CC-BY-4.0", "CC0-1.0", "ODbL-1.0", "public-domain"}
	adjectives    = []string{"Regional", "Global", "Seasonal", "Operational", "Historic", "Experimental", "Reference", "Near-real-time"}
	places        = []place{{"New York", -74.006, 40.7128}, {"Vancouver", -123.1207, 49.2827}, {"Reykjavik", -21.9426, 64.1466}, {"Nairobi", 36.8219, -1.2921}, {"Singapore", 103.8198, 1.3521}, {"Sydney", 151.2093, -33.8688}, {"Santiago", -70.6693, -33.4489}, {"Tokyo", 139.6917, 35.6895}, {"Honolulu", -157.8583, 21.3069}, {"Lisbon", -9.1393, 38.7223}}
)

func sampleDocument(id string, index int, generator *sequence) map[string]any {
	organization := organizations[generator.index(len(organizations))]
	topic := topics[generator.index(len(topics))]
	secondary := topics[generator.index(len(topics))]
	recordType := recordTypes[generator.index(len(recordTypes))]
	status := statuses[generator.index(len(statuses))]
	place := places[generator.index(len(places))]
	observed := time.Date(2021, 1, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, index*13+generator.index(9))
	score := float64(generator.next()%1001) / 10
	keywords := []string{topic, recordType, "demo"}
	if secondary != topic {
		keywords = append(keywords, secondary)
	}
	properties := map[string]any{
		"type":         recordType,
		"title":        fmt.Sprintf("%s %s resource for %s", adjectives[generator.index(len(adjectives))], topic, place.name),
		"description":  fmt.Sprintf("A deterministic %s sample about %s near %s, published by %s for testing Records API behavior.", recordType, topic, place.name, organization),
		"organization": organization,
		"keywords":     keywords,
		"score":        score,
		"status":       status,
		"license":      licenses[generator.index(len(licenses))],
		"observed":     observed.Format(time.RFC3339),
		"published":    observed.AddDate(0, 0, 7+generator.index(40)).Format("2006-01-02"),
		"hasDownload":  recordType == "dataset" || recordType == "collection",
	}
	document := map[string]any{
		"id":         id,
		"type":       "Feature",
		"geometry":   sampleGeometry(index, place, generator),
		"properties": properties,
		"externalIds": []any{
			map[string]any{"scheme": "local", "value": fmt.Sprintf("TLON-%06d", index+1)},
		},
		"links": []any{
			map[string]any{"rel": "about", "type": "text/html", "href": "https://example.com/tlon-demo/" + id},
		},
	}
	if value := sampleTime(index, observed); value != nil {
		document["time"] = value
	}
	if recordType == "dataset" && index%4 == 0 {
		document["externalIds"] = append(document["externalIds"].([]any), map[string]any{"scheme": "doi", "value": fmt.Sprintf("10.9999/tlon.%04d", index+1)})
	}
	return document
}

func sampleGeometry(index int, place place, generator *sequence) any {
	switch index % 10 {
	case 0:
		return nil
	case 1:
		// Two polygons on either side of the antimeridian exercise wrapped bbox
		// searches without representing a polygon across the rest of the globe.
		return map[string]any{"type": "MultiPolygon", "coordinates": []any{
			ring(170, -18, 180, -8),
			ring(-180, -18, -170, -8),
		}}
	case 2:
		return map[string]any{"type": "Polygon", "coordinates": []any{ringCoordinates(place.longitude-1.5, place.latitude-1, place.longitude+1.5, place.latitude+1)}}
	case 3:
		return map[string]any{"type": "LineString", "coordinates": []any{[]float64{place.longitude - 1, place.latitude - .5}, []float64{place.longitude, place.latitude}, []float64{place.longitude + 1, place.latitude + .5}}}
	default:
		longitude := place.longitude + float64(int(generator.next()%1201)-600)/1000
		latitude := place.latitude + float64(int(generator.next()%801)-400)/1000
		return map[string]any{"type": "Point", "coordinates": []float64{longitude, latitude}}
	}
}

func ring(minX, minY, maxX, maxY float64) []any {
	return []any{ringCoordinates(minX, minY, maxX, maxY)}
}

func ringCoordinates(minX, minY, maxX, maxY float64) []any {
	return []any{[]float64{minX, minY}, []float64{maxX, minY}, []float64{maxX, maxY}, []float64{minX, maxY}, []float64{minX, minY}}
}

func sampleTime(index int, observed time.Time) any {
	date := observed.Format("2006-01-02")
	switch index % 7 {
	case 0:
		return nil
	case 1:
		return map[string]any{"date": date}
	case 2:
		return map[string]any{"timestamp": observed.Format(time.RFC3339)}
	case 3:
		return map[string]any{"interval": []string{observed.AddDate(0, 0, -14).Format("2006-01-02"), date}}
	case 4:
		return map[string]any{"interval": []string{"..", date}}
	case 5:
		return map[string]any{"interval": []string{date, ".."}}
	default:
		return map[string]any{"interval": []string{observed.AddDate(0, -2, 0).Format(time.RFC3339), observed.AddDate(0, 2, 0).Format(time.RFC3339)}}
	}
}
