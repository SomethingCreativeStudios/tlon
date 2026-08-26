\set ON_ERROR_STOP on
\timing on

\if :{?transactional_catalog}
\else
\set transactional_catalog load-transactional
\endif
\if :{?temporal_catalog}
\else
\set temporal_catalog load-temporal
\endif

SELECT storage_class, count(*) AS records
FROM record_keys
WHERE catalog_id IN (:'transactional_catalog', :'temporal_catalog')
GROUP BY storage_class
ORDER BY storage_class;

SELECT relname AS relation,
       n_live_tup AS estimated_rows,
       pg_size_pretty(pg_relation_size(relid)) AS table_size,
       pg_size_pretty(pg_indexes_size(relid)) AS index_size,
       pg_size_pretty(pg_total_relation_size(relid)) AS total_size
FROM pg_stat_user_tables
WHERE relname IN ('record_keys', 'record_external_ids', 'records_transactional', 'records_temporal')
ORDER BY relname;

SELECT pg_size_pretty(table_bytes) AS temporal_table,
       pg_size_pretty(index_bytes) AS temporal_indexes,
       pg_size_pretty(toast_bytes) AS temporal_toast,
       pg_size_pretty(total_bytes) AS temporal_total
FROM hypertable_detailed_size('records_temporal');

SELECT indexrelname AS temporal_index,
       pg_size_pretty(hypertable_index_size(indexrelid)) AS all_chunks_size
FROM pg_stat_user_indexes
WHERE relname = 'records_temporal'
ORDER BY indexrelname;

SELECT schemaname, relname AS relation, indexrelname AS index,
       idx_scan, idx_tup_read, idx_tup_fetch,
       pg_size_pretty(pg_relation_size(indexrelid)) AS index_size
FROM pg_stat_user_indexes
WHERE relname IN ('record_keys', 'record_external_ids', 'records_transactional', 'records_temporal')
ORDER BY relname, indexrelname;

-- Stable identity lookup through the small locator table.
EXPLAIN (ANALYZE, BUFFERS, SETTINGS)
SELECT partition_time
FROM record_keys
WHERE catalog_id = :'temporal_catalog' AND id = 'load-000000000001';

-- Default updated-desc cursor page. Managed sort columns are non-null, so the
-- predicate and ordering must remain compatible with the catalog/updated/ID
-- B-tree rather than falling back to a full-table top-N sort.
SELECT updated_at AS cursor_updated
FROM records_transactional
WHERE catalog_id = :'transactional_catalog'
  AND id = 'load-000000995010'
\gset

EXPLAIN (ANALYZE, BUFFERS, SETTINGS)
SELECT r.id
FROM records_transactional r
WHERE r.catalog_id = :'transactional_catalog'
  AND (
    r.updated_at < :'cursor_updated'::timestamptz
    OR (r.updated_at = :'cursor_updated'::timestamptz AND r.id > 'load-000000995010')
  )
ORDER BY r.updated_at DESC, r.id ASC
LIMIT 11;

-- Core type predicate backed by the catalog/type/ID expression index.
EXPLAIN (ANALYZE, BUFFERS, SETTINGS)
SELECT count(*)
FROM records_transactional r
WHERE r.catalog_id = :'transactional_catalog'
  AND jsonb_extract_path_text(r.document, 'properties', 'type') = 'observation';

-- Full-text query backed by the generated tsvector GIN index.
EXPLAIN (ANALYZE, BUFFERS, SETTINGS)
SELECT count(*)
FROM records_transactional r
WHERE r.catalog_id = :'transactional_catalog'
  AND r.search_vector @@ phraseto_tsquery('simple', 'climate');

-- Records semantics retain null geometries, producing the same OR predicate
-- used by the API's bbox implementation.
EXPLAIN (ANALYZE, BUFFERS, SETTINGS)
SELECT count(*)
FROM records_transactional r
WHERE r.catalog_id = :'transactional_catalog'
  AND (r.geometry IS NULL OR ST_Intersects(r.geometry, ST_MakeEnvelope(-80, 35, -70, 45, 4326)));

-- Transactional temporal overlap, including records without time.
EXPLAIN (ANALYZE, BUFFERS, SETTINGS)
SELECT count(*)
FROM records_transactional r
WHERE r.catalog_id = :'transactional_catalog'
  AND (NOT r.has_time OR r.time_range && tstzrange('2024-06-01T00:00:00Z', '2024-06-02T00:00:00Z', '[]'));

-- Temporal overlap with the safe upper partition bound used by Tlon. A lower
-- bound cannot be assumed for arbitrary intervals that began before the query.
EXPLAIN (ANALYZE, BUFFERS, SETTINGS)
SELECT count(*)
FROM records_temporal r
WHERE r.catalog_id = :'temporal_catalog'
  AND r.time_range && tstzrange('2024-06-01T00:00:00Z', '2024-06-02T00:00:00Z', '[]')
  AND r.partition_time <= '2024-06-02T00:00:00Z';

-- Normalized external identifier lookup.
EXPLAIN (ANALYZE, BUFFERS, SETTINGS)
SELECT r.id
FROM records_temporal r
WHERE r.catalog_id = :'temporal_catalog'
  AND EXISTS (
      SELECT 1
      FROM record_external_ids e
      JOIN record_keys k ON k.catalog_id = e.catalog_id AND k.id = e.record_id
      WHERE e.catalog_id = r.catalog_id AND e.record_id = r.id
        AND k.partition_time = r.partition_time
        AND e.external_id = 'load:temporal-000000000001'
  );

-- Representative extension-property term facet backed by the automatically
-- managed scalar facet index.
EXPLAIN (ANALYZE, BUFFERS, SETTINGS)
SELECT jsonb_extract_path_text(r.document, 'properties', 'organization') AS value,
       count(*) AS bucket_count
FROM records_transactional r
WHERE r.catalog_id = :'transactional_catalog'
GROUP BY value
ORDER BY bucket_count DESC, value
LIMIT 20;

-- Selective multi-property CQL2 shape backed by the explicit
-- category/organization/score composite configured by the load catalog.
EXPLAIN (ANALYZE, BUFFERS, SETTINGS)
SELECT count(*)
FROM records_temporal r
WHERE r.catalog_id = :'temporal_catalog'
  AND jsonb_extract_path_text(r.document, 'properties', 'category') = 'energy'
  AND jsonb_extract_path_text(r.document, 'properties', 'organization') = 'JAXA'
  AND NULLIF(jsonb_extract_path_text(r.document, 'properties', 'score'), '')::numeric >= 34.985
  AND NULLIF(jsonb_extract_path_text(r.document, 'properties', 'score'), '')::numeric < 35.982;

SELECT updated_at AS filtered_cursor_updated, id AS filtered_cursor_id
FROM records_temporal r
WHERE r.catalog_id = :'temporal_catalog'
  AND jsonb_extract_path_text(r.document, 'properties', 'category') = 'energy'
  AND jsonb_extract_path_text(r.document, 'properties', 'organization') = 'JAXA'
  AND NULLIF(jsonb_extract_path_text(r.document, 'properties', 'score'), '')::numeric >= 34.985
  AND NULLIF(jsonb_extract_path_text(r.document, 'properties', 'score'), '')::numeric < 35.982
ORDER BY r.updated_at DESC, r.id ASC
OFFSET 10 LIMIT 1
\gset

EXPLAIN (ANALYZE, BUFFERS, SETTINGS)
SELECT r.id
FROM records_temporal r
WHERE r.catalog_id = :'temporal_catalog'
  AND jsonb_extract_path_text(r.document, 'properties', 'category') = 'energy'
  AND jsonb_extract_path_text(r.document, 'properties', 'organization') = 'JAXA'
  AND NULLIF(jsonb_extract_path_text(r.document, 'properties', 'score'), '')::numeric >= 34.985
  AND NULLIF(jsonb_extract_path_text(r.document, 'properties', 'score'), '')::numeric < 35.982
  AND (
    r.updated_at < :'filtered_cursor_updated'::timestamptz
    OR (r.updated_at = :'filtered_cursor_updated'::timestamptz AND r.id > :'filtered_cursor_id')
  )
ORDER BY r.updated_at DESC, r.id ASC
LIMIT 11;

-- Representative extension filter plus sort. Compare this plan before and
-- after a candidate catalog/expression/ordering index.
EXPLAIN (ANALYZE, BUFFERS, SETTINGS)
SELECT r.id
FROM records_temporal r
WHERE r.catalog_id = :'temporal_catalog'
  AND jsonb_extract_path_text(r.document, 'properties', 'organization') = 'NOAA'
ORDER BY NULLIF(jsonb_extract_path_text(r.document, 'properties', 'observed'), '')::timestamptz DESC,
         r.id ASC
LIMIT 100;
