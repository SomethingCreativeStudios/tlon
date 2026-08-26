// Package postgres implements Tlon's store contract with PostgreSQL, PostGIS,
// and TimescaleDB.
package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SomethingCreativeStudios/tlon/catalog"
	recorddoc "github.com/SomethingCreativeStudios/tlon/internal/record"
	"github.com/SomethingCreativeStudios/tlon/internal/recordschema"
	storeapi "github.com/SomethingCreativeStudios/tlon/store"
)

type Store struct{ pool *pgxpool.Pool }

type recordRelation string

const (
	recordsTransactional recordRelation = "records_transactional"
	recordsTemporal      recordRelation = "records_temporal"
)

func relationFor(storageClass string) (recordRelation, error) {
	switch storageClass {
	case storeapi.StorageTransactional:
		return recordsTransactional, nil
	case storeapi.StorageTemporal:
		return recordsTemporal, nil
	default:
		return "", fmt.Errorf("unknown catalog storage class %q", storageClass)
	}
}

type Options struct {
	MaxConnections        int32
	MinConnections        int32
	MaxConnectionLifetime time.Duration
}

func Open(ctx context.Context, databaseURL string, options Options) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	if options.MaxConnections > 0 {
		config.MaxConns = options.MaxConnections
	}
	if options.MinConnections > 0 {
		config.MinConns = options.MinConnections
	}
	if options.MaxConnectionLifetime > 0 {
		config.MaxConnLifetime = options.MaxConnectionLifetime
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (p *Store) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }
func (p *Store) Close()                         { p.pool.Close() }

// DatabaseVersions reports the active PostgreSQL, PostGIS, and TimescaleDB versions.
func (p *Store) DatabaseVersions(ctx context.Context) (string, string, string, error) {
	var postgresVersion, postgisVersion, timescaleVersion string
	err := p.pool.QueryRow(ctx, `
		SELECT current_setting('server_version'), postgis_lib_version(),
		       (SELECT extversion FROM pg_extension WHERE extname='timescaledb')`).
		Scan(&postgresVersion, &postgisVersion, &timescaleVersion)
	return postgresVersion, postgisVersion, timescaleVersion, err
}

func (p *Store) ApplyCatalog(ctx context.Context, input storeapi.CatalogBundle) (storeapi.Catalog, error) {
	bundle, id, err := catalog.Normalize(input)
	if err != nil {
		return storeapi.Catalog{}, err
	}
	derived, err := recorddoc.DeriveCatalog(bundle.Catalog)
	if err != nil {
		return storeapi.Catalog{}, err
	}
	queryables, _ := json.Marshal(bundle.Queryables)
	sortables, _ := json.Marshal(bundle.Sortables)
	defaultSort, _ := json.Marshal(bundle.DefaultSortOrder)
	facets, _ := json.Marshal(bundle.Facets)
	storage, _ := json.Marshal(bundle.Storage)
	externalIDs := make([]string, 0, len(derived.ExternalIDs))
	for _, value := range derived.ExternalIDs {
		externalIDs = append(externalIDs, value.Canonical)
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return storeapi.Catalog{}, err
	}
	defer tx.Rollback(ctx)
	var lockedID, existingStorage string
	var existingSchema, existingQueryables, existingSortables []byte
	err = tx.QueryRow(ctx, `SELECT id, storage_class, record_schema, queryables, sortables FROM catalogs WHERE id=$1 FOR UPDATE`, id).
		Scan(&lockedID, &existingStorage, &existingSchema, &existingQueryables, &existingSortables)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return storeapi.Catalog{}, err
	}
	if err == nil {
		var count int64
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM record_keys WHERE catalog_id=$1`, id).Scan(&count); err != nil {
			return storeapi.Catalog{}, err
		}
		if count > 0 && existingStorage != bundle.Storage.Class {
			return storeapi.Catalog{}, fmt.Errorf("cannot change storage.class for a non-empty catalog")
		}
		if !jsonDocumentsEqual(existingSchema, bundle.Schema) ||
			!jsonDocumentsEqual(existingQueryables, queryables) ||
			!jsonDocumentsEqual(existingSortables, sortables) {
			relation, err := relationFor(existingStorage)
			if err != nil {
				return storeapi.Catalog{}, err
			}
			rows, err := tx.Query(ctx, `SELECT document FROM `+string(relation)+` WHERE catalog_id=$1`, id)
			if err != nil {
				return storeapi.Catalog{}, err
			}
			for rows.Next() {
				var document json.RawMessage
				if err := rows.Scan(&document); err != nil {
					rows.Close()
					return storeapi.Catalog{}, err
				}
				if err := recordschema.ValidateBundle(bundle, document); err != nil {
					rows.Close()
					return storeapi.Catalog{}, fmt.Errorf("existing record does not satisfy updated catalog configuration: %w", err)
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return storeapi.Catalog{}, err
			}
			rows.Close()
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO catalogs(id, document, storage_class, storage, queryables, sortables, default_sort, facets, record_schema,
		                     geometry, has_time, time_start, time_end, external_ids)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,
		       CASE WHEN $10::text IS NULL THEN NULL ELSE ST_SetSRID(ST_GeomFromGeoJSON($10),4326) END,
		       $11,$12,$13,$14)
		ON CONFLICT(id) DO UPDATE SET
			document=EXCLUDED.document, storage_class=EXCLUDED.storage_class, storage=EXCLUDED.storage,
			queryables=EXCLUDED.queryables, sortables=EXCLUDED.sortables,
			default_sort=EXCLUDED.default_sort, facets=EXCLUDED.facets, record_schema=EXCLUDED.record_schema,
			geometry=EXCLUDED.geometry, has_time=EXCLUDED.has_time, time_start=EXCLUDED.time_start,
			time_end=EXCLUDED.time_end, external_ids=EXCLUDED.external_ids, updated_at=statement_timestamp()`,
		id, bundle.Catalog, bundle.Storage.Class, storage, queryables, sortables, defaultSort, facets, bundle.Schema,
		derived.Geometry, derived.HasTime, derived.TimeStart, derived.TimeEnd, externalIDs)
	if err != nil {
		return storeapi.Catalog{}, mapError(err)
	}
	if err := reconcileCatalogIndexes(ctx, tx, id, bundle); err != nil {
		return storeapi.Catalog{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return storeapi.Catalog{}, err
	}
	return p.GetCatalog(ctx, id)
}

func jsonDocumentsEqual(left, right []byte) bool {
	var leftValue, rightValue any
	leftDecoder := json.NewDecoder(bytes.NewReader(left))
	leftDecoder.UseNumber()
	rightDecoder := json.NewDecoder(bytes.NewReader(right))
	rightDecoder.UseNumber()
	if leftDecoder.Decode(&leftValue) != nil || rightDecoder.Decode(&rightValue) != nil {
		return false
	}
	leftCanonical, _ := json.Marshal(leftValue)
	rightCanonical, _ := json.Marshal(rightValue)
	return bytes.Equal(leftCanonical, rightCanonical)
}

func (p *Store) GetCatalog(ctx context.Context, id string) (storeapi.Catalog, error) {
	return getCatalog(ctx, p.pool, id)
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getCatalog(ctx context.Context, q rowQuerier, id string) (storeapi.Catalog, error) {
	var result storeapi.Catalog
	var catalogDoc, storage, queryables, sortables, defaultSort, facets, schema []byte
	err := q.QueryRow(ctx, `SELECT id, document, storage, queryables, sortables, default_sort, facets, record_schema, created_at, updated_at FROM catalogs WHERE id=$1`, id).
		Scan(&result.ID, &catalogDoc, &storage, &queryables, &sortables, &defaultSort, &facets, &schema, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, storeapi.ErrNotFound
	}
	if err != nil {
		return result, err
	}
	result.Bundle.Catalog = catalogDoc
	result.Bundle.Schema = schema
	if err := json.Unmarshal(storage, &result.Bundle.Storage); err != nil {
		return result, err
	}
	if err := json.Unmarshal(queryables, &result.Bundle.Queryables); err != nil {
		return result, err
	}
	if err := json.Unmarshal(sortables, &result.Bundle.Sortables); err != nil {
		return result, err
	}
	if err := json.Unmarshal(defaultSort, &result.Bundle.DefaultSortOrder); err != nil {
		return result, err
	}
	if err := json.Unmarshal(facets, &result.Bundle.Facets); err != nil {
		return result, err
	}
	return result, nil
}

func lockCatalog(ctx context.Context, tx pgx.Tx, id string) error {
	var lockedID string
	if err := tx.QueryRow(ctx, `SELECT id FROM catalogs WHERE id=$1 FOR KEY SHARE`, id).Scan(&lockedID); errors.Is(err, pgx.ErrNoRows) {
		return storeapi.ErrNotFound
	} else {
		return err
	}
}

func (p *Store) DeleteCatalog(ctx context.Context, id string, cascade bool) error {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalogs WHERE id=$1 FOR UPDATE)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return storeapi.ErrNotFound
	}
	var count int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM record_keys WHERE catalog_id=$1`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 && !cascade {
		return storeapi.ErrCatalogNotEmpty
	}
	if cascade {
		if _, err := tx.Exec(ctx, `DELETE FROM records_temporal WHERE catalog_id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM record_keys WHERE catalog_id=$1`, id); err != nil {
			return err
		}
	}
	if err := removeCatalogIndexes(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM catalogs WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Store) GetRecord(ctx context.Context, catalogID, id string) (storeapi.StoredRecord, error) {
	catalog, err := p.GetCatalog(ctx, catalogID)
	if err != nil {
		return storeapi.StoredRecord{}, err
	}
	record, err := getRecord(ctx, p.pool, catalog.Bundle.Storage.Class, catalogID, id, false)
	return record.StoredRecord, err
}

type locatedRecord struct {
	storeapi.StoredRecord
	PartitionTime *time.Time
}

func getRecord(ctx context.Context, q rowQuerier, storageClass, catalogID, id string, lock bool) (locatedRecord, error) {
	var result locatedRecord
	relation, err := relationFor(storageClass)
	if err != nil {
		return result, err
	}
	locatorSQL := `SELECT partition_time FROM record_keys WHERE catalog_id=$1 AND id=$2 AND storage_class=$3`
	if lock {
		locatorSQL += ` FOR UPDATE`
	}
	err = q.QueryRow(ctx, locatorSQL, catalogID, id, storageClass).Scan(&result.PartitionTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, storeapi.ErrNotFound
	}
	if err != nil {
		return result, err
	}
	sql := `SELECT id, document, version, created_at, updated_at FROM ` + string(relation) + ` WHERE catalog_id=$1 AND id=$2`
	args := []any{catalogID, id}
	if relation == recordsTemporal {
		sql += ` AND partition_time=$3`
		args = append(args, result.PartitionTime)
	}
	if lock {
		sql += ` FOR UPDATE`
	}
	err = q.QueryRow(ctx, sql, args...).Scan(&result.ID, &result.Document, &result.Version, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, storeapi.ErrNotFound
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

func (p *Store) CreateRecord(ctx context.Context, catalogID, id string, document json.RawMessage) (storeapi.StoredRecord, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return storeapi.StoredRecord{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockCatalog(ctx, tx, catalogID); err != nil {
		return storeapi.StoredRecord{}, err
	}
	catalog, err := getCatalog(ctx, tx, catalogID)
	if err != nil {
		return storeapi.StoredRecord{}, err
	}
	if err := recordschema.ValidateBundle(catalog.Bundle, document); err != nil {
		return storeapi.StoredRecord{}, fmt.Errorf("invalid record: %w", err)
	}
	derived, err := recorddoc.Derive(document)
	if err != nil {
		return storeapi.StoredRecord{}, err
	}
	result, err := insertRecord(ctx, tx, catalog.Bundle.Storage.Class, catalogID, id, document, derived)
	if err != nil {
		return result, mapError(err)
	}
	if err := replaceExternalIDs(ctx, tx, catalogID, id, derived.ExternalIDs); err != nil {
		return result, err
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func (p *Store) PutRecord(ctx context.Context, catalogID, id string, document json.RawMessage, expected *int64) (storeapi.PutResult, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return storeapi.PutResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockCatalog(ctx, tx, catalogID); err != nil {
		return storeapi.PutResult{}, err
	}
	catalog, err := getCatalog(ctx, tx, catalogID)
	if err != nil {
		return storeapi.PutResult{}, err
	}
	if err := recordschema.ValidateBundle(catalog.Bundle, document); err != nil {
		return storeapi.PutResult{}, fmt.Errorf("invalid record: %w", err)
	}
	derived, err := recorddoc.Derive(document)
	if err != nil {
		return storeapi.PutResult{}, err
	}
	current, err := getRecord(ctx, tx, catalog.Bundle.Storage.Class, catalogID, id, true)
	if errors.Is(err, storeapi.ErrNotFound) {
		if expected != nil {
			return storeapi.PutResult{}, storeapi.ErrPrecondition
		}
		created, err := insertRecord(ctx, tx, catalog.Bundle.Storage.Class, catalogID, id, document, derived)
		if err != nil {
			return storeapi.PutResult{}, mapError(err)
		}
		if err := replaceExternalIDs(ctx, tx, catalogID, id, derived.ExternalIDs); err != nil {
			return storeapi.PutResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return storeapi.PutResult{}, err
		}
		return storeapi.PutResult{Record: created, Created: true}, nil
	}
	if err != nil {
		return storeapi.PutResult{}, err
	}
	if expected != nil && *expected >= 0 && current.Version != *expected {
		return storeapi.PutResult{}, storeapi.ErrPrecondition
	}
	updated, err := updateRecord(ctx, tx, catalog.Bundle.Storage.Class, catalogID, id, document, derived, current.PartitionTime)
	if err != nil {
		return storeapi.PutResult{}, err
	}
	if err := replaceExternalIDs(ctx, tx, catalogID, id, derived.ExternalIDs); err != nil {
		return storeapi.PutResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return storeapi.PutResult{}, err
	}
	return storeapi.PutResult{Record: updated}, nil
}

func (p *Store) PatchRecord(ctx context.Context, catalogID, id string, patch json.RawMessage, expected *int64) (storeapi.StoredRecord, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return storeapi.StoredRecord{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockCatalog(ctx, tx, catalogID); err != nil {
		return storeapi.StoredRecord{}, err
	}
	catalog, err := getCatalog(ctx, tx, catalogID)
	if err != nil {
		return storeapi.StoredRecord{}, err
	}
	current, err := getRecord(ctx, tx, catalog.Bundle.Storage.Class, catalogID, id, true)
	if err != nil {
		return storeapi.StoredRecord{}, err
	}
	if expected != nil && *expected >= 0 && current.Version != *expected {
		return storeapi.StoredRecord{}, storeapi.ErrPrecondition
	}
	merged, err := jsonpatch.MergePatch(current.Document, patch)
	if err != nil {
		return storeapi.StoredRecord{}, fmt.Errorf("merge patch: %w", err)
	}
	doc, err := recorddoc.Decode(merged)
	if err != nil {
		return storeapi.StoredRecord{}, err
	}
	if err := recorddoc.ForPut(doc, id); err != nil {
		return storeapi.StoredRecord{}, err
	}
	merged, err = recorddoc.Encode(doc)
	if err != nil {
		return storeapi.StoredRecord{}, err
	}
	if err := recordschema.ValidateBundle(catalog.Bundle, merged); err != nil {
		return storeapi.StoredRecord{}, fmt.Errorf("invalid record: %w", err)
	}
	derived, err := recorddoc.Derive(merged)
	if err != nil {
		return storeapi.StoredRecord{}, err
	}
	result, err := updateRecord(ctx, tx, catalog.Bundle.Storage.Class, catalogID, id, merged, derived, current.PartitionTime)
	if err != nil {
		return result, err
	}
	if err := replaceExternalIDs(ctx, tx, catalogID, id, derived.ExternalIDs); err != nil {
		return result, err
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func (p *Store) DeleteRecord(ctx context.Context, catalogID, id string, expected *int64) error {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockCatalog(ctx, tx, catalogID); err != nil {
		return err
	}
	catalog, err := getCatalog(ctx, tx, catalogID)
	if err != nil {
		return err
	}
	current, err := getRecord(ctx, tx, catalog.Bundle.Storage.Class, catalogID, id, true)
	if err != nil {
		return err
	}
	if expected != nil && *expected >= 0 && current.Version != *expected {
		return storeapi.ErrPrecondition
	}
	relation, err := relationFor(catalog.Bundle.Storage.Class)
	if err != nil {
		return err
	}
	deleteSQL := `DELETE FROM ` + string(relation) + ` WHERE catalog_id=$1 AND id=$2`
	args := []any{catalogID, id}
	if relation == recordsTemporal {
		deleteSQL += ` AND partition_time=$3`
		args = append(args, current.PartitionTime)
	}
	if _, err := tx.Exec(ctx, deleteSQL, args...); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM record_keys WHERE catalog_id=$1 AND id=$2`, catalogID, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func insertRecord(ctx context.Context, tx pgx.Tx, storageClass, catalogID, id string, document json.RawMessage, derived recorddoc.Derived) (storeapi.StoredRecord, error) {
	var result storeapi.StoredRecord
	relation, partitionTime, err := recordTarget(storageClass, derived, nil)
	if err != nil {
		return result, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO record_keys(catalog_id,id,storage_class,partition_time) VALUES($1,$2,$3,$4)`, catalogID, id, storageClass, partitionTime); err != nil {
		return result, err
	}
	columns := `catalog_id,id,document,geometry,has_time,time_start,time_end`
	values := `$1,$2,$3,CASE WHEN $4::text IS NULL THEN NULL ELSE ST_SetSRID(ST_GeomFromGeoJSON($4),4326) END,$5,$6,$7`
	args := []any{catalogID, id, document, derived.Geometry, derived.HasTime, derived.TimeStart, derived.TimeEnd}
	if relation == recordsTemporal {
		columns = `catalog_id,id,partition_time,document,geometry,has_time,time_start,time_end`
		values = `$1,$2,$8,$3,CASE WHEN $4::text IS NULL THEN NULL ELSE ST_SetSRID(ST_GeomFromGeoJSON($4),4326) END,$5,$6,$7`
		args = append(args, partitionTime)
	}
	err = tx.QueryRow(ctx, `INSERT INTO `+string(relation)+`(`+columns+`) VALUES(`+values+`)
		RETURNING id,document,version,created_at,updated_at`, args...).
		Scan(&result.ID, &result.Document, &result.Version, &result.CreatedAt, &result.UpdatedAt)
	return result, err
}

func updateRecord(ctx context.Context, tx pgx.Tx, storageClass, catalogID, id string, document json.RawMessage, derived recorddoc.Derived, partitionTime *time.Time) (storeapi.StoredRecord, error) {
	var result storeapi.StoredRecord
	relation, _, err := recordTarget(storageClass, derived, partitionTime)
	if err != nil {
		return result, err
	}
	where := `catalog_id=$1 AND id=$2`
	args := []any{catalogID, id, document, derived.Geometry, derived.HasTime, derived.TimeStart, derived.TimeEnd}
	if relation == recordsTemporal {
		where += ` AND partition_time=$8`
		args = append(args, partitionTime)
	}
	err = tx.QueryRow(ctx, `
		UPDATE `+string(relation)+` SET document=$3,
		 geometry=CASE WHEN $4::text IS NULL THEN NULL ELSE ST_SetSRID(ST_GeomFromGeoJSON($4),4326) END,
		 has_time=$5,time_start=$6,time_end=$7,version=version+1,
		 updated_at=GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
		WHERE `+where+`
		RETURNING id,document,version,created_at,updated_at`, args...).
		Scan(&result.ID, &result.Document, &result.Version, &result.CreatedAt, &result.UpdatedAt)
	return result, err
}

func recordTarget(storageClass string, derived recorddoc.Derived, existingPartition *time.Time) (recordRelation, *time.Time, error) {
	relation, err := relationFor(storageClass)
	if err != nil {
		return "", nil, err
	}
	if relation == recordsTransactional {
		return relation, nil, nil
	}
	if !derived.HasTime || derived.TimeStart == nil {
		return "", nil, fmt.Errorf("%w: temporal catalog records require time with a closed start", recorddoc.ErrInvalid)
	}
	partition := derived.TimeStart.UTC()
	if existingPartition != nil && !partition.Equal(existingPartition.UTC()) {
		return "", nil, fmt.Errorf("%w: temporal record time start is immutable", recorddoc.ErrInvalid)
	}
	return relation, &partition, nil
}

func replaceExternalIDs(ctx context.Context, tx pgx.Tx, catalogID, id string, values []recorddoc.ExternalID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM record_external_ids WHERE catalog_id=$1 AND record_id=$2`, catalogID, id); err != nil {
		return err
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if seen[value.Canonical] {
			continue
		}
		seen[value.Canonical] = true
		var scheme any
		if value.Scheme != "" {
			scheme = value.Scheme
		}
		if _, err := tx.Exec(ctx, `INSERT INTO record_external_ids(catalog_id,record_id,scheme,value,external_id) VALUES($1,$2,$3,$4,$5)`, catalogID, id, scheme, value.Value, value.Canonical); err != nil {
			return err
		}
	}
	return nil
}

func mapError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return storeapi.ErrConflict
		case "23503":
			return storeapi.ErrNotFound
		}
	}
	return err
}

var _ storeapi.Store = (*Store)(nil)
