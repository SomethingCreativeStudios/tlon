CREATE EXTENSION IF NOT EXISTS timescaledb;
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE IF NOT EXISTS catalogs (
    id text PRIMARY KEY,
    document jsonb NOT NULL,
    storage_class text NOT NULL DEFAULT 'transactional'
        CHECK (storage_class IN ('transactional', 'temporal')),
    queryables jsonb NOT NULL DEFAULT '{}'::jsonb,
    sortables jsonb NOT NULL DEFAULT '{}'::jsonb,
    default_sort jsonb NOT NULL DEFAULT '[]'::jsonb,
    facets jsonb NOT NULL DEFAULT '{}'::jsonb,
    record_schema jsonb NOT NULL DEFAULT '{}'::jsonb,
    geometry geometry(Geometry, 4326),
    has_time boolean NOT NULL DEFAULT false,
    time_start timestamptz,
    time_end timestamptz,
    time_range tstzrange GENERATED ALWAYS AS (tstzrange(time_start, time_end, '[]')) STORED,
    external_ids text[] NOT NULL DEFAULT '{}'::text[],
    search_vector tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', coalesce(document->>'title', '')), 'A') ||
        setweight(to_tsvector('simple', coalesce(document->>'description', '')), 'B') ||
        setweight(to_tsvector('simple', coalesce(document->>'keywords', '')), 'C')
    ) STORED,
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE INDEX IF NOT EXISTS catalogs_geometry_gist ON catalogs USING gist (geometry);
CREATE INDEX IF NOT EXISTS catalogs_time_gist ON catalogs USING gist (time_range) WHERE has_time;
CREATE INDEX IF NOT EXISTS catalogs_search_gin ON catalogs USING gin (search_vector);
CREATE INDEX IF NOT EXISTS catalogs_external_ids_gin ON catalogs USING gin (external_ids);

-- The locator owns API-level identity independently of physical storage. For
-- temporal records it also contains the complete Timescale partition key.
CREATE TABLE IF NOT EXISTS record_keys (
    catalog_id text NOT NULL REFERENCES catalogs(id) ON DELETE RESTRICT,
    id text NOT NULL,
    storage_class text NOT NULL CHECK (storage_class IN ('transactional', 'temporal')),
    partition_time timestamptz,
    PRIMARY KEY (catalog_id, id),
    CHECK (
        (storage_class = 'transactional' AND partition_time IS NULL) OR
        (storage_class = 'temporal' AND partition_time IS NOT NULL)
    )
);

CREATE TABLE IF NOT EXISTS records_transactional (
    catalog_id text NOT NULL,
    id text NOT NULL,
    document jsonb NOT NULL,
    geometry geometry(Geometry, 4326),
    has_time boolean NOT NULL DEFAULT false,
    time_start timestamptz,
    time_end timestamptz,
    time_range tstzrange GENERATED ALWAYS AS (tstzrange(time_start, time_end, '[]')) STORED,
    search_vector tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', coalesce(jsonb_extract_path_text(document, 'properties', 'title'), '')), 'A') ||
        setweight(to_tsvector('simple', coalesce(jsonb_extract_path_text(document, 'properties', 'description'), '')), 'B') ||
        setweight(to_tsvector('simple', coalesce(jsonb_extract_path_text(document, 'properties', 'keywords'), '')), 'C')
    ) STORED,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (catalog_id, id),
    FOREIGN KEY (catalog_id, id) REFERENCES record_keys(catalog_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS records_transactional_catalog_geometry_gist
    ON records_transactional USING gist (catalog_id, geometry);
CREATE INDEX IF NOT EXISTS records_transactional_catalog_time_gist
    ON records_transactional USING gist (catalog_id, time_range) WHERE has_time;
CREATE INDEX IF NOT EXISTS records_transactional_search_gin
    ON records_transactional USING gin (search_vector);
CREATE INDEX IF NOT EXISTS records_transactional_catalog_type_id_idx
    ON records_transactional (catalog_id, (jsonb_extract_path_text(document, 'properties', 'type')), id);
CREATE INDEX IF NOT EXISTS records_transactional_catalog_title_id_idx
    ON records_transactional (catalog_id, (jsonb_extract_path_text(document, 'properties', 'title')), id);
CREATE INDEX IF NOT EXISTS records_transactional_catalog_created_id_idx
    ON records_transactional (catalog_id, created_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS records_transactional_catalog_updated_id_idx
    ON records_transactional (catalog_id, updated_at DESC, id ASC);

CREATE TABLE IF NOT EXISTS records_temporal (
    catalog_id text NOT NULL REFERENCES catalogs(id) ON DELETE RESTRICT,
    id text NOT NULL,
    partition_time timestamptz NOT NULL,
    document jsonb NOT NULL,
    geometry geometry(Geometry, 4326),
    has_time boolean NOT NULL DEFAULT true CHECK (has_time),
    time_start timestamptz NOT NULL,
    time_end timestamptz,
    time_range tstzrange GENERATED ALWAYS AS (tstzrange(time_start, time_end, '[]')) STORED,
    search_vector tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', coalesce(jsonb_extract_path_text(document, 'properties', 'title'), '')), 'A') ||
        setweight(to_tsvector('simple', coalesce(jsonb_extract_path_text(document, 'properties', 'description'), '')), 'B') ||
        setweight(to_tsvector('simple', coalesce(jsonb_extract_path_text(document, 'properties', 'keywords'), '')), 'C')
    ) STORED,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (catalog_id, partition_time, id),
    CHECK (partition_time = time_start)
);

SELECT create_hypertable(
    'records_temporal',
    'partition_time',
    chunk_time_interval => INTERVAL '7 days',
    create_default_indexes => FALSE,
    if_not_exists => TRUE
);

CREATE INDEX IF NOT EXISTS records_temporal_catalog_partition_id_idx
    ON records_temporal (catalog_id, partition_time DESC, id ASC);
CREATE INDEX IF NOT EXISTS records_temporal_catalog_geometry_gist
    ON records_temporal USING gist (catalog_id, geometry);
CREATE INDEX IF NOT EXISTS records_temporal_catalog_time_gist
    ON records_temporal USING gist (catalog_id, time_range);
CREATE INDEX IF NOT EXISTS records_temporal_search_gin
    ON records_temporal USING gin (search_vector);
CREATE INDEX IF NOT EXISTS records_temporal_catalog_type_time_id_idx
    ON records_temporal (catalog_id, (jsonb_extract_path_text(document, 'properties', 'type')), partition_time DESC, id);
CREATE INDEX IF NOT EXISTS records_temporal_catalog_title_time_id_idx
    ON records_temporal (catalog_id, (jsonb_extract_path_text(document, 'properties', 'title')), partition_time DESC, id);
CREATE INDEX IF NOT EXISTS records_temporal_catalog_created_id_idx
    ON records_temporal (catalog_id, created_at DESC, id ASC, partition_time);
CREATE INDEX IF NOT EXISTS records_temporal_catalog_updated_id_idx
    ON records_temporal (catalog_id, updated_at DESC, id ASC, partition_time);

CREATE TABLE IF NOT EXISTS record_external_ids (
    catalog_id text NOT NULL,
    record_id text NOT NULL,
    scheme text,
    value text NOT NULL,
    external_id text NOT NULL,
    PRIMARY KEY (catalog_id, record_id, external_id),
    FOREIGN KEY (catalog_id, record_id) REFERENCES record_keys(catalog_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS record_external_ids_lookup_idx
    ON record_external_ids (catalog_id, external_id, record_id);
CREATE INDEX IF NOT EXISTS record_external_value_lookup_idx
    ON record_external_ids (catalog_id, value, record_id);
