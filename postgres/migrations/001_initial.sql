CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS catalogs (
    id text PRIMARY KEY,
    document jsonb NOT NULL,
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

CREATE TABLE IF NOT EXISTS records (
    catalog_id text NOT NULL REFERENCES catalogs(id) ON DELETE RESTRICT,
    id text NOT NULL,
    document jsonb NOT NULL,
    geometry geometry(Geometry, 4326),
    has_time boolean NOT NULL DEFAULT false,
    time_start timestamptz,
    time_end timestamptz,
    time_range tstzrange GENERATED ALWAYS AS (tstzrange(time_start, time_end, '[]')) STORED,
    search_vector tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', coalesce(document #>> '{properties,title}', '')), 'A') ||
        setweight(to_tsvector('simple', coalesce(document #>> '{properties,description}', '')), 'B') ||
        setweight(to_tsvector('simple', coalesce(document #>> '{properties,keywords}', '')), 'C')
    ) STORED,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (catalog_id, id)
);

CREATE INDEX IF NOT EXISTS records_geometry_gist ON records USING gist (geometry);
CREATE INDEX IF NOT EXISTS records_time_gist ON records USING gist (time_range) WHERE has_time;
CREATE INDEX IF NOT EXISTS records_search_gin ON records USING gin (search_vector);
CREATE INDEX IF NOT EXISTS records_catalog_type_idx ON records (catalog_id, ((document #>> '{properties,type}')));
CREATE INDEX IF NOT EXISTS records_catalog_updated_id_idx ON records (catalog_id, updated_at DESC, id ASC);

CREATE TABLE IF NOT EXISTS record_external_ids (
    catalog_id text NOT NULL,
    record_id text NOT NULL,
    scheme text,
    value text NOT NULL,
    external_id text NOT NULL,
    PRIMARY KEY (catalog_id, record_id, external_id),
    FOREIGN KEY (catalog_id, record_id) REFERENCES records(catalog_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS record_external_ids_lookup_idx ON record_external_ids (catalog_id, external_id);
CREATE INDEX IF NOT EXISTS record_external_value_lookup_idx ON record_external_ids (catalog_id, value);

