package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	recorddoc "github.com/SomethingCreativeStudios/tlon/internal/record"
	"github.com/SomethingCreativeStudios/tlon/internal/recordschema"
	storeapi "github.com/SomethingCreativeStudios/tlon/store"
)

type stagedRecord struct {
	id          string
	document    json.RawMessage
	geometry    *string
	hasTime     bool
	timeStart   *time.Time
	timeEnd     *time.Time
	externalIDs json.RawMessage
	partition   *time.Time
}

// BulkCreateRecords validates and creates a batch of client-identified
// records in one transaction. It is intended for trusted ingestion and load
// tooling; duplicate identifiers fail the complete batch.
func (p *Store) BulkCreateRecords(ctx context.Context, catalogID string, inputs []storeapi.RecordInput) (int, error) {
	if len(inputs) == 0 {
		return 0, nil
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if err := lockCatalog(ctx, tx, catalogID); err != nil {
		return 0, err
	}
	catalog, err := getCatalog(ctx, tx, catalogID)
	if err != nil {
		return 0, err
	}
	relation, err := relationFor(catalog.Bundle.Storage.Class)
	if err != nil {
		return 0, err
	}

	rows := make([][]any, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		if input.ID == "" {
			return 0, fmt.Errorf("bulk record id is required")
		}
		if _, duplicate := seen[input.ID]; duplicate {
			return 0, fmt.Errorf("duplicate bulk record id %q", input.ID)
		}
		seen[input.ID] = struct{}{}
		staged, err := stageRecord(catalog.Bundle, input, relation)
		if err != nil {
			return 0, fmt.Errorf("bulk record %q: %w", input.ID, err)
		}
		rows = append(rows, []any{staged.id, staged.document, staged.geometry, staged.hasTime, staged.timeStart, staged.timeEnd, staged.externalIDs, staged.partition})
	}

	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE tlon_record_batch (
			id text NOT NULL,
			document jsonb NOT NULL,
			geometry_json text,
			has_time boolean NOT NULL,
			time_start timestamptz,
			time_end timestamptz,
			external_ids jsonb NOT NULL,
			partition_time timestamptz
		) ON COMMIT DROP`); err != nil {
		return 0, err
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"tlon_record_batch"}, []string{"id", "document", "geometry_json", "has_time", "time_start", "time_end", "external_ids", "partition_time"}, pgx.CopyFromRows(rows)); err != nil {
		return 0, fmt.Errorf("copy record batch: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO record_keys(catalog_id,id,storage_class,partition_time)
		SELECT $1,id,$2,partition_time FROM tlon_record_batch`, catalogID, catalog.Bundle.Storage.Class); err != nil {
		return 0, mapError(err)
	}

	if relation == recordsTransactional {
		_, err = tx.Exec(ctx, `
			INSERT INTO records_transactional(catalog_id,id,document,geometry,has_time,time_start,time_end)
			SELECT $1,id,document,
			       CASE WHEN geometry_json IS NULL THEN NULL ELSE ST_SetSRID(ST_GeomFromGeoJSON(geometry_json),4326) END,
			       has_time,time_start,time_end
			FROM tlon_record_batch`, catalogID)
	} else {
		_, err = tx.Exec(ctx, `
			INSERT INTO records_temporal(catalog_id,id,partition_time,document,geometry,has_time,time_start,time_end)
			SELECT $1,id,partition_time,document,
			       CASE WHEN geometry_json IS NULL THEN NULL ELSE ST_SetSRID(ST_GeomFromGeoJSON(geometry_json),4326) END,
			       has_time,time_start,time_end
			FROM tlon_record_batch`, catalogID)
	}
	if err != nil {
		return 0, mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO record_external_ids(catalog_id,record_id,scheme,value,external_id)
		SELECT $1,b.id,NULLIF(e.scheme,''),e.value,e.canonical
		FROM tlon_record_batch b
		CROSS JOIN LATERAL jsonb_to_recordset(b.external_ids)
			AS e(scheme text,value text,canonical text)`, catalogID); err != nil {
		return 0, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(inputs), nil
}

// AnalyzeCatalog refreshes planner statistics for the physical record table
// selected by a catalog. PostgreSQL ANALYZE operates on the shared table, so
// statistics are refreshed for all catalogs using that storage class.
func (p *Store) AnalyzeCatalog(ctx context.Context, catalogID string) error {
	catalog, err := p.GetCatalog(ctx, catalogID)
	if err != nil {
		return err
	}
	relation, err := relationFor(catalog.Bundle.Storage.Class)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `ANALYZE `+string(relation))
	return err
}

func stageRecord(bundle storeapi.CatalogBundle, input storeapi.RecordInput, relation recordRelation) (stagedRecord, error) {
	doc, err := recorddoc.Decode(input.Document)
	if err != nil {
		return stagedRecord{}, err
	}
	if err := recorddoc.ForPut(doc, input.ID); err != nil {
		return stagedRecord{}, err
	}
	raw, err := recorddoc.Encode(doc)
	if err != nil {
		return stagedRecord{}, err
	}
	if err := recordschema.ValidateBundle(bundle, raw); err != nil {
		return stagedRecord{}, fmt.Errorf("invalid record: %w", err)
	}
	derived, err := recorddoc.Derive(raw)
	if err != nil {
		return stagedRecord{}, err
	}
	_, partition, err := recordTarget(bundle.Storage.Class, derived, nil)
	if err != nil {
		return stagedRecord{}, err
	}
	if relation == recordsTransactional {
		partition = nil
	}
	external, err := json.Marshal(externalIDRows(derived.ExternalIDs))
	if err != nil {
		return stagedRecord{}, err
	}
	return stagedRecord{id: input.ID, document: raw, geometry: derived.Geometry, hasTime: derived.HasTime, timeStart: derived.TimeStart, timeEnd: derived.TimeEnd, externalIDs: external, partition: partition}, nil
}

func externalIDRows(values []recorddoc.ExternalID) []map[string]string {
	result := make([]map[string]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, duplicate := seen[value.Canonical]; duplicate {
			continue
		}
		seen[value.Canonical] = struct{}{}
		result = append(result, map[string]string{"scheme": value.Scheme, "value": value.Value, "canonical": value.Canonical})
	}
	return result
}
