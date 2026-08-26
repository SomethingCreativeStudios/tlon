// Package store defines Tlon's reusable persistence contract and domain data.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrConflict        = errors.New("conflict")
	ErrPrecondition    = errors.New("precondition failed")
	ErrCatalogNotEmpty = errors.New("catalog is not empty")
)

// Queryable describes a CQL2-visible field. Path is an RFC 6901 JSON pointer
// into the stored record. The implementation converts only validated paths to
// SQL; user input is never used as a SQL identifier.
type Queryable struct {
	Title       string     `json:"title,omitempty"`
	Description string     `json:"description,omitempty"`
	Type        string     `json:"type"`
	Format      string     `json:"format,omitempty"`
	Path        string     `json:"x-tlon-path,omitempty"`
	Items       *Queryable `json:"items,omitempty"`
}

type Sortable struct {
	Title  string `json:"title,omitempty"`
	Type   string `json:"type"`
	Format string `json:"format,omitempty"`
	Path   string `json:"x-tlon-path,omitempty"`
}

type SortField struct {
	Property  string `json:"property"`
	Direction string `json:"direction,omitempty"`
}

const (
	StorageTransactional = "transactional"
	StorageTemporal      = "temporal"

	FacetTerm      = "term"
	FacetHistogram = "histogram"
	FacetFilter    = "filter"

	BucketFixedInterval = "fixedInterval"
	BucketFixedCount    = "fixedBucketCount"
)

// CatalogStorage selects the physical persistence strategy for every record
// in a catalog. Transactional storage accepts the complete Records model.
// Temporal storage requires a record time with a closed start and uses that
// start as an immutable TimescaleDB partition key.
type CatalogStorage struct {
	Class            string         `json:"class"`
	AutoFacetIndexes *bool          `json:"autoFacetIndexes,omitempty"`
	Indexes          []CatalogIndex `json:"indexes,omitempty"`
}

// CatalogIndex describes a workload-specific B-tree index. Properties refer
// only to validated catalog queryables; datastore implementations own the SQL
// representation and never accept SQL in a catalog bundle.
type CatalogIndex struct {
	Name string     `json:"name"`
	Keys []IndexKey `json:"keys"`
}

type IndexKey struct {
	Property  string `json:"property"`
	Direction string `json:"direction,omitempty"`
}

type FacetDefinition struct {
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type"`
	Property    string            `json:"property,omitempty"`
	Default     bool              `json:"default,omitempty"`
	BucketCount int               `json:"bucketCount,omitempty"`
	SortedBy    string            `json:"sortedBy,omitempty"`
	MinOccurs   int64             `json:"minOccurs,omitempty"`
	BucketType  string            `json:"bucketType,omitempty"`
	Interval    json.RawMessage   `json:"interval,omitempty"`
	Filters     map[string]string `json:"filters,omitempty"`
}

// CatalogBundle is the atomic catalog-management document accepted by
// `tlon catalog apply`.
type CatalogBundle struct {
	Catalog          json.RawMessage            `json:"catalog"`
	Storage          CatalogStorage             `json:"storage"`
	Queryables       map[string]Queryable       `json:"queryables"`
	Sortables        map[string]Sortable        `json:"sortables"`
	DefaultSortOrder []SortField                `json:"defaultSortOrder"`
	Facets           map[string]FacetDefinition `json:"facets"`
	Schema           json.RawMessage            `json:"schema,omitempty"`
}

type Catalog struct {
	ID        string
	Bundle    CatalogBundle
	CreatedAt time.Time
	UpdatedAt time.Time
}

type CursorDirection string

const (
	CursorNext CursorDirection = "next"
	CursorPrev CursorDirection = "prev"
)

type CursorPosition struct {
	Direction CursorDirection
	Values    []any
}

type Search struct {
	Bbox        []float64
	Datetime    string
	Q           []string
	Types       []string
	IDs         []string
	ExternalIDs []string
	Limit       int
	Sort        []SortField
	Position    *CursorPosition
	Filter      string
	Facets      *string
}

type StoredRecord struct {
	ID         string
	Document   json.RawMessage
	Version    int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
	SortValues []any
}

// RecordInput is a client-identified record used by trusted bulk-ingest tools.
// Implementations must apply the same validation and managed-field rules as a
// normal client-ID PUT.
type RecordInput struct {
	ID       string
	Document json.RawMessage
}

type FacetBucket struct {
	Value any   `json:"value,omitempty"`
	Min   any   `json:"min,omitempty"`
	Max   any   `json:"max,omitempty"`
	Count int64 `json:"count"`
}

type FacetResult struct {
	Type     string        `json:"type"`
	Property string        `json:"property"`
	Buckets  []FacetBucket `json:"buckets"`
	More     *bool         `json:"more,omitempty"`
}

type SearchResult struct {
	Records       []StoredRecord
	NumberMatched int64
	Facets        map[string]FacetResult
	HasNext       bool
	HasPrev       bool
}

type CatalogSearchResult struct {
	Catalogs      []Catalog
	NumberMatched int64
	HasNext       bool
	HasPrev       bool
	SortValues    [][]any
}

type PutResult struct {
	Record  StoredRecord
	Created bool
}

// Store is deliberately dependency-free so applications can supply their own
// persistence implementation.
type Store interface {
	Ping(context.Context) error
	Close()

	ApplyCatalog(context.Context, CatalogBundle) (Catalog, error)
	GetCatalog(context.Context, string) (Catalog, error)
	ListCatalogs(context.Context, Search) (CatalogSearchResult, error)
	DeleteCatalog(context.Context, string, bool) error

	SearchRecords(context.Context, string, Search) (SearchResult, error)
	GetRecord(context.Context, string, string) (StoredRecord, error)
	CreateRecord(context.Context, string, string, json.RawMessage) (StoredRecord, error)
	PutRecord(context.Context, string, string, json.RawMessage, *int64) (PutResult, error)
	PatchRecord(context.Context, string, string, json.RawMessage, *int64) (StoredRecord, error)
	DeleteRecord(context.Context, string, string, *int64) error
}
