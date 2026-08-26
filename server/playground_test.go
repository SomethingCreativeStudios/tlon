package server_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestPlaygroundIsEmbedded(t *testing.T) {
	handler := testHandler(t)
	for _, test := range []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/playground", contentType: "text/html", contains: "Tlon playground"},
		{path: "/playground/", contentType: "text/html", contains: "Search records"},
		{path: "/playground/app.js", contentType: "text/javascript", contains: "loadCatalogs"},
		{path: "/playground/styles.css", contentType: "text/css", contains: ".facet-card"},
	} {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://localhost"+test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); !strings.Contains(got, test.contentType) {
				t.Errorf("Content-Type = %q", got)
			}
			if !strings.Contains(response.Body.String(), test.contains) {
				t.Errorf("response does not contain %q", test.contains)
			}
			if response.Header().Get("Content-Security-Policy") == "" {
				t.Error("Content-Security-Policy is missing")
			}
			if got := response.Header().Get("Cache-Control"); got != "no-cache" {
				t.Errorf("Cache-Control = %q", got)
			}
		})
	}
}

func TestPlaygroundIsReadOnly(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://localhost/playground", strings.NewReader("ignored"))
	response := httptest.NewRecorder()
	testHandler(t).ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("Allow = %q", response.Header().Get("Allow"))
	}
}

func TestPlaygroundHeadUsesAssetContentType(t *testing.T) {
	for _, test := range []struct{ path, contentType string }{{"/playground", "text/html"}, {"/playground/app.js", "text/javascript"}, {"/playground/styles.css", "text/css"}} {
		request := httptest.NewRequest(http.MethodHead, "http://localhost"+test.path, nil)
		response := httptest.NewRecorder()
		testHandler(t).ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Errorf("HEAD %s status = %d", test.path, response.Code)
		}
		if got := response.Header().Get("Content-Type"); !strings.Contains(got, test.contentType) {
			t.Errorf("HEAD %s Content-Type = %q", test.path, got)
		}
		if response.Body.Len() != 0 {
			t.Errorf("HEAD %s returned a body", test.path)
		}
	}
}

func TestPlaygroundAssetReferencesResolve(t *testing.T) {
	handler := testHandler(t)
	request := httptest.NewRequest(http.MethodGet, "http://localhost/playground", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("playground status = %d", response.Code)
	}

	references := regexp.MustCompile(`(?:href|src)="(/playground/(?:styles\.css|app\.js)(?:\?v=[^"]+)?)"`).FindAllStringSubmatch(response.Body.String(), -1)
	if len(references) != 2 {
		t.Fatalf("found %d playground asset references in index", len(references))
	}
	for _, match := range references {
		assetRequest := httptest.NewRequest(http.MethodGet, "http://localhost"+match[1], nil)
		assetResponse := httptest.NewRecorder()
		handler.ServeHTTP(assetResponse, assetRequest)
		if assetResponse.Code != http.StatusOK {
			t.Errorf("asset %s status = %d", match[1], assetResponse.Code)
		}
	}
}

func TestPlaygroundIncludesQueryableCQLAutocomplete(t *testing.T) {
	handler := testHandler(t)
	request := httptest.NewRequest(http.MethodGet, "http://localhost/playground", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("playground status = %d", response.Code)
	}
	for _, marker := range []string{`id="query-filter"`, `role="combobox"`, `id="cql-suggestions"`, `role="listbox"`, "Ctrl+Space"} {
		if !strings.Contains(response.Body.String(), marker) {
			t.Errorf("playground does not contain %q", marker)
		}
	}

	request = httptest.NewRequest(http.MethodGet, "http://localhost/playground/app.js", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	for _, marker := range []string{"state.metadata.queryables", "literalSuggestions", "acceptCQLSuggestion", "aria-activedescendant", `requestURL.searchParams.set("facets", "")`, "state.savedFacets", `if (!paging) state.savedFacets = data.facets || {}`} {
		if !strings.Contains(response.Body.String(), marker) {
			t.Errorf("playground JavaScript does not contain %q", marker)
		}
	}
}
