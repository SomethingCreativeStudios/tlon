# Tlon

Tlon is a Go 1.27 server and reusable library for OGC API Records. This first milestone implements:

- OGC API Records Part 1 JSON/GeoJSON discovery and searchable catalogs;
- draft Part 2 simple and advanced term, histogram, and filter facets;
- the record CRUD class from draft Part 3, without the still-unspecified Harvest class;
- Basic CQL2 Text, spatial/temporal/text filters, exact counts, deterministic sorting, and signed cursor pagination;
- durable PostgreSQL 18/PostGIS 3.6 storage and operator-controlled migrations.

The normalized contract is [api/openapi.yaml](api/openapi.yaml). Generated strict Chi types and the embedded specification live in package `api`; the generated Hronir-ready HTTP client lives in package `client`.

## Quick start

Build and start PostgreSQL, run migrations, and start Tlon:

```sh
docker compose up --build
```

Load the bundled demonstration catalog and 120 deterministic records from
another terminal:

```sh
docker compose run --rm tlon demo seed --reset
```

Open [http://localhost:8080/playground](http://localhost:8080/playground) to
browse its collections, records, searches, and facets. The playground is
embedded in the Tlon binary and does not need a separate frontend service.

To apply only the smaller catalog definition without records, use:

```sh
docker compose run --rm \
  -v "$PWD/examples:/examples:ro" \
  tlon catalog apply /examples/catalog.json
```

Reads are public. Mutations are denied by default:

```sh
curl http://localhost:8080/collections/records/items
curl -i -H 'Content-Type: application/geo+json' \
  --data @record.json \
  http://localhost:8080/collections/records/items
```

For a deployment where Istio or another trusted proxy performs OIDC authentication and method/catalog authorization, explicitly enable external authorization:

```sh
TLON_AUTH_MODE=external docker compose up --build
```

`external` mode trusts that proxy completely and must not be exposed directly to untrusted clients.

## Commands

```text
tlon serve
tlon migrate up
tlon migrate status
tlon catalog apply BUNDLE.json
tlon catalog get CATALOG_ID
tlon catalog list
tlon catalog delete [--cascade] CATALOG_ID
tlon demo seed [--catalog ID] [--count N] [--seed N] [--reset]
```

`TLON_DATABASE_URL` supplies the database for migration, catalog, and demo
commands; `--database-url` overrides it. Catalog deletion refuses a non-empty
catalog unless `--cascade` is explicit.

## Configuration

| Variable | Default | Meaning |
|---|---:|---|
| `TLON_DATABASE_URL` | required | PostgreSQL connection URL |
| `TLON_PUBLIC_URL` | `http://localhost:8080` | Absolute base used for links and `Location` |
| `TLON_LISTEN_ADDR` | `:8080` | HTTP listen address |
| `TLON_CURSOR_SECRET` | required | Stable HMAC secret, at least 32 bytes |
| `TLON_AUTH_MODE` | `deny` | `deny` or explicitly trusted `external` writes |
| `TLON_DEFAULT_LIMIT` | `10` | Default collection page size |
| `TLON_MAX_LIMIT` | `1000` | Deployment maximum, at most 10000 |
| `TLON_READ_TIMEOUT` | `15s` | HTTP read/header timeout |
| `TLON_WRITE_TIMEOUT` | `30s` | HTTP response timeout |
| `TLON_IDLE_TIMEOUT` | `60s` | HTTP keep-alive timeout |
| `TLON_SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown deadline |
| `TLON_LOG_LEVEL` | `info` | JSON log level |

The cursor secret must be identical across replicas and retained across restarts; changing it intentionally invalidates outstanding cursors.

## Catalog bundles

A bundle atomically combines the OGC catalog object with its operational schema:

- `catalog`: the catalog document (`id`, `type: Collection`, and `itemType: record` are required);
- `queryables`: typed Basic CQL2 properties with an optional `x-tlon-path` JSON pointer;
- `sortables`: safely mapped sortable properties;
- `defaultSortOrder`: `{property,direction}` entries;
- `facets`: term, histogram, and named filter definitions;
- `schema`: optional catalog-specific record JSON Schema.

The schema is available at `/collections/{catalogId}/schema`; Part 3 clients
may request the receivable replacement schema with `?type=replace` (the same
catalog schema currently applies to create, replace, and update operations).

Core `id`, `type`, `title`, `description`, `created`, and `updated` queryables are supplied automatically. Configuration is fail-closed: unsafe JSON pointers, unknown sort/facet properties, invalid Basic CQL2 filter buckets, and fixed-interval histograms without a positive numeric or ISO-8601 day/time interval are rejected during apply.

See [examples/catalog.json](examples/catalog.json) for a complete bundle.

## Playground and demo data

`GET /playground` is a non-standard, read-only developer UI and is not part of
Tlon's OGC conformance surface. It discovers the live API rather than using
fixtures. From it you can:

- browse catalogs and inspect their catalog document, queryables, sortables,
  facet definitions, and receivable schema;
- run text, type, temporal, spatial, sorting, and page-size searches, with
  catalog-aware CQL2 autocomplete sourced from the live queryables document;
- inspect exact counts, records, raw GeoJSON, and signed next/previous pages;
- visualize term, histogram, and named-filter buckets, and apply compatible
  buckets as CQL2 filters.

The demo generator uses stable record IDs. Repeating the same command replaces
those records, while the same `--seed` always produces the same content:

```sh
tlon demo seed --count 250 --seed 42
tlon demo seed --count 80 --seed 7 --catalog experiments --reset
```

The checked-in demo bundle includes all three facet types and the generated
records cover multiple resource types and organizations, numeric and temporal
ranges, points and polygons, antimeridian coverage, external IDs, open-ended
times, and records without geometry or time. `--reset` deletes and recreates
only the selected demo catalog. Because this is a local database administration
command, it intentionally uses the trusted application mutation path regardless
of the HTTP server's `TLON_AUTH_MODE`; database access is therefore the security
boundary for this command.

## Search and mutations

`bbox`, `datetime`, `q`, `type`, `ids`, `externalIds`, `filter`, `sortby`, and cursor predicates are combined with logical AND. Records without geometry or time match spatial or temporal parameters as required by Records Part 1. Sorting uses `+property`/`-property` and always adds an ID tie-breaker. Cursors are HMAC-signed and scoped to the catalog, normalized filters, facet request, and ordering.

Facets are calculated before paging. Omitting `facets` returns configured defaults; `facets=` suppresses them; `facets=name:count:count_desc` selects them. `limit=0` returns exact counts and facets without features.

Hronir's canonical synchronization operation is client-ID `PUT`. `POST` discards a submitted ID and creates UUIDv7. Tlon owns `id`, `properties.created`, `properties.updated`, and navigation links. Strong ETags are returned; `If-Match` is enforced when supplied, while unconditional authoritative writes remain available. PATCH uses RFC 7396 JSON Merge Patch.

## Development

```sh
make generate
make check-generate
make fmt
make test
make lint
make build
```

Go automatically downloads the Go 1.27 toolchain when an older host toolchain has `GOTOOLCHAIN=auto`. Integration tests use `TLON_TEST_DATABASE_URL` and the `integration` build tag. The development database image starts from multi-architecture `postgres:18-trixie` and installs distro PostGIS packages because the upstream `postgis/postgis:18-3.6` tag is amd64-only.
