package cql

import (
	"strings"
	"testing"

	"github.com/SomethingCreativeStudios/tlon/store"
)

func TestCompileIsAllowlistedAndParameterized(t *testing.T) {
	queryables := map[string]store.Queryable{"title": {Type: "string", Path: "/properties/title"}, "score": {Type: "number", Path: "/properties/score"}}
	fragment, err := Compile("title = 'x'' OR true --' AND score >= 5", queryables)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fragment.SQL, "OR true") || !strings.Contains(fragment.SQL, "$1") || len(fragment.Args) != 2 {
		t.Fatalf("unsafe fragment: %s %#v", fragment.SQL, fragment.Args)
	}
	if _, err := Compile("secret = 'x'", queryables); err == nil {
		t.Fatal("unknown property accepted")
	}
	if _, err := Compile("S_INTERSECTS(title, POINT(0 0))", queryables); err == nil {
		t.Fatal("non-Basic spatial operator accepted")
	}
	for _, filter := range []string{"title LIKE 'x%'", "score BETWEEN 1 AND 2", "score IN (1,2)", "T_AFTER(title, TIMESTAMP('2024-01-01T00:00:00Z'))", "A_CONTAINS(title, ('x'))", "title = description"} {
		if _, err := Compile(filter, map[string]store.Queryable{"title": {Type: "string"}, "description": {Type: "string"}, "score": {Type: "number"}}); err == nil {
			t.Errorf("non-Basic expression %q was accepted", filter)
		}
	}
}

func TestRejectUnsafeConfiguredPath(t *testing.T) {
	if _, err := PropertySQL("x", store.Queryable{Type: "string", Path: "/properties/x');DROP TABLE records;--"}); err == nil {
		t.Fatal("unsafe path accepted")
	}
}

func TestCompileTimestampRangeUsedByPlaygroundFacets(t *testing.T) {
	queryables := map[string]store.Queryable{"observed": {Type: "string", Format: "date-time", Path: "/properties/observed"}}
	fragment, err := Compile("observed >= TIMESTAMP('2024-01-01T00:00:00Z') AND observed < TIMESTAMP('2024-04-01T00:00:00Z')", queryables)
	if err != nil {
		t.Fatal(err)
	}
	if len(fragment.Args) != 2 {
		t.Fatalf("timestamp arguments = %#v", fragment.Args)
	}
}
