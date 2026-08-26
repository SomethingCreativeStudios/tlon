package server

import (
	"context"
	"encoding/json"

	"github.com/SomethingCreativeStudios/tlon/api"
)

func (s *Service) GetCollections(ctx context.Context, request api.GetCollectionsRequestObject) (api.GetCollectionsResponseObject, error) {
	search, scope, err := s.catalogSearch(request.Params)
	if err != nil {
		return s.collectionsProblem(ctx, err), nil
	}
	result, err := s.app.Catalogs(ctx, search)
	if err != nil {
		return s.collectionsProblem(ctx, err), nil
	}
	models := make([]api.Catalog, 0, len(result.Catalogs))
	for _, value := range result.Catalogs {
		model, err := s.catalogModel(value)
		if err != nil {
			return s.collectionsProblem(ctx, err), nil
		}
		models = append(models, model)
	}
	var first, last []any
	if len(result.SortValues) > 0 {
		first = result.SortValues[0]
		last = result.SortValues[len(result.SortValues)-1]
	}
	requestURL := requestFrom(ctx).URL
	links := s.links(requestURL, scope, "$catalogs", search, first, last, result.HasPrev, result.HasNext, "application/json")
	return api.GetCollections200JSONResponse(api.CatalogCollection{Collections: models, Links: links, NumberMatched: result.NumberMatched, NumberReturned: len(models)}), nil
}
func (s *Service) collectionsProblem(ctx context.Context, err error) api.GetCollectionsResponseObject {
	problem, status := s.problem(ctx, err)
	return api.GetCollectionsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}
}
func (s *Service) HeadCollections(context.Context, api.HeadCollectionsRequestObject) (api.HeadCollectionsResponseObject, error) {
	return api.HeadCollections200Response{}, nil
}
func (s *Service) OptionsCollections(context.Context, api.OptionsCollectionsRequestObject) (api.OptionsCollectionsResponseObject, error) {
	return api.OptionsCollections204Response{Headers: api.OptionsResponseHeaders{Allow: "GET, HEAD, OPTIONS"}}, nil
}

func (s *Service) GetCatalog(ctx context.Context, request api.GetCatalogRequestObject) (api.GetCatalogResponseObject, error) {
	value, err := s.app.Catalog(ctx, request.CatalogId)
	if err != nil {
		problem, status := s.problem(ctx, err)
		return api.GetCatalogdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	model, err := s.catalogModel(value)
	if err != nil {
		problem, status := s.problem(ctx, err)
		return api.GetCatalogdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	return api.GetCatalog200JSONResponse(model), nil
}
func (s *Service) HeadCatalog(ctx context.Context, request api.HeadCatalogRequestObject) (api.HeadCatalogResponseObject, error) {
	if _, err := s.app.Catalog(ctx, request.CatalogId); err != nil {
		problem, status := s.problem(ctx, err)
		return api.HeadCatalogdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	return api.HeadCatalog200Response{}, nil
}
func (s *Service) OptionsCatalog(context.Context, api.OptionsCatalogRequestObject) (api.OptionsCatalogResponseObject, error) {
	return api.OptionsCatalog204Response{Headers: api.OptionsResponseHeaders{Allow: "GET, HEAD, OPTIONS"}}, nil
}

func (s *Service) GetQueryables(ctx context.Context, request api.GetQueryablesRequestObject) (api.GetQueryablesResponseObject, error) {
	value, err := s.app.Catalog(ctx, request.CatalogId)
	if err != nil {
		problem, status := s.problem(ctx, err)
		return api.GetQueryablesdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	id := s.publicURL + "/collections/" + request.CatalogId + "/queryables"
	return api.GetQueryables200ApplicationSchemaPlusJSONResponse(api.Queryables{Schema: "https://json-schema.org/draft/2020-12/schema", Id: &id, Type: api.QueryablesTypeObject, Title: "Queryable properties for " + request.CatalogId, Properties: schemaProperties(value.Bundle.Queryables, value.Bundle.Facets)}), nil
}
func (s *Service) HeadQueryables(ctx context.Context, request api.HeadQueryablesRequestObject) (api.HeadQueryablesResponseObject, error) {
	if _, err := s.app.Catalog(ctx, request.CatalogId); err != nil {
		problem, status := s.problem(ctx, err)
		return api.HeadQueryablesdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	return api.HeadQueryables200Response{}, nil
}
func (s *Service) OptionsQueryables(context.Context, api.OptionsQueryablesRequestObject) (api.OptionsQueryablesResponseObject, error) {
	return api.OptionsQueryables204Response{Headers: api.OptionsResponseHeaders{Allow: "GET, HEAD, OPTIONS"}}, nil
}

func (s *Service) GetSortables(ctx context.Context, request api.GetSortablesRequestObject) (api.GetSortablesResponseObject, error) {
	value, err := s.app.Catalog(ctx, request.CatalogId)
	if err != nil {
		problem, status := s.problem(ctx, err)
		return api.GetSortablesdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	id := s.publicURL + "/collections/" + request.CatalogId + "/sortables"
	return api.GetSortables200ApplicationSchemaPlusJSONResponse(api.Sortables{Schema: "https://json-schema.org/draft/2020-12/schema", Id: &id, Type: api.SortablesTypeObject, Title: "Sortable properties for " + request.CatalogId, Properties: schemaProperties(value.Bundle.Sortables, nil)}), nil
}
func (s *Service) HeadSortables(ctx context.Context, request api.HeadSortablesRequestObject) (api.HeadSortablesResponseObject, error) {
	if _, err := s.app.Catalog(ctx, request.CatalogId); err != nil {
		problem, status := s.problem(ctx, err)
		return api.HeadSortablesdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	return api.HeadSortables200Response{}, nil
}
func (s *Service) OptionsSortables(context.Context, api.OptionsSortablesRequestObject) (api.OptionsSortablesResponseObject, error) {
	return api.OptionsSortables204Response{Headers: api.OptionsResponseHeaders{Allow: "GET, HEAD, OPTIONS"}}, nil
}

func (s *Service) GetFacets(ctx context.Context, request api.GetFacetsRequestObject) (api.GetFacetsResponseObject, error) {
	value, err := s.app.Catalog(ctx, request.CatalogId)
	if err != nil {
		problem, status := s.problem(ctx, err)
		return api.GetFacetsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	definitions := map[string]any{}
	defaultCount := 10
	for name, definition := range value.Bundle.Facets {
		raw, _ := json.Marshal(definition)
		var item map[string]any
		_ = json.Unmarshal(raw, &item)
		delete(item, "default")
		definitions[name] = item
	}
	title := request.CatalogId
	if model, err := s.catalogModel(value); err == nil && model.Title != nil {
		title = *model.Title
	}
	links := []api.Link{{Href: s.publicURL + "/collections/" + request.CatalogId + "/facets", Rel: "self"}}
	return api.GetFacets200ApplicationFacetsPlusJSONResponse(api.Facets{Facets: definitions, Links: &links, Id: &request.CatalogId, Title: &title, DefaultBucketCount: &defaultCount}), nil
}
func (s *Service) HeadFacets(ctx context.Context, request api.HeadFacetsRequestObject) (api.HeadFacetsResponseObject, error) {
	if _, err := s.app.Catalog(ctx, request.CatalogId); err != nil {
		problem, status := s.problem(ctx, err)
		return api.HeadFacetsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	return api.HeadFacets200Response{}, nil
}
func (s *Service) OptionsFacets(context.Context, api.OptionsFacetsRequestObject) (api.OptionsFacetsResponseObject, error) {
	return api.OptionsFacets204Response{Headers: api.OptionsResponseHeaders{Allow: "GET, HEAD, OPTIONS"}}, nil
}

func (s *Service) GetRecordSchema(ctx context.Context, request api.GetRecordSchemaRequestObject) (api.GetRecordSchemaResponseObject, error) {
	value, err := s.app.Catalog(ctx, request.CatalogId)
	if err != nil {
		problem, status := s.problem(ctx, err)
		return api.GetRecordSchemadefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	var schema map[string]any
	if err := json.Unmarshal(value.Bundle.Schema, &schema); err != nil {
		problem, status := s.problem(ctx, err)
		return api.GetRecordSchemadefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	return api.GetRecordSchema200ApplicationSchemaPlusJSONResponse(schema), nil
}
func (s *Service) HeadRecordSchema(ctx context.Context, request api.HeadRecordSchemaRequestObject) (api.HeadRecordSchemaResponseObject, error) {
	if _, err := s.app.Catalog(ctx, request.CatalogId); err != nil {
		problem, status := s.problem(ctx, err)
		return api.HeadRecordSchemadefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	return api.HeadRecordSchema200Response{}, nil
}
func (s *Service) OptionsRecordSchema(context.Context, api.OptionsRecordSchemaRequestObject) (api.OptionsRecordSchemaResponseObject, error) {
	return api.OptionsRecordSchema204Response{Headers: api.OptionsResponseHeaders{Allow: "GET, HEAD, OPTIONS"}}, nil
}

func (s *Service) OptionsRecords(context.Context, api.OptionsRecordsRequestObject) (api.OptionsRecordsResponseObject, error) {
	return api.OptionsRecords204Response{Headers: api.OptionsResponseHeaders{Allow: "GET, HEAD, OPTIONS, POST"}}, nil
}
func (s *Service) OptionsRecord(context.Context, api.OptionsRecordRequestObject) (api.OptionsRecordResponseObject, error) {
	return api.OptionsRecord204Response{Headers: api.OptionsResponseHeaders{Allow: "GET, HEAD, OPTIONS, PUT, PATCH, DELETE"}}, nil
}
