package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/SomethingCreativeStudios/tlon/internal/cql"
	recorddoc "github.com/SomethingCreativeStudios/tlon/internal/record"
	storeapi "github.com/SomethingCreativeStudios/tlon/store"
)

type sqlBuilder struct {
	conditions []string
	args       []any
}

func (b *sqlBuilder) arg(value any) string {
	b.args = append(b.args, value)
	return "$" + strconv.Itoa(len(b.args))
}
func (b *sqlBuilder) add(condition string) { b.conditions = append(b.conditions, condition) }
func (b *sqlBuilder) where() string {
	if len(b.conditions) == 0 {
		return "TRUE"
	}
	return strings.Join(b.conditions, " AND ")
}
func (b sqlBuilder) clone() sqlBuilder {
	return sqlBuilder{conditions: append([]string(nil), b.conditions...), args: append([]any(nil), b.args...)}
}

type sortColumn struct {
	Field      storeapi.SortField
	Expression string
	Cast       string
}

func (p *Store) SearchRecords(ctx context.Context, catalogID string, search storeapi.Search) (storeapi.SearchResult, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return storeapi.SearchResult{}, err
	}
	defer tx.Rollback(ctx)
	catalog, err := getCatalog(ctx, tx, catalogID)
	if err != nil {
		return storeapi.SearchResult{}, err
	}
	columns, err := normalizeSort(search.Sort, catalog.Bundle.DefaultSortOrder, catalog.Bundle.Sortables)
	if err != nil {
		return storeapi.SearchResult{}, err
	}
	search.Sort = sortFields(columns)
	base, err := recordWhere(catalogID, search, catalog.Bundle.Queryables)
	if err != nil {
		return storeapi.SearchResult{}, err
	}
	var result storeapi.SearchResult
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM records r WHERE `+base.where(), base.args...).Scan(&result.NumberMatched); err != nil {
		return result, err
	}
	result.Facets, err = computeFacets(ctx, tx, base, search.Facets, catalog.Bundle)
	if err != nil {
		return result, err
	}
	if search.Limit > 0 {
		result.Records, result.HasNext, result.HasPrev, err = pageRecords(ctx, tx, base, columns, search.Position, search.Limit)
		if err != nil {
			return result, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func recordWhere(catalogID string, search storeapi.Search, queryables map[string]storeapi.Queryable) (sqlBuilder, error) {
	b := sqlBuilder{}
	b.add("r.catalog_id=" + b.arg(catalogID))
	if err := addSharedPredicates(&b, search, queryables, false); err != nil {
		return b, err
	}
	return b, nil
}

func addSharedPredicates(b *sqlBuilder, search storeapi.Search, queryables map[string]storeapi.Queryable, catalog bool) error {
	if len(search.Bbox) > 0 {
		if len(search.Bbox) != 4 && len(search.Bbox) != 6 {
			return fmt.Errorf("bbox must have four or six coordinates")
		}
		minX, minY := search.Bbox[0], search.Bbox[1]
		maxX, maxY := search.Bbox[2], search.Bbox[3]
		if len(search.Bbox) == 6 {
			maxX, maxY = search.Bbox[3], search.Bbox[4]
		}
		if minY < -90 || maxY > 90 || minY > maxY || minX < -180 || minX > 180 || maxX < -180 || maxX > 180 {
			return fmt.Errorf("bbox is outside CRS84 or has invalid bounds")
		}
		if minX <= maxX {
			p1, p2, p3, p4 := b.arg(minX), b.arg(minY), b.arg(maxX), b.arg(maxY)
			b.add("(r.geometry IS NULL OR ST_Intersects(r.geometry, ST_MakeEnvelope(" + p1 + "," + p2 + "," + p3 + "," + p4 + ",4326)))")
		} else {
			p1, p2, p3, p4 := b.arg(minX), b.arg(minY), b.arg(maxY), b.arg(180.0)
			p5, p6 := b.arg(-180.0), b.arg(maxX)
			b.add("(r.geometry IS NULL OR ST_Intersects(r.geometry, ST_MakeEnvelope(" + p1 + "," + p2 + "," + p4 + "," + p3 + ",4326)) OR ST_Intersects(r.geometry, ST_MakeEnvelope(" + p5 + "," + p2 + "," + p6 + "," + p3 + ",4326)))")
		}
	}
	if search.Datetime != "" {
		start, end, err := recorddoc.ParseDatetime(search.Datetime)
		if err != nil {
			return fmt.Errorf("datetime: %w", err)
		}
		p1, p2 := b.arg(start), b.arg(end)
		b.add("(NOT r.has_time OR r.time_range && tstzrange(" + p1 + "::timestamptz," + p2 + "::timestamptz,'[]'))")
	}
	if len(search.Q) > 0 {
		terms := make([]string, 0, len(search.Q))
		for _, value := range search.Q {
			value = strings.TrimSpace(value)
			if value != "" {
				terms = append(terms, "phraseto_tsquery('simple',"+b.arg(value)+")")
			}
		}
		if len(terms) > 0 {
			b.add("r.search_vector @@ (" + strings.Join(terms, " || ") + ")")
		}
	}
	if len(search.Types) > 0 {
		path := "r.document #>> '{properties,type}'"
		if catalog {
			path = "r.document->>'type'"
		}
		b.add(path + " = ANY(" + b.arg(search.Types) + "::text[])")
	}
	if len(search.IDs) > 0 {
		b.add("r.id = ANY(" + b.arg(search.IDs) + "::text[])")
	}
	if len(search.ExternalIDs) > 0 {
		if catalog {
			b.add("r.external_ids && " + b.arg(search.ExternalIDs) + "::text[]")
		} else {
			b.add("EXISTS(SELECT 1 FROM record_external_ids e WHERE e.catalog_id=r.catalog_id AND e.record_id=r.id AND (e.external_id=ANY(" + b.arg(search.ExternalIDs) + "::text[]) OR e.value=ANY(" + b.arg(search.ExternalIDs) + "::text[])))")
		}
	}
	if search.Filter != "" {
		fragment, err := cql.Compile(search.Filter, queryables)
		if err != nil {
			return err
		}
		b.add("(" + cql.ShiftPlaceholders(fragment.SQL, len(b.args)) + ")")
		b.args = append(b.args, fragment.Args...)
	}
	return nil
}

func normalizeSort(requested, defaults []storeapi.SortField, allowed map[string]storeapi.Sortable) ([]sortColumn, error) {
	fields := requested
	if len(fields) == 0 {
		fields = defaults
	}
	if len(fields) == 0 {
		fields = []storeapi.SortField{{Property: "id", Direction: "asc"}}
	}
	if len(fields) > 8 {
		return nil, fmt.Errorf("at most eight sort fields are supported")
	}
	columns := make([]sortColumn, 0, len(fields)+1)
	hasID := false
	for _, field := range fields {
		field.Direction = strings.ToLower(field.Direction)
		if field.Direction == "" {
			field.Direction = "asc"
		}
		if field.Direction != "asc" && field.Direction != "desc" {
			return nil, fmt.Errorf("invalid sort direction for %q", field.Property)
		}
		definition, ok := allowed[field.Property]
		if !ok {
			return nil, fmt.Errorf("property %q is not sortable", field.Property)
		}
		expression, err := cql.PropertySQL(field.Property, storeapi.Queryable{Type: definition.Type, Format: definition.Format, Path: definition.Path})
		if err != nil {
			return nil, err
		}
		columns = append(columns, sortColumn{Field: field, Expression: expression, Cast: castFor(definition.Type, definition.Format)})
		if field.Property == "id" {
			hasID = true
		}
	}
	if !hasID {
		columns = append(columns, sortColumn{Field: storeapi.SortField{Property: "id", Direction: "asc"}, Expression: "r.id", Cast: "text"})
	}
	return columns, nil
}

func sortFields(columns []sortColumn) []storeapi.SortField {
	result := make([]storeapi.SortField, len(columns))
	for i := range columns {
		result[i] = columns[i].Field
	}
	return result
}
func castFor(typ, format string) string {
	switch typ {
	case "number":
		return "numeric"
	case "integer":
		return "bigint"
	case "boolean":
		return "boolean"
	case "string":
		if format == "date-time" {
			return "timestamptz"
		}
		if format == "date" {
			return "date"
		}
	}
	return "text"
}

func pageRecords(ctx context.Context, tx pgx.Tx, base sqlBuilder, columns []sortColumn, position *storeapi.CursorPosition, limit int) ([]storeapi.StoredRecord, bool, bool, error) {
	b := base.clone()
	effective, nullsLast := effectiveSort(columns, position)
	if position != nil {
		predicate, err := keysetPredicate(&b, effective, nullsLast, position.Values)
		if err != nil {
			return nil, false, false, err
		}
		b.add(predicate)
	}
	selects := []string{"r.id", "r.document", "r.version", "r.created_at", "r.updated_at"}
	for _, column := range columns {
		selects = append(selects, "("+column.Expression+")::text")
	}
	orders := make([]string, len(effective))
	for i, column := range effective {
		nulls := "NULLS LAST"
		if !nullsLast {
			nulls = "NULLS FIRST"
		}
		orders[i] = column.Expression + " " + strings.ToUpper(column.Field.Direction) + " " + nulls
	}
	query := "SELECT " + strings.Join(selects, ",") + " FROM records r WHERE " + b.where() + " ORDER BY " + strings.Join(orders, ",") + " LIMIT " + b.arg(limit+1)
	rows, err := tx.Query(ctx, query, b.args...)
	if err != nil {
		return nil, false, false, err
	}
	defer rows.Close()
	result := make([]storeapi.StoredRecord, 0, limit+1)
	for rows.Next() {
		var item storeapi.StoredRecord
		values := make([]any, len(columns))
		dest := []any{&item.ID, &item.Document, &item.Version, &item.CreatedAt, &item.UpdatedAt}
		for i := range values {
			dest = append(dest, &values[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, false, false, err
		}
		item.SortValues = normalizeCursorValues(values)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, false, err
	}
	more := len(result) > limit
	if more {
		result = result[:limit]
	}
	hasNext, hasPrev := more, false
	if position != nil {
		if position.Direction == storeapi.CursorNext {
			hasPrev = true
		} else {
			hasNext = true
			hasPrev = more
			reverseRecords(result)
		}
	}
	return result, hasNext, hasPrev, nil
}

func effectiveSort(columns []sortColumn, position *storeapi.CursorPosition) ([]sortColumn, bool) {
	if position == nil || position.Direction == storeapi.CursorNext {
		return columns, true
	}
	result := make([]sortColumn, len(columns))
	copy(result, columns)
	for i := range result {
		if result[i].Field.Direction == "asc" {
			result[i].Field.Direction = "desc"
		} else {
			result[i].Field.Direction = "asc"
		}
	}
	return result, false
}

func keysetPredicate(b *sqlBuilder, columns []sortColumn, nullsLast bool, values []any) (string, error) {
	if len(values) != len(columns) {
		return "", fmt.Errorf("cursor has %d values; expected %d", len(values), len(columns))
	}
	branches := make([]string, 0, len(columns))
	prefix := make([]string, 0, len(columns))
	for i, column := range columns {
		value := values[i]
		comparison := ""
		if value == nil {
			if !nullsLast {
				comparison = column.Expression + " IS NOT NULL"
			}
		} else {
			placeholder := b.arg(value) + "::" + column.Cast
			op := ">"
			if column.Field.Direction == "desc" {
				op = "<"
			}
			comparison = column.Expression + " " + op + " " + placeholder
			if nullsLast {
				comparison = "(" + comparison + " OR " + column.Expression + " IS NULL)"
			}
		}
		if comparison != "" {
			branch := comparison
			if len(prefix) > 0 {
				branch = "(" + strings.Join(prefix, " AND ") + " AND " + comparison + ")"
			}
			branches = append(branches, branch)
		}
		if i < len(columns)-1 {
			if value == nil {
				prefix = append(prefix, column.Expression+" IS NULL")
			} else {
				placeholder := b.arg(value) + "::" + column.Cast
				prefix = append(prefix, column.Expression+" IS NOT DISTINCT FROM "+placeholder)
			}
		}
	}
	if len(branches) == 0 {
		return "FALSE", nil
	}
	return "(" + strings.Join(branches, " OR ") + ")", nil
}

func normalizeCursorValues(values []any) []any {
	result := make([]any, len(values))
	for i, value := range values {
		switch v := value.(type) {
		case time.Time:
			result[i] = v.UTC().Format(time.RFC3339Nano)
		default:
			result[i] = v
		}
	}
	return result
}
func reverseRecords(items []storeapi.StoredRecord) {
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
}

type requestedFacet struct {
	Name  string
	Count int
	Sort  string
}

func selectFacets(raw *string, definitions map[string]storeapi.FacetDefinition) ([]requestedFacet, error) {
	if raw == nil {
		result := []requestedFacet{}
		names := sortedFacetNames(definitions)
		for _, name := range names {
			d := definitions[name]
			if d.Default {
				result = append(result, requestedFacet{Name: name, Count: d.BucketCount, Sort: defaultFacetSort(d)})
			}
		}
		return result, nil
	}
	if *raw == "" {
		return nil, nil
	}
	seen := map[string]bool{}
	result := []requestedFacet{}
	for _, token := range strings.Split(*raw, ",") {
		if token == "" {
			continue
		}
		parts := strings.Split(token, ":")
		if len(parts) > 3 || parts[0] == "" {
			return nil, fmt.Errorf("invalid facets parameter")
		}
		d, ok := definitions[parts[0]]
		if !ok {
			return nil, fmt.Errorf("unknown facet %q", parts[0])
		}
		if seen[parts[0]] {
			return nil, fmt.Errorf("facet %q requested more than once", parts[0])
		}
		seen[parts[0]] = true
		count := d.BucketCount
		if len(parts) > 1 && parts[1] != "" {
			n, err := strconv.Atoi(parts[1])
			if err != nil || n < 1 || n > 1000 {
				return nil, fmt.Errorf("invalid bucket count for facet %q", parts[0])
			}
			count = n
		}
		ordering := defaultFacetSort(d)
		if len(parts) > 2 && parts[2] != "" {
			ordering = parts[2]
		}
		switch ordering {
		case "value_asc", "value_desc", "count_asc", "count_desc":
		default:
			return nil, fmt.Errorf("invalid ordering for facet %q", parts[0])
		}
		result = append(result, requestedFacet{Name: parts[0], Count: count, Sort: ordering})
	}
	return result, nil
}
func sortedFacetNames(definitions map[string]storeapi.FacetDefinition) []string {
	result := make([]string, 0, len(definitions))
	for name := range definitions {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
func defaultFacetSort(d storeapi.FacetDefinition) string {
	if d.SortedBy == "value" {
		return "value_asc"
	}
	return "count_desc"
}

func computeFacets(ctx context.Context, tx pgx.Tx, base sqlBuilder, raw *string, bundle storeapi.CatalogBundle) (map[string]storeapi.FacetResult, error) {
	requests, err := selectFacets(raw, bundle.Facets)
	if err != nil {
		return nil, err
	}
	if len(requests) == 0 {
		return nil, nil
	}
	result := make(map[string]storeapi.FacetResult, len(requests))
	for _, request := range requests {
		definition := bundle.Facets[request.Name]
		var facet storeapi.FacetResult
		switch definition.Type {
		case storeapi.FacetTerm:
			facet, err = termFacet(ctx, tx, base, request, definition, bundle.Queryables)
		case storeapi.FacetHistogram:
			facet, err = histogramFacet(ctx, tx, base, request, definition, bundle.Queryables)
		case storeapi.FacetFilter:
			facet, err = filterFacet(ctx, tx, base, request, definition, bundle.Queryables)
		}
		if err != nil {
			return nil, fmt.Errorf("facet %s: %w", request.Name, err)
		}
		result[request.Name] = facet
	}
	return result, nil
}

func termFacet(ctx context.Context, tx pgx.Tx, base sqlBuilder, request requestedFacet, definition storeapi.FacetDefinition, queryables map[string]storeapi.Queryable) (storeapi.FacetResult, error) {
	q := queryables[definition.Property]
	expr, err := cql.PropertySQL(definition.Property, q)
	if err != nil {
		return storeapi.FacetResult{}, err
	}
	b := base.clone()
	valueSQL := expr
	from := "records r"
	if q.Type == "array" || q.Items != nil {
		valueSQL = "facet_value.value"
		from += " CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(" + expr + ",'[]'::jsonb)) AS facet_value(value)"
	}
	order := facetOrder(request.Sort, "value")
	query := "SELECT to_jsonb(" + valueSQL + ") AS value,count(*) AS bucket_count FROM " + from + " WHERE " + b.where() + " AND " + valueSQL + " IS NOT NULL GROUP BY " + valueSQL + " HAVING count(*) >= " + b.arg(definition.MinOccurs) + " ORDER BY " + order + " LIMIT " + b.arg(request.Count+1)
	rows, err := tx.Query(ctx, query, b.args...)
	if err != nil {
		return storeapi.FacetResult{}, err
	}
	defer rows.Close()
	buckets := []storeapi.FacetBucket{}
	for rows.Next() {
		var raw []byte
		var count int64
		if err := rows.Scan(&raw, &count); err != nil {
			return storeapi.FacetResult{}, err
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return storeapi.FacetResult{}, err
		}
		buckets = append(buckets, storeapi.FacetBucket{Value: value, Count: count})
	}
	more := len(buckets) > request.Count
	if more {
		buckets = buckets[:request.Count]
	}
	return storeapi.FacetResult{Type: storeapi.FacetTerm, Property: definition.Property, Buckets: buckets, More: &more}, rows.Err()
}
func facetOrder(value, column string) string {
	switch value {
	case "value_desc":
		return column + " DESC NULLS LAST"
	case "count_asc":
		return "bucket_count ASC, " + column + " ASC NULLS LAST"
	case "count_desc":
		return "bucket_count DESC, " + column + " ASC NULLS LAST"
	default:
		return column + " ASC NULLS LAST"
	}
}

func histogramFacet(ctx context.Context, tx pgx.Tx, base sqlBuilder, request requestedFacet, definition storeapi.FacetDefinition, queryables map[string]storeapi.Queryable) (storeapi.FacetResult, error) {
	q := queryables[definition.Property]
	expr, err := cql.PropertySQL(definition.Property, q)
	if err != nil {
		return storeapi.FacetResult{}, err
	}
	if q.Format == "date-time" {
		return temporalHistogram(ctx, tx, base, request, definition, expr)
	}
	return numericHistogram(ctx, tx, base, request, definition, expr)
}

func numericHistogram(ctx context.Context, tx pgx.Tx, base sqlBuilder, request requestedFacet, definition storeapi.FacetDefinition, expr string) (storeapi.FacetResult, error) {
	b := base.clone()
	var min, max *float64
	if err := tx.QueryRow(ctx, "SELECT min("+expr+")::float8,max("+expr+")::float8 FROM records r WHERE "+b.where(), b.args...).Scan(&min, &max); err != nil {
		return storeapi.FacetResult{}, err
	}
	facet := storeapi.FacetResult{Type: storeapi.FacetHistogram, Property: definition.Property, Buckets: []storeapi.FacetBucket{}}
	if min == nil || max == nil {
		return facet, nil
	}
	var interval, anchor float64
	if definition.BucketType == storeapi.BucketFixedInterval {
		if err := json.Unmarshal(definition.Interval, &interval); err != nil {
			return facet, err
		}
		anchor = math.Floor(*min/interval) * interval
	} else {
		if *min == *max {
			var count int64
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM records r WHERE "+b.where()+" AND "+expr+" IS NOT NULL", b.args...).Scan(&count); err != nil {
				return facet, err
			}
			if count >= definition.MinOccurs {
				facet.Buckets = []storeapi.FacetBucket{{Min: *min, Max: *max, Count: count}}
			}
			return facet, nil
		}
		interval = (*max - *min) / float64(request.Count)
		anchor = *min
	}
	idxExpr := "floor((" + expr + "-" + b.arg(anchor) + ")/" + b.arg(interval) + ")::bigint"
	if definition.BucketType == storeapi.BucketFixedCount {
		idxExpr = "LEAST(" + idxExpr + "," + strconv.Itoa(request.Count-1) + ")"
	}
	query := "SELECT " + idxExpr + " bucket,count(*) bucket_count FROM records r WHERE " + b.where() + " AND " + expr + " IS NOT NULL GROUP BY bucket HAVING count(*) >= " + b.arg(definition.MinOccurs) + " ORDER BY " + facetOrder(request.Sort, "bucket") + " LIMIT " + b.arg(request.Count+1)
	rows, err := tx.Query(ctx, query, b.args...)
	if err != nil {
		return facet, err
	}
	defer rows.Close()
	for rows.Next() {
		var idx, count int64
		if err := rows.Scan(&idx, &count); err != nil {
			return facet, err
		}
		low := anchor + float64(idx)*interval
		high := low + interval
		if definition.BucketType == storeapi.BucketFixedCount && high > *max {
			high = *max
		}
		facet.Buckets = append(facet.Buckets, storeapi.FacetBucket{Min: low, Max: high, Count: count})
	}
	more := len(facet.Buckets) > request.Count
	if more {
		facet.Buckets = facet.Buckets[:request.Count]
	}
	facet.More = &more
	return facet, rows.Err()
}

func temporalHistogram(ctx context.Context, tx pgx.Tx, base sqlBuilder, request requestedFacet, definition storeapi.FacetDefinition, expr string) (storeapi.FacetResult, error) {
	if definition.BucketType == storeapi.BucketFixedInterval {
		var interval string
		if err := json.Unmarshal(definition.Interval, &interval); err != nil {
			return storeapi.FacetResult{}, err
		}
		b := base.clone()
		p := b.arg(interval)
		bucket := "date_bin(" + p + "::interval," + expr + ",TIMESTAMPTZ '1970-01-01T00:00:00Z')"
		query := "SELECT " + bucket + " bucket," + bucket + "+" + p + "::interval bucket_end,count(*) bucket_count FROM records r WHERE " + b.where() + " AND " + expr + " IS NOT NULL GROUP BY bucket,bucket_end HAVING count(*) >= " + b.arg(definition.MinOccurs) + " ORDER BY " + facetOrder(request.Sort, "bucket") + " LIMIT " + b.arg(request.Count+1)
		rows, err := tx.Query(ctx, query, b.args...)
		if err != nil {
			return storeapi.FacetResult{}, err
		}
		defer rows.Close()
		facet := storeapi.FacetResult{Type: storeapi.FacetHistogram, Property: definition.Property, Buckets: []storeapi.FacetBucket{}}
		for rows.Next() {
			var low, high time.Time
			var count int64
			if err := rows.Scan(&low, &high, &count); err != nil {
				return facet, err
			}
			facet.Buckets = append(facet.Buckets, storeapi.FacetBucket{Min: low.UTC().Format(time.RFC3339Nano), Max: high.UTC().Format(time.RFC3339Nano), Count: count})
		}
		more := len(facet.Buckets) > request.Count
		if more {
			facet.Buckets = facet.Buckets[:request.Count]
		}
		facet.More = &more
		return facet, rows.Err()
	}
	// Fixed bucket count uses epoch seconds but reports ISO timestamps.
	epoch := "extract(epoch from " + expr + ")"
	numeric, err := numericHistogram(ctx, tx, base, request, definition, epoch)
	if err != nil {
		return numeric, err
	}
	for i := range numeric.Buckets {
		low, _ := asFloat(numeric.Buckets[i].Min)
		high, _ := asFloat(numeric.Buckets[i].Max)
		numeric.Buckets[i].Min = time.Unix(0, int64(low*1e9)).UTC().Format(time.RFC3339Nano)
		numeric.Buckets[i].Max = time.Unix(0, int64(high*1e9)).UTC().Format(time.RFC3339Nano)
	}
	return numeric, nil
}
func asFloat(value any) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, true
	case int64:
		return float64(value), true
	}
	return 0, false
}

func filterFacet(ctx context.Context, tx pgx.Tx, base sqlBuilder, request requestedFacet, definition storeapi.FacetDefinition, queryables map[string]storeapi.Queryable) (storeapi.FacetResult, error) {
	result := storeapi.FacetResult{Type: storeapi.FacetFilter, Property: definition.Property, Buckets: []storeapi.FacetBucket{}}
	for name, filter := range definition.Filters {
		fragment, err := cql.Compile(filter, queryables)
		if err != nil {
			return result, err
		}
		b := base.clone()
		b.add("(" + cql.ShiftPlaceholders(fragment.SQL, len(b.args)) + ")")
		b.args = append(b.args, fragment.Args...)
		var count int64
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM records r WHERE "+b.where(), b.args...).Scan(&count); err != nil {
			return result, err
		}
		if count >= definition.MinOccurs {
			result.Buckets = append(result.Buckets, storeapi.FacetBucket{Value: name, Count: count})
		}
	}
	sort.Slice(result.Buckets, func(i, j int) bool {
		a, b := result.Buckets[i], result.Buckets[j]
		switch request.Sort {
		case "count_asc":
			if a.Count != b.Count {
				return a.Count < b.Count
			}
		case "count_desc":
			if a.Count != b.Count {
				return a.Count > b.Count
			}
		case "value_desc":
			return fmt.Sprint(a.Value) > fmt.Sprint(b.Value)
		}
		return fmt.Sprint(a.Value) < fmt.Sprint(b.Value)
	})
	more := len(result.Buckets) > request.Count
	if more {
		result.Buckets = result.Buckets[:request.Count]
	}
	result.More = &more
	return result, nil
}

func (p *Store) ListCatalogs(ctx context.Context, search storeapi.Search) (storeapi.CatalogSearchResult, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return storeapi.CatalogSearchResult{}, err
	}
	defer tx.Rollback(ctx)
	queryables := catalogQueryables()
	sortables := map[string]storeapi.Sortable{}
	for name, q := range queryables {
		sortables[name] = storeapi.Sortable{Title: q.Title, Type: q.Type, Format: q.Format, Path: q.Path}
	}
	columns, err := normalizeSort(search.Sort, []storeapi.SortField{{Property: "id", Direction: "asc"}}, sortables)
	if err != nil {
		return storeapi.CatalogSearchResult{}, err
	}
	b := sqlBuilder{}
	if err := addSharedPredicates(&b, search, queryables, true); err != nil {
		return storeapi.CatalogSearchResult{}, err
	}
	var result storeapi.CatalogSearchResult
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM catalogs r WHERE "+b.where(), b.args...).Scan(&result.NumberMatched); err != nil {
		return result, err
	}
	if search.Limit == 0 {
		if err := tx.Commit(ctx); err != nil {
			return result, err
		}
		return result, nil
	}
	effective, nullsLast := effectiveSort(columns, search.Position)
	if search.Position != nil {
		predicate, err := keysetPredicate(&b, effective, nullsLast, search.Position.Values)
		if err != nil {
			return result, err
		}
		b.add(predicate)
	}
	selects := []string{"r.id", "r.document", "r.queryables", "r.sortables", "r.default_sort", "r.facets", "r.record_schema", "r.created_at", "r.updated_at"}
	for _, column := range columns {
		selects = append(selects, "("+column.Expression+")::text")
	}
	orders := make([]string, len(effective))
	for i, column := range effective {
		nulls := "NULLS LAST"
		if !nullsLast {
			nulls = "NULLS FIRST"
		}
		orders[i] = column.Expression + " " + strings.ToUpper(column.Field.Direction) + " " + nulls
	}
	query := "SELECT " + strings.Join(selects, ",") + " FROM catalogs r WHERE " + b.where() + " ORDER BY " + strings.Join(orders, ",") + " LIMIT " + b.arg(search.Limit+1)
	rows, err := tx.Query(ctx, query, b.args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item storeapi.Catalog
		var doc, queryableJSON, sortableJSON, defaultJSON, facetsJSON, schema []byte
		values := make([]any, len(columns))
		dest := []any{&item.ID, &doc, &queryableJSON, &sortableJSON, &defaultJSON, &facetsJSON, &schema, &item.CreatedAt, &item.UpdatedAt}
		for i := range values {
			dest = append(dest, &values[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return result, err
		}
		item.Bundle.Catalog = doc
		item.Bundle.Schema = schema
		if err := json.Unmarshal(queryableJSON, &item.Bundle.Queryables); err != nil {
			return result, err
		}
		if err := json.Unmarshal(sortableJSON, &item.Bundle.Sortables); err != nil {
			return result, err
		}
		if err := json.Unmarshal(defaultJSON, &item.Bundle.DefaultSortOrder); err != nil {
			return result, err
		}
		if err := json.Unmarshal(facetsJSON, &item.Bundle.Facets); err != nil {
			return result, err
		}
		result.Catalogs = append(result.Catalogs, item)
		result.SortValues = append(result.SortValues, normalizeCursorValues(values))
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	more := len(result.Catalogs) > search.Limit
	if more {
		result.Catalogs = result.Catalogs[:search.Limit]
		result.SortValues = result.SortValues[:search.Limit]
	}
	result.HasNext = more
	if search.Position != nil {
		if search.Position.Direction == storeapi.CursorNext {
			result.HasPrev = true
		} else {
			result.HasNext = true
			result.HasPrev = more
			reverseCatalogs(result.Catalogs, result.SortValues)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func catalogQueryables() map[string]storeapi.Queryable {
	return map[string]storeapi.Queryable{"id": {Type: "string", Path: "/id"}, "type": {Type: "string", Path: "/type"}, "title": {Type: "string", Path: "/title"}, "description": {Type: "string", Path: "/description"}, "created": {Type: "string", Format: "date-time", Path: "/properties/created"}, "updated": {Type: "string", Format: "date-time", Path: "/properties/updated"}}
}
func reverseCatalogs(items []storeapi.Catalog, values [][]any) {
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
		values[left], values[right] = values[right], values[left]
	}
}
