// Package cql is the sole adapter around the pinned CQL2 dependency. No
// dependency-specific AST or SQL types cross this package boundary.
package cql

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	gocql2 "github.com/cwygoda/gocql2"
	cqlapi "github.com/cwygoda/gocql2/api"
	cqlsql "github.com/cwygoda/gocql2/sql"

	"github.com/SomethingCreativeStudios/tlon/store"
)

type Fragment struct {
	SQL  string
	Args []any
}

func Validate(filter string, queryables map[string]store.Queryable) error {
	_, err := Compile(filter, queryables)
	return err
}

func Compile(filter string, queryables map[string]store.Queryable) (Fragment, error) {
	properties := make([]cqlsql.Property, 0, len(queryables))
	for name, q := range queryables {
		typ, err := propertyType(q)
		if err != nil {
			return Fragment{}, fmt.Errorf("queryable %q: %w", name, err)
		}
		expr, err := propertyExpression(name, q, "r.")
		if err != nil {
			return Fragment{}, fmt.Errorf("queryable %q: %w", name, err)
		}
		properties = append(properties, cqlsql.Property{Name: name, Type: typ, Expr: cqlsql.RawSQL(expr)})
	}
	expr, err := gocql2.NewParser().WithConformance(cqlapi.ConformanceBasicCQL2).WithMaxDepth(32).WithAllowedProperties(cqlsql.PropertyDefinitions(properties...)...).ParseText(filter)
	if err != nil {
		return Fragment{}, fmt.Errorf("invalid Basic CQL2 Text: %w", err)
	}
	compiled, err := cqlsql.ToSQL(expr, cqlsql.PostGISDialect(), cqlsql.WithSQLProperties(properties...))
	if err != nil {
		return Fragment{}, fmt.Errorf("compile Basic CQL2 Text: %w", err)
	}
	return Fragment{SQL: compiled.Text, Args: compiled.Args}, nil
}

func PropertySQL(name string, q store.Queryable) (string, error) {
	return propertyExpression(name, q, "r.")
}

// IndexSQL returns the same typed expression used by the CQL compiler without
// a table alias so a datastore can safely embed it in an index definition.
func IndexSQL(name string, q store.Queryable) (string, error) {
	return propertyExpression(name, q, "")
}

func propertyType(q store.Queryable) (cqlapi.PropertyType, error) {
	if q.Items != nil || q.Type == "array" {
		return cqlapi.PropertyTypeArray, nil
	}
	switch q.Type {
	case "string":
		if q.Format == "date" {
			return cqlapi.PropertyTypeDate, nil
		}
		if q.Format == "date-time" {
			return cqlapi.PropertyTypeTimestamp, nil
		}
		return cqlapi.PropertyTypeString, nil
	case "number":
		return cqlapi.PropertyTypeNumber, nil
	case "integer":
		return cqlapi.PropertyTypeInteger, nil
	case "boolean":
		return cqlapi.PropertyTypeBoolean, nil
	default:
		return "", fmt.Errorf("unsupported type %q", q.Type)
	}
}

var pointerToken = regexp.MustCompile(`^[A-Za-z0-9_.:@-]+$`)

func propertyExpression(name string, q store.Queryable, qualifier string) (string, error) {
	path := q.Path
	if path == "" {
		switch name {
		case "id":
			path = "/id"
		case "created", "updated":
			path = "/properties/" + name
		default:
			path = "/properties/" + name
		}
	}
	switch path {
	case "/id":
		return qualifier + `id`, nil
	case "/properties/created":
		return qualifier + `created_at`, nil
	case "/properties/updated":
		return qualifier + `updated_at`, nil
	}
	parts, err := pointerParts(path)
	if err != nil {
		return "", err
	}
	args := make([]string, 0, len(parts)+1)
	args = append(args, qualifier+"document")
	for _, part := range parts {
		args = append(args, "'"+part+"'")
	}
	value := "jsonb_extract_path_text(" + strings.Join(args, ", ") + ")"
	switch q.Type {
	case "number":
		value = "NULLIF(" + value + ", '')::numeric"
	case "integer":
		value = "NULLIF(" + value + ", '')::bigint"
	case "boolean":
		value = "NULLIF(" + value + ", '')::boolean"
	case "string":
		if q.Format == "date" {
			value = "public.tlon_iso_date(NULLIF(" + value + ", ''))"
		}
		if q.Format == "date-time" {
			value = "public.tlon_rfc3339_timestamptz(NULLIF(" + value + ", ''))"
		}
	case "array":
		value = "jsonb_extract_path(" + strings.Join(args, ", ") + ")"
	default:
		return "", fmt.Errorf("unsupported type %q", q.Type)
	}
	return value, nil
}

func pointerParts(path string) ([]string, error) {
	if path == "" || path[0] != '/' {
		return nil, fmt.Errorf("x-tlon-path must be an absolute JSON pointer")
	}
	raw := strings.Split(path[1:], "/")
	if len(raw) == 0 || len(raw) > 16 {
		return nil, fmt.Errorf("invalid JSON pointer")
	}
	parts := make([]string, len(raw))
	for i, token := range raw {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		if !pointerToken.MatchString(token) {
			return nil, fmt.Errorf("unsafe JSON pointer token %q", token)
		}
		parts[i] = token
	}
	return parts, nil
}

var placeholder = regexp.MustCompile(`\$([0-9]+)`)

// ShiftPlaceholders composes a generated fragment after existing PostgreSQL
// parameters.
func ShiftPlaceholders(sql string, offset int) string {
	if offset == 0 {
		return sql
	}
	return placeholder.ReplaceAllStringFunc(sql, func(value string) string {
		n, _ := strconv.Atoi(value[1:])
		return "$" + strconv.Itoa(n+offset)
	})
}
