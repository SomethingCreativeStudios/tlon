// Package recordschema compiles and evaluates the JSON Schemas supplied in
// catalog bundles. It intentionally has no remote URL loader: a catalog schema
// may use local references and standard metaschemas, but applying a catalog or
// writing a record must never perform an outbound fetch.
package recordschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/SomethingCreativeStudios/tlon/store"
)

const resourceURL = "https://tlon.invalid/catalog-record-schema"

func Compile(raw json.RawMessage) (*jsonschema.Schema, error) {
	document, err := decode(raw)
	if err != nil {
		return nil, fmt.Errorf("decode JSON Schema: %w", err)
	}
	if _, ok := document.(map[string]any); !ok {
		return nil, fmt.Errorf("JSON Schema must be an object")
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(resourceURL, document); err != nil {
		return nil, fmt.Errorf("add JSON Schema: %w", err)
	}
	compiled, err := compiler.Compile(resourceURL)
	if err != nil {
		return nil, fmt.Errorf("compile JSON Schema: %w", err)
	}
	return compiled, nil
}

func Validate(schemaDocument, instanceDocument json.RawMessage) error {
	compiled, err := Compile(schemaDocument)
	if err != nil {
		return err
	}
	instance, err := decode(instanceDocument)
	if err != nil {
		return fmt.Errorf("decode record: %w", err)
	}
	if err := compiled.Validate(instance); err != nil {
		return fmt.Errorf("record does not match catalog schema: %w", err)
	}
	return nil
}

// ValidateBundle checks both the catalog's record schema and every configured
// queryable/sortable value whose SQL representation depends on its declared
// type. Missing and null extension properties remain valid.
func ValidateBundle(bundle store.CatalogBundle, instanceDocument json.RawMessage) error {
	if err := Validate(bundle.Schema, instanceDocument); err != nil {
		return err
	}
	document, err := decode(instanceDocument)
	if err != nil {
		return fmt.Errorf("decode record: %w", err)
	}
	for name, definition := range bundle.Queryables {
		if err := validateConfiguredValue(document, name, definition.Path, definition); err != nil {
			return fmt.Errorf("queryable %q: %w", name, err)
		}
	}
	for name, definition := range bundle.Sortables {
		queryable := store.Queryable{Type: definition.Type, Format: definition.Format, Path: definition.Path}
		if err := validateConfiguredValue(document, name, definition.Path, queryable); err != nil {
			return fmt.Errorf("sortable %q: %w", name, err)
		}
	}
	return nil
}

func validateConfiguredValue(document any, name, path string, definition store.Queryable) error {
	if path == "" {
		if name == "id" {
			path = "/id"
		} else {
			path = "/properties/" + name
		}
	}
	// These values live in normalized columns and are materialized only when a
	// record is returned; they are intentionally absent from the stored JSON.
	if path == "/id" || path == "/properties/created" || path == "/properties/updated" {
		return nil
	}
	value, found := pointerValue(document, path)
	if !found || value == nil {
		return nil
	}
	return validateType(value, definition)
}

func pointerValue(document any, path string) (any, bool) {
	if path == "" || !strings.HasPrefix(path, "/") {
		return nil, false
	}
	current := document
	for _, token := range strings.Split(path[1:], "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[token]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func validateType(value any, definition store.Queryable) error {
	if definition.Type == "array" || definition.Items != nil {
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("must be an array")
		}
		if definition.Items != nil {
			for i, item := range items {
				if err := validateType(item, *definition.Items); err != nil {
					return fmt.Errorf("item %d: %w", i, err)
				}
			}
		}
		return nil
	}
	switch definition.Type {
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("must be a string")
		}
		switch definition.Format {
		case "date":
			if _, err := time.Parse("2006-01-02", text); err != nil {
				return fmt.Errorf("must be an RFC 3339 full-date")
			}
		case "date-time":
			if _, err := time.Parse(time.RFC3339Nano, text); err != nil {
				return fmt.Errorf("must be an RFC 3339 timestamp")
			}
		}
	case "number":
		if _, ok := value.(json.Number); !ok {
			return fmt.Errorf("must be a number")
		}
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("must be an integer")
		}
		if _, err := strconv.ParseInt(string(number), 10, 64); err != nil {
			return fmt.Errorf("must be a 64-bit integer")
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("must be a boolean")
		}
	default:
		return fmt.Errorf("has unsupported type %q", definition.Type)
	}
	return nil
}

func decode(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}
