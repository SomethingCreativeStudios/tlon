package postgres

import (
	"strings"
	"testing"

	storeapi "github.com/SomethingCreativeStudios/tlon/store"
)

func TestNonNullSortUsesIndexCompatibleOrder(t *testing.T) {
	column := sortColumn{
		Field:      storeapi.SortField{Property: "updated", Direction: "desc"},
		Expression: "r.updated_at",
		Cast:       "timestamptz",
		NonNull:    true,
	}
	if got := sortOrderSQL(column, true); got != "r.updated_at DESC" {
		t.Fatalf("forward order = %q", got)
	}
	column.Field.Direction = "asc"
	if got := sortOrderSQL(column, false); got != "r.updated_at ASC" {
		t.Fatalf("reverse order = %q", got)
	}
}

func TestNullableSortRetainsExplicitNullOrdering(t *testing.T) {
	column := sortColumn{
		Field:      storeapi.SortField{Property: "score", Direction: "asc"},
		Expression: "score_expression",
		Cast:       "numeric",
	}
	if got := sortOrderSQL(column, true); got != "score_expression ASC NULLS LAST" {
		t.Fatalf("forward order = %q", got)
	}
	column.Field.Direction = "desc"
	if got := sortOrderSQL(column, false); got != "score_expression DESC NULLS FIRST" {
		t.Fatalf("reverse order = %q", got)
	}
}

func TestNonNullKeysetPredicateAvoidsNullBranches(t *testing.T) {
	columns := []sortColumn{
		{Field: storeapi.SortField{Property: "updated", Direction: "desc"}, Expression: "r.updated_at", Cast: "timestamptz", NonNull: true},
		{Field: storeapi.SortField{Property: "id", Direction: "asc"}, Expression: "r.id", Cast: "text", NonNull: true},
	}
	b := sqlBuilder{}
	predicate, err := keysetPredicate(&b, columns, true, []any{"2026-08-26T02:28:18Z", "load-10"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(predicate, "IS NULL") || strings.Contains(predicate, "IS NOT DISTINCT FROM") {
		t.Fatalf("predicate contains nullable branches: %s", predicate)
	}
	want := "(r.updated_at < $1::timestamptz OR (r.updated_at = $2::timestamptz AND r.id > $3::text))"
	if predicate != want {
		t.Fatalf("predicate = %q, want %q", predicate, want)
	}
}

func TestNormalizeSortMarksManagedColumnsNonNull(t *testing.T) {
	allowed := map[string]storeapi.Sortable{
		"updated": {Type: "string", Format: "date-time", Path: "/properties/updated"},
		"id":      {Type: "string", Path: "/id"},
	}
	columns, err := normalizeSort([]storeapi.SortField{{Property: "updated", Direction: "desc"}}, nil, allowed)
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 2 || !columns[0].NonNull || !columns[1].NonNull {
		t.Fatalf("managed sort columns = %#v", columns)
	}
}
