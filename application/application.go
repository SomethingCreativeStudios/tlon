// Package application contains the reusable Tlon use-case layer.
package application

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/google/uuid"

	"github.com/SomethingCreativeStudios/tlon/auth"
	recorddoc "github.com/SomethingCreativeStudios/tlon/internal/record"
	"github.com/SomethingCreativeStudios/tlon/internal/recordschema"
	"github.com/SomethingCreativeStudios/tlon/store"
)

type Application struct {
	Store      store.Store
	Authorizer auth.Authorizer
	PublicURL  string
}

func New(storage store.Store, authorizer auth.Authorizer, publicURL string) *Application {
	return &Application{Store: storage, Authorizer: authorizer, PublicURL: strings.TrimRight(publicURL, "/")}
}

func (a *Application) Catalog(ctx context.Context, id string) (store.Catalog, error) {
	return a.Store.GetCatalog(ctx, id)
}
func (a *Application) Catalogs(ctx context.Context, search store.Search) (store.CatalogSearchResult, error) {
	return a.Store.ListCatalogs(ctx, search)
}
func (a *Application) Search(ctx context.Context, catalogID string, search store.Search) (store.SearchResult, error) {
	result, err := a.Store.SearchRecords(ctx, catalogID, search)
	if err != nil {
		return result, err
	}
	for i := range result.Records {
		result.Records[i], err = a.materialize(result.Records[i], catalogID)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func (a *Application) Record(ctx context.Context, catalogID, id string) (store.StoredRecord, error) {
	stored, err := a.Store.GetRecord(ctx, catalogID, id)
	if err != nil {
		return stored, err
	}
	return a.materialize(stored, catalogID)
}

func (a *Application) Create(ctx context.Context, catalogID string, raw json.RawMessage, request *http.Request) (store.StoredRecord, error) {
	if err := a.authorize(ctx, auth.OperationCreate, catalogID, "", request); err != nil {
		return store.StoredRecord{}, err
	}
	doc, err := recorddoc.Decode(raw)
	if err != nil {
		return store.StoredRecord{}, err
	}
	recorddoc.ForCreate(doc)
	raw, err = recorddoc.Encode(doc)
	if err != nil {
		return store.StoredRecord{}, err
	}
	if err := a.validateRecord(ctx, catalogID, raw); err != nil {
		return store.StoredRecord{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return store.StoredRecord{}, fmt.Errorf("generate UUIDv7: %w", err)
	}
	stored, err := a.Store.CreateRecord(ctx, catalogID, id.String(), raw)
	if err != nil {
		return stored, err
	}
	return a.materialize(stored, catalogID)
}

func (a *Application) Put(ctx context.Context, catalogID, id string, raw json.RawMessage, expected *int64, request *http.Request) (store.PutResult, error) {
	if err := a.authorize(ctx, auth.OperationReplace, catalogID, id, request); err != nil {
		return store.PutResult{}, err
	}
	doc, err := recorddoc.Decode(raw)
	if err != nil {
		return store.PutResult{}, err
	}
	if err := recorddoc.ForPut(doc, id); err != nil {
		return store.PutResult{}, err
	}
	raw, err = recorddoc.Encode(doc)
	if err != nil {
		return store.PutResult{}, err
	}
	if err := a.validateRecord(ctx, catalogID, raw); err != nil {
		return store.PutResult{}, err
	}
	result, err := a.Store.PutRecord(ctx, catalogID, id, raw, expected)
	if err != nil {
		return result, err
	}
	result.Record, err = a.materialize(result.Record, catalogID)
	return result, err
}

func (a *Application) Patch(ctx context.Context, catalogID, id string, patch json.RawMessage, expected *int64, request *http.Request) (store.StoredRecord, error) {
	if err := a.authorize(ctx, auth.OperationPatch, catalogID, id, request); err != nil {
		return store.StoredRecord{}, err
	}
	current, err := a.Store.GetRecord(ctx, catalogID, id)
	if err != nil {
		return store.StoredRecord{}, err
	}
	if expected != nil && *expected >= 0 && current.Version != *expected {
		return store.StoredRecord{}, store.ErrPrecondition
	}
	clean, err := recorddoc.SanitizePatch(patch)
	if err != nil {
		return store.StoredRecord{}, err
	}
	merged, err := jsonpatch.MergePatch(current.Document, clean)
	if err != nil {
		return store.StoredRecord{}, fmt.Errorf("merge patch: %w", err)
	}
	doc, err := recorddoc.Decode(merged)
	if err != nil {
		return store.StoredRecord{}, err
	}
	if err := recorddoc.ForPut(doc, id); err != nil {
		return store.StoredRecord{}, err
	}
	merged, err = recorddoc.Encode(doc)
	if err != nil {
		return store.StoredRecord{}, err
	}
	if err := a.validateRecord(ctx, catalogID, merged); err != nil {
		return store.StoredRecord{}, err
	}
	// A merge patch is a read-modify-write operation. Pin the update to the
	// version read above even when the caller chose an unconditional request,
	// preventing a concurrent mutation from being silently overwritten.
	condition := current.Version
	result, err := a.Store.PutRecord(ctx, catalogID, id, merged, &condition)
	if err != nil {
		return store.StoredRecord{}, err
	}
	return a.materialize(result.Record, catalogID)
}

func (a *Application) Delete(ctx context.Context, catalogID, id string, expected *int64, request *http.Request) error {
	if err := a.authorize(ctx, auth.OperationDelete, catalogID, id, request); err != nil {
		return err
	}
	return a.Store.DeleteRecord(ctx, catalogID, id, expected)
}

func (a *Application) materialize(stored store.StoredRecord, catalogID string) (store.StoredRecord, error) {
	raw, err := recorddoc.Materialize(stored.Document, a.PublicURL, catalogID, stored.ID, stored.CreatedAt, stored.UpdatedAt)
	if err != nil {
		return stored, err
	}
	stored.Document = raw
	return stored, nil
}
func (a *Application) authorize(ctx context.Context, operation auth.Operation, catalogID, id string, request *http.Request) error {
	if a.Authorizer == nil {
		return auth.ErrForbidden
	}
	return a.Authorizer.Authorize(ctx, auth.Action{Operation: operation, CatalogID: catalogID, RecordID: id, Request: request})
}

func (a *Application) validateRecord(ctx context.Context, catalogID string, raw json.RawMessage) error {
	catalog, err := a.Store.GetCatalog(ctx, catalogID)
	if err != nil {
		return err
	}
	if err := recordschema.ValidateBundle(catalog.Bundle, raw); err != nil {
		return fmt.Errorf("%w: %v", recorddoc.ErrInvalid, err)
	}
	return nil
}
