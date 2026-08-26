ALTER TABLE catalogs
    ADD COLUMN IF NOT EXISTS storage jsonb;

UPDATE catalogs
SET storage = jsonb_build_object('class', storage_class)
WHERE storage IS NULL;

ALTER TABLE catalogs
    ALTER COLUMN storage SET DEFAULT '{"class":"transactional"}'::jsonb,
    ALTER COLUMN storage SET NOT NULL;

-- Bind catalog declarations to physical indexes. Identical typed expressions
-- share one physical index across catalogs in the same storage relation, which
-- avoids one PostgreSQL/Timescale index object per catalog.
CREATE TABLE IF NOT EXISTS catalog_indexes (
    catalog_id text NOT NULL REFERENCES catalogs(id) ON DELETE CASCADE,
    signature text NOT NULL,
    index_name text NOT NULL,
    storage_class text NOT NULL CHECK (storage_class IN ('transactional', 'temporal')),
    definition jsonb NOT NULL,
    automatic boolean NOT NULL,
    PRIMARY KEY (catalog_id, signature)
);

CREATE INDEX IF NOT EXISTS catalog_indexes_physical_idx
    ON catalog_indexes (index_name, catalog_id);
