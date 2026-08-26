package record

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const Profile = "http://www.opengis.net/def/profile/OGC/0/ogc-record"

var ErrInvalid = errors.New("invalid record")

type Document map[string]any

type ExternalID struct{ Scheme, Value, Canonical string }
type Derived struct {
	Geometry    *string
	HasTime     bool
	TimeStart   *time.Time
	TimeEnd     *time.Time
	ExternalIDs []ExternalID
}

func Decode(raw json.RawMessage) (Document, error) {
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%w: malformed JSON: %v", ErrInvalid, err)
	}
	if err := Validate(doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func Validate(doc Document) error {
	if typ, ok := doc["type"].(string); !ok || typ != "Feature" {
		return fmt.Errorf("%w: type must be Feature", ErrInvalid)
	}
	props, ok := doc["properties"].(map[string]any)
	if !ok {
		return fmt.Errorf("%w: properties must be an object", ErrInvalid)
	}
	if typ, ok := props["type"].(string); !ok || strings.TrimSpace(typ) == "" {
		return fmt.Errorf("%w: properties.type is required", ErrInvalid)
	}
	geometry, present := doc["geometry"]
	if !present {
		return fmt.Errorf("%w: geometry is required and may be null", ErrInvalid)
	}
	if geometry != nil {
		g, ok := geometry.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: geometry must be a GeoJSON object or null", ErrInvalid)
		}
		if err := validateGeometry(g, 0); err != nil {
			return fmt.Errorf("%w: geometry: %v", ErrInvalid, err)
		}
	}
	if _, _, _, err := deriveTime(doc["time"]); err != nil {
		return fmt.Errorf("%w: time: %v", ErrInvalid, err)
	}
	return nil
}

func ForCreate(doc Document) {
	delete(doc, "id")
	stripManaged(doc)
}

func ForPut(doc Document, pathID string) error {
	if bodyID, ok := doc["id"].(string); ok && bodyID != "" && bodyID != pathID {
		return fmt.Errorf("%w: body id %q differs from path id %q", ErrInvalid, bodyID, pathID)
	}
	delete(doc, "id")
	stripManaged(doc)
	return nil
}

// SanitizePatch returns a merge patch without server-owned fields.
func SanitizePatch(patch json.RawMessage) (json.RawMessage, error) {
	var obj map[string]any
	if err := json.Unmarshal(patch, &obj); err != nil {
		return nil, fmt.Errorf("%w: malformed merge patch", ErrInvalid)
	}
	if obj == nil {
		return nil, fmt.Errorf("%w: merge patch must be an object", ErrInvalid)
	}
	delete(obj, "id")
	if links, exists := obj["links"]; exists && links != nil {
		obj["links"] = userLinks(links)
	}
	if p, ok := obj["properties"].(map[string]any); ok {
		delete(p, "created")
		delete(p, "updated")
	}
	return json.Marshal(obj)
}

func stripManaged(doc Document) {
	if links, exists := doc["links"]; exists {
		doc["links"] = userLinks(links)
	}
	if props, ok := doc["properties"].(map[string]any); ok {
		delete(props, "created")
		delete(props, "updated")
	}
}

func Encode(doc Document) (json.RawMessage, error) { return json.Marshal(doc) }

func Derive(raw json.RawMessage) (Derived, error) {
	doc, err := Decode(raw)
	if err != nil {
		return Derived{}, err
	}
	var result Derived
	if geometry, ok := doc["geometry"]; ok && geometry != nil {
		b, err := json.Marshal(geometry)
		if err != nil {
			return result, err
		}
		value := string(b)
		result.Geometry = &value
	}
	result.TimeStart, result.TimeEnd, result.HasTime, err = deriveTime(doc["time"])
	if err != nil {
		return result, fmt.Errorf("%w: time: %v", ErrInvalid, err)
	}
	if values, ok := doc["externalIds"].([]any); ok {
		for _, item := range values {
			value, ok := item.(map[string]any)
			if !ok {
				return result, fmt.Errorf("%w: externalIds entries must be objects", ErrInvalid)
			}
			text, ok := value["value"].(string)
			if !ok || text == "" {
				return result, fmt.Errorf("%w: externalIds.value is required", ErrInvalid)
			}
			scheme, _ := value["scheme"].(string)
			canonical := text
			if scheme != "" {
				canonical = scheme + ":" + text
			}
			result.ExternalIDs = append(result.ExternalIDs, ExternalID{Scheme: scheme, Value: text, Canonical: canonical})
		}
	}
	return result, nil
}

// DeriveCatalog extracts the normalized search columns from a catalog object.
func DeriveCatalog(raw json.RawMessage) (Derived, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Derived{}, err
	}
	var result Derived
	if extent, ok := doc["extent"].(map[string]any); ok {
		if spatial, ok := extent["spatial"].(map[string]any); ok {
			if boxes, ok := spatial["bbox"].([]any); ok && len(boxes) > 0 {
				if box, ok := numberArray(boxes[0]); ok && len(box) >= 4 {
					maxX, maxY := box[2], box[3]
					if len(box) >= 6 {
						maxX, maxY = box[3], box[4]
					}
					geometry := bboxGeometry(box[0], box[1], maxX, maxY)
					b, _ := json.Marshal(geometry)
					value := string(b)
					result.Geometry = &value
				}
			}
		}
		if temporal, ok := extent["temporal"].(map[string]any); ok {
			if intervals, ok := temporal["interval"].([]any); ok && len(intervals) > 0 {
				result.TimeStart, result.TimeEnd, result.HasTime, _ = deriveTime(map[string]any{"interval": intervals[0]})
			}
		}
	}
	if values, ok := doc["externalIds"].([]any); ok {
		for _, item := range values {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			value, _ := entry["value"].(string)
			if value == "" {
				continue
			}
			scheme, _ := entry["scheme"].(string)
			canonical := value
			if scheme != "" {
				canonical = scheme + ":" + value
			}
			result.ExternalIDs = append(result.ExternalIDs, ExternalID{Scheme: scheme, Value: value, Canonical: canonical})
		}
	}
	return result, nil
}

func ParseDatetime(value string) (*time.Time, *time.Time, error) {
	if strings.Contains(value, "/") {
		parts := strings.Split(value, "/")
		if len(parts) != 2 {
			return nil, nil, fmt.Errorf("invalid interval")
		}
		start, err := parseEndpoint(parts[0], false)
		if err != nil {
			return nil, nil, err
		}
		end, err := parseEndpoint(parts[1], true)
		if err != nil {
			return nil, nil, err
		}
		if start != nil && end != nil && start.After(*end) {
			return nil, nil, fmt.Errorf("interval starts after it ends")
		}
		return start, end, nil
	}
	start, err := parseEndpoint(value, false)
	if err != nil {
		return nil, nil, err
	}
	end, err := parseEndpoint(value, true)
	if err != nil {
		return nil, nil, err
	}
	return start, end, nil
}

func deriveTime(value any) (*time.Time, *time.Time, bool, error) {
	if value == nil {
		return nil, nil, false, nil
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, nil, false, fmt.Errorf("must be an object or null")
	}
	kinds := 0
	for _, name := range []string{"interval", "timestamp", "date"} {
		if _, exists := obj[name]; exists {
			kinds++
		}
	}
	if kinds != 1 {
		return nil, nil, false, fmt.Errorf("must contain exactly one of date, timestamp, or interval")
	}
	if interval, ok := obj["interval"].([]any); ok {
		if len(interval) != 2 {
			return nil, nil, false, fmt.Errorf("interval must have two endpoints")
		}
		startText, ok1 := interval[0].(string)
		endText, ok2 := interval[1].(string)
		if !ok1 || !ok2 {
			return nil, nil, false, fmt.Errorf("interval endpoints must be strings")
		}
		start, err := parseEndpoint(startText, false)
		if err != nil {
			return nil, nil, false, err
		}
		end, err := parseEndpoint(endText, true)
		if err != nil {
			return nil, nil, false, err
		}
		if start != nil && end != nil && start.After(*end) {
			return nil, nil, false, fmt.Errorf("interval starts after it ends")
		}
		return start, end, true, nil
	}
	if timestamp, ok := obj["timestamp"].(string); ok {
		value, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			return nil, nil, false, err
		}
		value = value.UTC()
		return &value, &value, true, nil
	}
	if date, ok := obj["date"].(string); ok {
		start, err := time.Parse("2006-01-02", date)
		if err != nil {
			return nil, nil, false, err
		}
		end := start.Add(24*time.Hour - time.Nanosecond)
		return &start, &end, true, nil
	}
	return nil, nil, false, nil
}

func validateGeometry(geometry map[string]any, depth int) error {
	if depth > 16 {
		return fmt.Errorf("GeometryCollection nesting is too deep")
	}
	typ, ok := geometry["type"].(string)
	if !ok || typ == "" {
		return fmt.Errorf("type is required")
	}
	if typ == "GeometryCollection" {
		items, ok := geometry["geometries"].([]any)
		if !ok {
			return fmt.Errorf("GeometryCollection.geometries must be an array")
		}
		for i, item := range items {
			child, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("geometries[%d] must be an object", i)
			}
			if err := validateGeometry(child, depth+1); err != nil {
				return fmt.Errorf("geometries[%d]: %w", i, err)
			}
		}
		return nil
	}
	coordinates, exists := geometry["coordinates"]
	if !exists {
		return fmt.Errorf("coordinates is required")
	}
	switch typ {
	case "Point":
		return validatePosition(coordinates)
	case "MultiPoint":
		return validatePositionArray(coordinates, 1)
	case "LineString":
		return validatePositionArray(coordinates, 2)
	case "MultiLineString":
		return validateNestedPositionArrays(coordinates, 2, false)
	case "Polygon":
		return validateNestedPositionArrays(coordinates, 4, true)
	case "MultiPolygon":
		polygons, ok := coordinates.([]any)
		if !ok {
			return fmt.Errorf("coordinates must be an array")
		}
		for i, polygon := range polygons {
			if err := validateNestedPositionArrays(polygon, 4, true); err != nil {
				return fmt.Errorf("polygon %d: %w", i, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported type %q", typ)
	}
}

func validateNestedPositionArrays(value any, minimum int, closed bool) error {
	arrays, ok := value.([]any)
	if !ok {
		return fmt.Errorf("coordinates must be an array")
	}
	for i, array := range arrays {
		if err := validatePositionArray(array, minimum); err != nil {
			return fmt.Errorf("part %d: %w", i, err)
		}
		if closed {
			positions := array.([]any)
			first, last := positions[0].([]any), positions[len(positions)-1].([]any)
			if !samePosition(first, last) {
				return fmt.Errorf("ring %d is not closed", i)
			}
		}
	}
	return nil
}

func validatePositionArray(value any, minimum int) error {
	positions, ok := value.([]any)
	if !ok || len(positions) < minimum {
		return fmt.Errorf("coordinates must contain at least %d positions", minimum)
	}
	for i, position := range positions {
		if err := validatePosition(position); err != nil {
			return fmt.Errorf("position %d: %w", i, err)
		}
	}
	return nil
}

func validatePosition(value any) error {
	position, ok := value.([]any)
	if !ok || len(position) < 2 {
		return fmt.Errorf("position must contain at least longitude and latitude")
	}
	longitude, okLongitude := position[0].(float64)
	latitude, okLatitude := position[1].(float64)
	if !okLongitude || !okLatitude {
		return fmt.Errorf("longitude and latitude must be numbers")
	}
	if longitude < -180 || longitude > 180 || latitude < -90 || latitude > 90 {
		return fmt.Errorf("position is outside CRS84")
	}
	for _, ordinate := range position[2:] {
		if _, ok := ordinate.(float64); !ok {
			return fmt.Errorf("additional ordinates must be numbers")
		}
	}
	return nil
}

func samePosition(left, right []any) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func parseEndpoint(value string, end bool) (*time.Time, error) {
	if value == ".." || value == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		parsed = parsed.UTC()
		return &parsed, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, fmt.Errorf("invalid date or timestamp %q", value)
	}
	if end {
		parsed = parsed.Add(24*time.Hour - time.Nanosecond)
	}
	return &parsed, nil
}

func numberArray(value any) ([]float64, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]float64, len(items))
	for i, item := range items {
		n, ok := item.(float64)
		if !ok {
			return nil, false
		}
		result[i] = n
	}
	return result, true
}

func bboxGeometry(minX, minY, maxX, maxY float64) map[string]any {
	polygon := func(a, b float64) []any {
		return []any{[]any{[]float64{a, minY}, []float64{b, minY}, []float64{b, maxY}, []float64{a, maxY}, []float64{a, minY}}}
	}
	if minX <= maxX {
		return map[string]any{"type": "Polygon", "coordinates": polygon(minX, maxX)}
	}
	return map[string]any{"type": "MultiPolygon", "coordinates": []any{polygon(minX, 180), polygon(-180, maxX)}}
}

// Materialize adds the authoritative id, timestamps, and absolute navigation
// links to a copy of a stored record.
func Materialize(raw json.RawMessage, publicURL, catalogID, recordID string, created, updated time.Time) (json.RawMessage, error) {
	doc, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	doc["id"] = recordID
	props := doc["properties"].(map[string]any)
	props["created"] = created.UTC().Format(time.RFC3339Nano)
	props["updated"] = updated.UTC().Format(time.RFC3339Nano)
	base := strings.TrimRight(publicURL, "/")
	catalogPath := "/collections/" + url.PathEscape(catalogID)
	itemPath := catalogPath + "/items/" + url.PathEscape(recordID)
	links := userLinks(doc["links"])
	links = append(links,
		map[string]any{"rel": "self", "type": "application/geo+json", "href": base + itemPath},
		map[string]any{"rel": "collection", "type": "application/json", "href": base + catalogPath},
		map[string]any{"rel": "profile", "type": "text/html", "href": Profile},
	)
	doc["links"] = links
	conformsTo := []string{}
	if values, ok := doc["conformsTo"].([]any); ok {
		for _, value := range values {
			if text, ok := value.(string); ok && text != "" && text != Profile {
				conformsTo = append(conformsTo, text)
			}
		}
	}
	conformsTo = append(conformsTo, Profile)
	doc["conformsTo"] = conformsTo
	return json.Marshal(doc)
}

func userLinks(value any) []any {
	items, ok := value.([]any)
	if !ok {
		return []any{}
	}
	result := make([]any, 0, len(items))
	for _, item := range items {
		link, ok := item.(map[string]any)
		if !ok {
			continue
		}
		rel, _ := link["rel"].(string)
		switch rel {
		case "self", "collection", "profile":
			continue
		}
		result = append(result, link)
	}
	return result
}
