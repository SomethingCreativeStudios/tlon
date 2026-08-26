package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/SomethingCreativeStudios/tlon/api"
	"github.com/SomethingCreativeStudios/tlon/application"
	"github.com/SomethingCreativeStudios/tlon/auth"
	"github.com/SomethingCreativeStudios/tlon/catalog"
	"github.com/SomethingCreativeStudios/tlon/server"
	"github.com/SomethingCreativeStudios/tlon/store"
)

type fakeStore struct{ catalog store.Catalog }

func (f *fakeStore) Ping(context.Context) error { return nil }
func (f *fakeStore) Close()                     {}
func (f *fakeStore) ApplyCatalog(context.Context, store.CatalogBundle) (store.Catalog, error) {
	return f.catalog, nil
}
func (f *fakeStore) GetCatalog(_ context.Context, id string) (store.Catalog, error) {
	if id != f.catalog.ID {
		return store.Catalog{}, store.ErrNotFound
	}
	return f.catalog, nil
}
func (f *fakeStore) ListCatalogs(context.Context, store.Search) (store.CatalogSearchResult, error) {
	return store.CatalogSearchResult{Catalogs: []store.Catalog{f.catalog}, NumberMatched: 1, SortValues: [][]any{{f.catalog.ID}}}, nil
}
func (f *fakeStore) DeleteCatalog(context.Context, string, bool) error { return nil }
func (f *fakeStore) SearchRecords(context.Context, string, store.Search) (store.SearchResult, error) {
	return store.SearchResult{Records: []store.StoredRecord{}, NumberMatched: 0, Facets: map[string]store.FacetResult{}}, nil
}
func (f *fakeStore) GetRecord(context.Context, string, string) (store.StoredRecord, error) {
	return store.StoredRecord{}, store.ErrNotFound
}
func (f *fakeStore) CreateRecord(context.Context, string, string, json.RawMessage) (store.StoredRecord, error) {
	return store.StoredRecord{}, nil
}
func (f *fakeStore) PutRecord(context.Context, string, string, json.RawMessage, *int64) (store.PutResult, error) {
	return store.PutResult{}, nil
}
func (f *fakeStore) PatchRecord(context.Context, string, string, json.RawMessage, *int64) (store.StoredRecord, error) {
	return store.StoredRecord{}, nil
}
func (f *fakeStore) DeleteRecord(context.Context, string, string, *int64) error { return nil }

func testHandler(t *testing.T) http.Handler {
	bundle := store.CatalogBundle{Catalog: json.RawMessage(`{"id":"records","type":"Collection","itemType":"record","title":"Records"}`), Queryables: map[string]store.Queryable{}, Facets: map[string]store.FacetDefinition{}}
	normalized, id, err := catalog.Normalize(bundle)
	if err != nil {
		t.Fatal(err)
	}
	storage := &fakeStore{catalog: store.Catalog{ID: id, Bundle: normalized, CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	app := application.New(storage, auth.DenyMutations{}, "http://localhost")
	handler, err := server.New(app, "01234567890123456789012345678901", server.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestActualResponsesMatchOpenAPI(t *testing.T) {
	handler := testHandler(t)
	for _, mediaType := range []string{"application/vnd.oai.openapi+json", "application/geo+json", "application/schema+json", "application/facets+json"} {
		openapi3filter.RegisterBodyDecoder(mediaType, openapi3filter.JSONBodyDecoder)
	}
	spec, err := api.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	router, err := legacy.NewRouter(spec)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		method, path, contentType, body string
		status                          int
	}{{"GET", "/", "", "", 200}, {"GET", "/api", "", "", 200}, {"GET", "/conformance", "", "", 200}, {"GET", "/collections?limit=1", "", "", 200}, {"GET", "/collections/records", "", "", 200}, {"GET", "/collections/records/items?limit=0&facets=", "", "", 200}, {"GET", "/collections/records/queryables", "", "", 200}, {"GET", "/collections/records/sortables", "", "", 200}, {"GET", "/collections/records/facets", "", "", 200}, {"GET", "/collections/records/schema?type=replace", "", "", 200}, {"GET", "/healthz", "", "", 200}, {"GET", "/readyz", "", "", 200}, {"POST", "/collections/records/items", "application/geo+json", `{"id":"ignored","type":"Feature","geometry":null,"properties":{"type":"dataset"}}`, 403}}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://localhost:8080"+test.path, bytes.NewBufferString(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
			route, pathParams, err := router.FindRoute(request)
			if err != nil {
				t.Fatal(err)
			}
			input := &openapi3filter.ResponseValidationInput{RequestValidationInput: &openapi3filter.RequestValidationInput{Request: request, PathParams: pathParams, Route: route}, Status: response.Code, Header: response.Header()}
			input.SetBodyBytes(response.Body.Bytes())
			if err := openapi3filter.ValidateResponse(t.Context(), input); err != nil {
				t.Fatalf("response does not match OpenAPI: %v\n%s", err, response.Body.String())
			}
		})
	}
}

func TestOptionsAndHead(t *testing.T) {
	handler := testHandler(t)
	for _, test := range []struct{ method, path, allow string }{{"HEAD", "/collections/records/items", ""}, {"HEAD", "/collections/records", ""}, {"OPTIONS", "/collections/records/items", "GET, HEAD, OPTIONS, POST"}, {"OPTIONS", "/collections/records/items/missing", "GET, HEAD, OPTIONS, PUT, PATCH, DELETE"}} {
		request := httptest.NewRequest(test.method, "http://localhost"+test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK && response.Code != http.StatusNoContent {
			t.Errorf("%s %s = %d", test.method, test.path, response.Code)
		}
		if test.allow != "" && response.Header().Get("Allow") != test.allow {
			t.Errorf("Allow = %q", response.Header().Get("Allow"))
		}
		if response.Body.Len() != 0 {
			t.Errorf("%s returned a body", test.method)
		}
	}
}

func TestConformanceDoesNotClaimHarvest(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://localhost/conformance", nil)
	response := httptest.NewRecorder()
	testHandler(t).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	var document struct {
		ConformsTo []string `json:"conformsTo"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(document.ConformsTo, "\n")
	for _, required := range []string{"ogcapi-records-1/1.0/conf/searchable-catalog", "ogcapi-records-2/1.0/conf/advanced", "ogcapi-records-3/1.0/conf/records", "ogcapi-features-4/1.0/conf/create-replace-delete", "ogcapi-features-4/1.0/conf/update"} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing conformance class %s", required)
		}
	}
	if strings.Contains(joined, "ogcapi-records-3/1.0/conf/harvest") {
		t.Error("Harvest conformance must remain unclaimed")
	}
}

var _ store.Store = (*fakeStore)(nil)
