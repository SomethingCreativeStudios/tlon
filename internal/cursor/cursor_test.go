package cursor

import (
	"errors"
	"testing"

	"github.com/SomethingCreativeStudios/tlon/store"
)

func TestCursorIsSignedAndScoped(t *testing.T) {
	codec, err := New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	search := store.Search{Types: []string{"dataset"}, Sort: []store.SortField{{Property: "updated", Direction: "desc"}, {Property: "id", Direction: "asc"}}}
	scope, err := Scope(search)
	if err != nil {
		t.Fatal(err)
	}
	token, err := codec.Encode("records", scope, search.Sort, store.CursorNext, []any{"2026-01-01T00:00:00Z", "a"})
	if err != nil {
		t.Fatal(err)
	}
	position, err := codec.Decode(token, "records", scope, search.Sort)
	if err != nil {
		t.Fatal(err)
	}
	if position.Direction != store.CursorNext || len(position.Values) != 2 {
		t.Fatalf("unexpected position: %#v", position)
	}
	tampered := token[:len(token)-1] + "A"
	if _, err := codec.Decode(tampered, "records", scope, search.Sort); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered cursor error = %v", err)
	}
	if _, err := codec.Decode(token, "other", scope, search.Sort); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-catalog cursor error = %v", err)
	}
	changed := search
	changed.Types = []string{"service"}
	changedScope, _ := Scope(changed)
	if _, err := codec.Decode(token, "records", changedScope, search.Sort); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-filter cursor error = %v", err)
	}
}

func TestCursorRequiresRealSecret(t *testing.T) {
	if _, err := New("short"); err == nil {
		t.Fatal("short cursor secret accepted")
	}
}
