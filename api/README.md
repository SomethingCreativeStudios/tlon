# OpenAPI source

`openapi.yaml` is the checked-in Tlon contract normalized from the Records Part 1 all-in-one example in the sibling `ogcapi-records` standards project.

Normalization decisions:

- JSON/GeoJSON only; HTML and example Google OIDC definitions are removed.
- `/api`, queryables, schema, facets, CRUD, HEAD, OPTIONS, and health endpoints are explicit.
- the Part 2 ATS media type `application/facets+json` wins over conflicting draft prose;
- facet searches allow `limit=0`;
- writes advertise generic bearer JWT security while runtime authorization remains an injected application concern.

Regenerate both public packages with `make generate`. CI uses `make check-generate` to reject drift.

