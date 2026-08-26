package server

import (
	"net/url"
	"testing"

	"github.com/SomethingCreativeStudios/tlon/api"
	"github.com/SomethingCreativeStudios/tlon/internal/cursor"
	"github.com/SomethingCreativeStudios/tlon/store"
)

func TestRecordPageLinksSuppressRepeatedFacets(t *testing.T) {
	codec, err := cursor.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{cursors: codec, publicURL: "https://records.example"}
	current, err := url.Parse("http://internal/collections/records/items?limit=10&facets=organizations")
	if err != nil {
		t.Fatal(err)
	}
	search := store.Search{Sort: []store.SortField{{Property: "updated", Direction: "desc"}, {Property: "id", Direction: "asc"}}}
	scope, err := cursor.Scope(search)
	if err != nil {
		t.Fatal(err)
	}
	links := service.links(current, scope, "records", search, []any{"2026-01-01T00:00:00Z", "a"}, []any{"2026-01-01T00:00:00Z", "b"}, true, true, "application/geo+json", pageLinkOptions{suppressFacets: true})

	for _, relation := range []string{"next", "prev"} {
		link := findLink(t, links, relation)
		parsed, err := url.Parse(link.Href)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		if !query.Has("facets") || query.Get("facets") != "" {
			t.Fatalf("%s facets query = %q in %s", relation, query.Get("facets"), link.Href)
		}
		if query.Get("cursor") == "" {
			t.Fatalf("%s link has no cursor: %s", relation, link.Href)
		}
	}

	self := findLink(t, links, "self")
	parsedSelf, err := url.Parse(self.Href)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsedSelf.Query().Get("facets"); got != "organizations" {
		t.Fatalf("self facets = %q", got)
	}
}

func TestCatalogPageLinksDoNotAddFacets(t *testing.T) {
	codec, err := cursor.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{cursors: codec, publicURL: "https://records.example"}
	current, err := url.Parse("http://internal/collections?limit=10")
	if err != nil {
		t.Fatal(err)
	}
	search := store.Search{Sort: []store.SortField{{Property: "id", Direction: "asc"}}}
	scope, err := cursor.Scope(search)
	if err != nil {
		t.Fatal(err)
	}
	links := service.links(current, scope, "$catalogs", search, nil, []any{"records"}, false, true, "application/json", pageLinkOptions{})
	next, err := url.Parse(findLink(t, links, "next").Href)
	if err != nil {
		t.Fatal(err)
	}
	if next.Query().Has("facets") {
		t.Fatalf("catalog next link unexpectedly contains facets: %s", next)
	}
}

func findLink(t *testing.T, links []api.Link, relation string) api.Link {
	t.Helper()
	for _, link := range links {
		if link.Rel == relation {
			return link
		}
	}
	t.Fatalf("missing %s link: %#v", relation, links)
	return api.Link{}
}
