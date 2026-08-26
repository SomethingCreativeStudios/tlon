package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/SomethingCreativeStudios/tlon/api"
)

var conformanceClasses = []string{
	"http://www.opengis.net/spec/ogcapi-features-1/1.0/conf/core",
	"http://www.opengis.net/spec/ogcapi-features-1/1.0/conf/oas30",
	"http://www.opengis.net/spec/ogcapi-features-1/1.0/conf/geojson",
	"http://www.opengis.net/spec/ogcapi-features-4/1.0/conf/features",
	"http://www.opengis.net/spec/ogcapi-features-4/1.0/conf/create-replace-delete",
	"http://www.opengis.net/spec/ogcapi-features-4/1.0/conf/update",
	"http://www.opengis.net/spec/ogcapi-records-1/1.0/conf/record-core",
	"http://www.opengis.net/spec/ogcapi-records-1/1.0/conf/record-collection",
	"http://www.opengis.net/spec/ogcapi-records-1/1.0/conf/record-core-query-parameters",
	"http://www.opengis.net/spec/ogcapi-records-1/1.0/conf/record-api",
	"http://www.opengis.net/spec/ogcapi-records-1/1.0/conf/searchable-catalog",
	"http://www.opengis.net/spec/ogcapi-records-1/1.0/conf/searchable-catalog-filtering",
	"http://www.opengis.net/spec/ogcapi-records-1/1.0/conf/searchable-catalog-sorting",
	"http://www.opengis.net/spec/ogcapi-records-1/1.0/conf/local-resources-catalog",
	"http://www.opengis.net/spec/ogcapi-records-1/1.0/conf/local-resources-catalog-query-parameters",
	"http://www.opengis.net/spec/ogcapi-records-1/1.0/conf/local-resources-catalog-filtering",
	"http://www.opengis.net/spec/ogcapi-records-1/1.0/conf/local-resources-catalog-sorting",
	"http://www.opengis.net/spec/ogcapi-records-1/1.0/conf/sorting",
	"http://www.opengis.net/spec/ogcapi-records-1/1.0/conf/filtering",
	"http://www.opengis.net/spec/ogcapi-records-1/1.0/conf/json",
	"http://www.opengis.net/spec/ogcapi-records-1/1.0/conf/oas30",
	"http://www.opengis.net/spec/ogcapi-records-1/1.0/conf/autodiscovery",
	"http://www.opengis.net/spec/cql2/1.0/conf/cql2-text",
	"http://www.opengis.net/spec/cql2/1.0/conf/basic-cql2",
	"http://www.opengis.net/spec/ogcapi-records-2/1.0/conf/simple",
	"http://www.opengis.net/spec/ogcapi-records-2/1.0/conf/advanced",
	"http://www.opengis.net/spec/ogcapi-records-3/1.0/conf/records",
}

func (s *Service) GetLandingPage(context.Context, api.GetLandingPageRequestObject) (api.GetLandingPageResponseObject, error) {
	description := "OGC API Records with facets and record transactions"
	jsonType := "application/json"
	openapiType := "application/vnd.oai.openapi+json;version=3.0"
	htmlType := "text/html"
	playgroundTitle := "Tlon playground"
	return api.GetLandingPage200JSONResponse(api.LandingPage{Title: "Tlon", Description: &description, Links: []api.Link{{Href: s.publicURL + "/", Rel: "self", Type: &jsonType}, {Href: s.publicURL + "/api", Rel: "service-desc", Type: &openapiType}, {Href: s.publicURL + "/conformance", Rel: "conformance", Type: &jsonType}, {Href: s.publicURL + "/collections", Rel: "data", Type: &jsonType}, {Href: s.publicURL + "/playground", Rel: "alternate", Type: &htmlType, Title: &playgroundTitle}}}), nil
}
func (s *Service) HeadLandingPage(context.Context, api.HeadLandingPageRequestObject) (api.HeadLandingPageResponseObject, error) {
	return api.HeadLandingPage200Response{}, nil
}
func (s *Service) OptionsLandingPage(context.Context, api.OptionsLandingPageRequestObject) (api.OptionsLandingPageResponseObject, error) {
	return api.OptionsLandingPage204Response{Headers: api.OptionsResponseHeaders{Allow: "GET, HEAD, OPTIONS"}}, nil
}

func (s *Service) GetAPI(ctx context.Context, _ api.GetAPIRequestObject) (api.GetAPIResponseObject, error) {
	spec, err := api.GetSwagger()
	if err != nil {
		problem, status := s.problem(ctx, err)
		return api.GetAPIdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		problem, status := s.problem(ctx, err)
		return api.GetAPIdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		problem, status := s.problem(ctx, err)
		return api.GetAPIdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	document["servers"] = []any{map[string]any{"url": s.publicURL}}
	return api.GetAPI200ApplicationVndOaiOpenapiPlusJSONVersion30Response(document), nil
}
func (s *Service) HeadAPI(context.Context, api.HeadAPIRequestObject) (api.HeadAPIResponseObject, error) {
	return api.HeadAPI200Response{}, nil
}
func (s *Service) OptionsAPI(context.Context, api.OptionsAPIRequestObject) (api.OptionsAPIResponseObject, error) {
	return api.OptionsAPI204Response{Headers: api.OptionsResponseHeaders{Allow: "GET, HEAD, OPTIONS"}}, nil
}

func (s *Service) GetConformance(context.Context, api.GetConformanceRequestObject) (api.GetConformanceResponseObject, error) {
	return api.GetConformance200JSONResponse(api.Conformance{ConformsTo: append([]string(nil), conformanceClasses...)}), nil
}
func (s *Service) HeadConformance(context.Context, api.HeadConformanceRequestObject) (api.HeadConformanceResponseObject, error) {
	return api.HeadConformance200Response{}, nil
}
func (s *Service) OptionsConformance(context.Context, api.OptionsConformanceRequestObject) (api.OptionsConformanceResponseObject, error) {
	return api.OptionsConformance204Response{Headers: api.OptionsResponseHeaders{Allow: "GET, HEAD, OPTIONS"}}, nil
}

func (s *Service) Health(context.Context, api.HealthRequestObject) (api.HealthResponseObject, error) {
	return api.Health200JSONResponse(api.Health{Status: "ok"}), nil
}
func (s *Service) Ready(ctx context.Context, _ api.ReadyRequestObject) (api.ReadyResponseObject, error) {
	if err := s.app.Store.Ping(ctx); err != nil {
		problem, _ := s.problem(ctx, err)
		problem.Status = http.StatusServiceUnavailable
		problem.Title = "Service Unavailable"
		problem.Code = "NotReady"
		return api.Ready503ApplicationProblemPlusJSONResponse(problem), nil
	}
	return api.Ready200JSONResponse(api.Health{Status: "ready"}), nil
}
