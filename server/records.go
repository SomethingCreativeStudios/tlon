package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/SomethingCreativeStudios/tlon/api"
	"github.com/SomethingCreativeStudios/tlon/internal/etag"
	"github.com/SomethingCreativeStudios/tlon/store"
)

func (s *Service) SearchRecords(ctx context.Context, request api.SearchRecordsRequestObject) (api.SearchRecordsResponseObject, error) {
	catalogValue, err := s.app.Catalog(ctx, request.CatalogId)
	if err != nil {
		return s.searchProblem(ctx, err), nil
	}
	search, scope, err := s.recordSearch(catalogValue, request.Params)
	if err != nil {
		return s.searchProblem(ctx, err), nil
	}
	result, err := s.app.Search(ctx, request.CatalogId, search)
	if err != nil {
		return s.searchProblem(ctx, err), nil
	}
	features := make([]api.Record, 0, len(result.Records))
	for _, stored := range result.Records {
		model, err := s.recordModel(stored)
		if err != nil {
			return s.searchProblem(ctx, err), nil
		}
		features = append(features, model)
	}
	var first, last []any
	if len(result.Records) > 0 {
		first = result.Records[0].SortValues
		last = result.Records[len(result.Records)-1].SortValues
	}
	links := s.links(requestFrom(ctx).URL, scope, request.CatalogId, search, first, last, result.HasPrev, result.HasNext, "application/geo+json")
	now := time.Now().UTC()
	response := api.RecordCollection{Type: api.FeatureCollection, Features: features, Links: links, NumberMatched: result.NumberMatched, NumberReturned: len(features), TimeStamp: &now}
	if result.Facets != nil {
		facets := map[string]api.FacetResult{}
		for name, value := range result.Facets {
			buckets := make([]api.FacetBucket, len(value.Buckets))
			for i, bucket := range value.Buckets {
				buckets[i] = api.FacetBucket{Value: bucket.Value, Min: bucket.Min, Max: bucket.Max, Count: bucket.Count}
			}
			facets[name] = api.FacetResult{Type: api.FacetResultType(value.Type), Property: value.Property, Buckets: buckets, More: value.More}
		}
		response.Facets = &facets
	}
	return api.SearchRecords200ApplicationGeoPlusJSONResponse(response), nil
}
func (s *Service) searchProblem(ctx context.Context, err error) api.SearchRecordsResponseObject {
	problem, status := s.problem(ctx, err)
	return api.SearchRecordsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}
}
func (s *Service) HeadRecords(ctx context.Context, request api.HeadRecordsRequestObject) (api.HeadRecordsResponseObject, error) {
	if _, err := s.app.Catalog(ctx, request.CatalogId); err != nil {
		problem, status := s.problem(ctx, err)
		return api.HeadRecordsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	return api.HeadRecords200Response{}, nil
}

func (s *Service) GetRecord(ctx context.Context, request api.GetRecordRequestObject) (api.GetRecordResponseObject, error) {
	stored, err := s.app.Record(ctx, request.CatalogId, request.RecordId)
	if err != nil {
		problem, status := s.problem(ctx, err)
		return api.GetRecorddefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	model, err := s.recordModel(stored)
	if err != nil {
		problem, status := s.problem(ctx, err)
		return api.GetRecorddefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	return api.GetRecord200ApplicationGeoPlusJSONResponse{Body: model, Headers: api.GetRecord200ResponseHeaders{ETag: etag.FromVersion(stored.Version)}}, nil
}
func (s *Service) HeadRecord(ctx context.Context, request api.HeadRecordRequestObject) (api.HeadRecordResponseObject, error) {
	stored, err := s.app.Record(ctx, request.CatalogId, request.RecordId)
	if err != nil {
		problem, status := s.problem(ctx, err)
		return api.HeadRecorddefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	return api.HeadRecord200Response{Headers: api.HeadRecord200ResponseHeaders{ETag: etag.FromVersion(stored.Version)}}, nil
}

func (s *Service) CreateRecord(ctx context.Context, request api.CreateRecordRequestObject) (api.CreateRecordResponseObject, error) {
	raw, err := createBody(request)
	if err != nil {
		return s.createProblem(ctx, err), nil
	}
	stored, err := s.app.Create(ctx, request.CatalogId, raw, requestFrom(ctx))
	if err != nil {
		return s.createProblem(ctx, err), nil
	}
	model, err := s.recordModel(stored)
	if err != nil {
		return s.createProblem(ctx, err), nil
	}
	return api.CreateRecord201ApplicationGeoPlusJSONResponse{Body: model, Headers: api.CreateRecord201ResponseHeaders{ETag: etag.FromVersion(stored.Version), Location: location(s.publicURL, request.CatalogId, stored.ID)}}, nil
}
func createBody(request api.CreateRecordRequestObject) (json.RawMessage, error) {
	if request.ApplicationGeoPlusJSONBody != nil {
		return json.Marshal(request.ApplicationGeoPlusJSONBody)
	}
	if request.JSONBody != nil {
		return json.Marshal(request.JSONBody)
	}
	return nil, store.ErrNotFound
}
func (s *Service) createProblem(ctx context.Context, err error) api.CreateRecordResponseObject {
	problem, status := s.problem(ctx, err)
	return api.CreateRecorddefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}
}

func (s *Service) PutRecord(ctx context.Context, request api.PutRecordRequestObject) (api.PutRecordResponseObject, error) {
	expected, err := expectedVersion((*string)(request.Params.IfMatch))
	if err != nil {
		return s.putProblem(ctx, err), nil
	}
	raw, err := putBody(request)
	if err != nil {
		return s.putProblem(ctx, err), nil
	}
	result, err := s.app.Put(ctx, request.CatalogId, request.RecordId, raw, expected, requestFrom(ctx))
	if err != nil {
		return s.putProblem(ctx, err), nil
	}
	if result.Created {
		model, err := s.recordModel(result.Record)
		if err != nil {
			return s.putProblem(ctx, err), nil
		}
		return api.PutRecord201ApplicationGeoPlusJSONResponse{Body: model, Headers: api.PutRecord201ResponseHeaders{ETag: etag.FromVersion(result.Record.Version), Location: location(s.publicURL, request.CatalogId, request.RecordId)}}, nil
	}
	return api.PutRecord204Response{Headers: api.PutRecord204ResponseHeaders{ETag: etag.FromVersion(result.Record.Version)}}, nil
}
func putBody(request api.PutRecordRequestObject) (json.RawMessage, error) {
	if request.ApplicationGeoPlusJSONBody != nil {
		return json.Marshal(request.ApplicationGeoPlusJSONBody)
	}
	if request.JSONBody != nil {
		return json.Marshal(request.JSONBody)
	}
	return nil, store.ErrNotFound
}
func (s *Service) putProblem(ctx context.Context, err error) api.PutRecordResponseObject {
	problem, status := s.problem(ctx, err)
	return api.PutRecorddefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}
}

func (s *Service) PatchRecord(ctx context.Context, request api.PatchRecordRequestObject) (api.PatchRecordResponseObject, error) {
	expected, err := expectedVersion((*string)(request.Params.IfMatch))
	if err != nil {
		return s.patchProblem(ctx, err), nil
	}
	if request.Body == nil {
		return s.patchProblem(ctx, store.ErrNotFound), nil
	}
	raw, err := json.Marshal(request.Body)
	if err != nil {
		return s.patchProblem(ctx, err), nil
	}
	stored, err := s.app.Patch(ctx, request.CatalogId, request.RecordId, raw, expected, requestFrom(ctx))
	if err != nil {
		return s.patchProblem(ctx, err), nil
	}
	return api.PatchRecord204Response{Headers: api.PatchRecord204ResponseHeaders{ETag: etag.FromVersion(stored.Version)}}, nil
}
func (s *Service) patchProblem(ctx context.Context, err error) api.PatchRecordResponseObject {
	problem, status := s.problem(ctx, err)
	return api.PatchRecorddefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}
}

func (s *Service) DeleteRecord(ctx context.Context, request api.DeleteRecordRequestObject) (api.DeleteRecordResponseObject, error) {
	expected, err := expectedVersion((*string)(request.Params.IfMatch))
	if err != nil {
		return s.deleteProblem(ctx, err), nil
	}
	if err := s.app.Delete(ctx, request.CatalogId, request.RecordId, expected, requestFrom(ctx)); err != nil {
		return s.deleteProblem(ctx, err), nil
	}
	return api.DeleteRecord204Response{}, nil
}
func (s *Service) deleteProblem(ctx context.Context, err error) api.DeleteRecordResponseObject {
	problem, status := s.problem(ctx, err)
	return api.DeleteRecorddefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}
}
