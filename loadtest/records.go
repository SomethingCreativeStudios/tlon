package loadtest

import (
	"encoding/json"
	"fmt"
	"time"

	storeapi "github.com/SomethingCreativeStudios/tlon/store"
)

var (
	loadOrganizations = []string{"NOAA", "USGS", "NASA", "Environment Canada", "Copernicus", "ESA", "JAXA", "CSIRO", "Met Office", "World Bank", "Natural Earth", "OpenStreetMap", "ECMWF", "EUMETSAT", "GEOSS", "WMO"}
	loadCategories    = []string{"weather", "climate", "oceans", "air-quality", "hydrology", "wildfire", "biodiversity", "transport", "energy", "agriculture", "elevation", "land-cover"}
	loadStatuses      = []string{"active", "active", "active", "active", "planned", "archived", "deprecated"}
	loadTypes         = []string{"observation", "observation", "observation", "observation", "dataset", "service", "collection", "model"}
	loadAdjectives    = []string{"Regional", "Global", "Operational", "Historic", "Experimental", "Reference", "Near-real-time", "Daily"}
)

// Records creates a deterministic slice whose values are independent of batch
// boundaries. Count is the total catalog size and is used to spread time over
// the configured range.
func Records(storageClass string, offset, batchCount, total int, seed int64, start time.Time, spanDays int) ([]storeapi.RecordInput, error) {
	if storageClass != storeapi.StorageTransactional && storageClass != storeapi.StorageTemporal {
		return nil, fmt.Errorf("unsupported storage class %q", storageClass)
	}
	if offset < 0 || batchCount < 0 || total < 1 || offset+batchCount > total {
		return nil, fmt.Errorf("invalid record batch %d:%d of %d", offset, batchCount, total)
	}
	result := make([]storeapi.RecordInput, 0, batchCount)
	for index := offset; index < offset+batchCount; index++ {
		generator := loadSequence{state: uint64(seed) ^ uint64(index+1)*0x9e3779b97f4a7c15}
		observed := recordTime(index, total, start, spanDays, &generator)
		organization := loadOrganizations[generator.index(len(loadOrganizations))]
		category := loadCategories[generator.index(len(loadCategories))]
		resourceType := loadTypes[generator.index(len(loadTypes))]
		status := loadStatuses[generator.index(len(loadStatuses))]
		source := fmt.Sprintf("source-%04d", generator.next()%1024)
		score := float64(generator.next()%10001) / 100
		id := fmt.Sprintf("load-%012d", index+1)
		keywords := []string{category, resourceType, status}
		if index%3 == 0 {
			keywords = append(keywords, organization)
		}
		document := map[string]any{
			"id":       id,
			"type":     "Feature",
			"geometry": loadGeometry(index, &generator),
			"properties": map[string]any{
				"type":         resourceType,
				"title":        fmt.Sprintf("%s %s record %012d", loadAdjectives[generator.index(len(loadAdjectives))], category, index+1),
				"description":  fmt.Sprintf("Synthetic %s metadata from %s for %s query and index evaluation.", resourceType, organization, category),
				"organization": organization,
				"keywords":     keywords,
				"score":        score,
				"status":       status,
				"source":       source,
				"category":     category,
				"observed":     observed.Format(time.RFC3339),
			},
			"externalIds": []any{map[string]any{"scheme": "load", "value": fmt.Sprintf("%s-%012d", storageClass, index+1)}},
		}
		if storageClass == storeapi.StorageTemporal {
			if index%10 == 0 {
				document["time"] = map[string]any{"interval": []string{observed.Format(time.RFC3339), observed.Add(time.Duration(1+generator.next()%48) * time.Hour).Format(time.RFC3339)}}
			} else {
				document["time"] = map[string]any{"timestamp": observed.Format(time.RFC3339)}
			}
		} else {
			document["time"] = transactionalTime(index, observed, &generator)
		}
		raw, err := json.Marshal(document)
		if err != nil {
			return nil, fmt.Errorf("encode load record %s: %w", id, err)
		}
		result = append(result, storeapi.RecordInput{ID: id, Document: raw})
	}
	return result, nil
}

func recordTime(index, total int, start time.Time, spanDays int, generator *loadSequence) time.Time {
	// Calculate in whole seconds so multiplying a large record index by a
	// nanosecond duration cannot overflow int64 before division.
	spanSeconds := int64(spanDays) * 24 * 60 * 60
	position := time.Duration((int64(index)*spanSeconds)/int64(total)) * time.Second
	jitter := time.Duration(generator.next()%uint64(6*time.Hour)) - 3*time.Hour
	value := start.UTC().Add(position + jitter)
	if value.Before(start.UTC()) {
		return start.UTC()
	}
	return value
}

func transactionalTime(index int, observed time.Time, generator *loadSequence) any {
	switch index % 10 {
	case 0:
		return nil
	case 1:
		return map[string]any{"interval": []string{"..", observed.Format(time.RFC3339)}}
	case 2:
		return map[string]any{"interval": []string{observed.Format(time.RFC3339), ".."}}
	case 3, 4:
		return map[string]any{"interval": []string{observed.Add(-24 * time.Hour).Format(time.RFC3339), observed.Add(time.Duration(1+generator.next()%168) * time.Hour).Format(time.RFC3339)}}
	default:
		return map[string]any{"timestamp": observed.Format(time.RFC3339)}
	}
}

func loadGeometry(index int, generator *loadSequence) any {
	if index%20 == 0 {
		return nil
	}
	longitude := float64(int64(generator.next()%3_600_000)-1_800_000) / 10_000
	latitude := float64(int64(generator.next()%1_800_000)-900_000) / 10_000
	if index%50 != 0 {
		return map[string]any{"type": "Point", "coordinates": []float64{longitude, latitude}}
	}
	delta := 0.05 + float64(generator.next()%100)/1000
	minX, maxX := max(-180.0, longitude-delta), min(180.0, longitude+delta)
	minY, maxY := max(-90.0, latitude-delta), min(90.0, latitude+delta)
	return map[string]any{"type": "Polygon", "coordinates": []any{[]any{
		[]float64{minX, minY}, []float64{maxX, minY}, []float64{maxX, maxY}, []float64{minX, maxY}, []float64{minX, minY},
	}}}
}

type loadSequence struct{ state uint64 }

func (s *loadSequence) next() uint64 {
	s.state += 0x9e3779b97f4a7c15
	value := s.state
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func (s *loadSequence) index(length int) int { return int(s.next() % uint64(length)) }
