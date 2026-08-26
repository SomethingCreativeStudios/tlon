-- Catalog values are validated as RFC 3339 timestamps and ISO full-dates
-- before storage. These wrappers pin PostgreSQL's otherwise session-dependent
-- parsing settings so the same typed expressions are safe in B-tree indexes.
CREATE OR REPLACE FUNCTION public.tlon_rfc3339_timestamptz(value text)
RETURNS timestamptz
LANGUAGE sql
IMMUTABLE
STRICT
PARALLEL SAFE
SET TimeZone = 'UTC'
SET DateStyle = 'ISO, YMD'
AS $$ SELECT value::timestamptz $$;

CREATE OR REPLACE FUNCTION public.tlon_iso_date(value text)
RETURNS date
LANGUAGE sql
IMMUTABLE
STRICT
PARALLEL SAFE
SET DateStyle = 'ISO, YMD'
AS $$ SELECT value::date $$;
