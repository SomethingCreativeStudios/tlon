package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/SomethingCreativeStudios/tlon/internal/cql"
	storeapi "github.com/SomethingCreativeStudios/tlon/store"
)

const catalogIndexAdvisoryLock = "tlon-catalog-indexes"

var genericIndexedProperties = map[string]struct{}{
	"id":      {},
	"type":    {},
	"title":   {},
	"created": {},
	"updated": {},
}

type physicalIndexKey struct {
	Expression string `json:"expression"`
	Direction  string `json:"direction"`
}

type physicalIndexDefinition struct {
	Relation recordRelation     `json:"relation"`
	Keys     []physicalIndexKey `json:"keys"`
}

type catalogIndexSpec struct {
	Signature    string
	IndexName    string
	StorageClass string
	Definition   json.RawMessage
	Automatic    bool
	CreateSQL    string
}

// CatalogIndexStatus describes a managed physical index used by a catalog.
type CatalogIndexStatus struct {
	Signature  string          `json:"signature"`
	Name       string          `json:"name"`
	Automatic  bool            `json:"automatic"`
	Ready      bool            `json:"ready"`
	Definition json.RawMessage `json:"definition"`
}

func desiredCatalogIndexes(bundle storeapi.CatalogBundle) (map[string]catalogIndexSpec, error) {
	relation, err := relationFor(bundle.Storage.Class)
	if err != nil {
		return nil, err
	}
	desired := map[string]catalogIndexSpec{}
	if bundle.Storage.AutoFacetIndexes != nil && *bundle.Storage.AutoFacetIndexes {
		properties := make(map[string]struct{})
		for _, facet := range bundle.Facets {
			if facet.Type != storeapi.FacetTerm && facet.Type != storeapi.FacetHistogram {
				continue
			}
			queryable, exists := bundle.Queryables[facet.Property]
			if !exists || queryable.Type == "array" || queryable.Items != nil {
				continue
			}
			if _, generic := genericIndexedProperties[facet.Property]; generic {
				continue
			}
			properties[facet.Property] = struct{}{}
		}
		names := make([]string, 0, len(properties))
		for property := range properties {
			names = append(names, property)
		}
		sort.Strings(names)
		for _, property := range names {
			spec, err := buildCatalogIndexSpec(relation, bundle.Storage.Class, []storeapi.IndexKey{{Property: property, Direction: "asc"}}, bundle.Queryables, true)
			if err != nil {
				return nil, err
			}
			desired[spec.Signature] = spec
		}
	}
	for _, definition := range bundle.Storage.Indexes {
		spec, err := buildCatalogIndexSpec(relation, bundle.Storage.Class, definition.Keys, bundle.Queryables, false)
		if err != nil {
			return nil, fmt.Errorf("storage index %q: %w", definition.Name, err)
		}
		// An explicit declaration wins when it is identical to an automatic
		// facet index, while still sharing the same physical object.
		desired[spec.Signature] = spec
	}
	return desired, nil
}

func buildCatalogIndexSpec(relation recordRelation, storageClass string, keys []storeapi.IndexKey, queryables map[string]storeapi.Queryable, automatic bool) (catalogIndexSpec, error) {
	definition := physicalIndexDefinition{Relation: relation, Keys: make([]physicalIndexKey, 0, len(keys))}
	for _, key := range keys {
		queryable, exists := queryables[key.Property]
		if !exists {
			return catalogIndexSpec{}, fmt.Errorf("unknown queryable %q", key.Property)
		}
		expression, err := cql.IndexSQL(key.Property, queryable)
		if err != nil {
			return catalogIndexSpec{}, fmt.Errorf("queryable %q: %w", key.Property, err)
		}
		direction := strings.ToUpper(key.Direction)
		if direction == "" {
			direction = "ASC"
		}
		definition.Keys = append(definition.Keys, physicalIndexKey{Expression: expression, Direction: direction})
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		return catalogIndexSpec{}, err
	}
	digest := sha256.Sum256(encoded)
	signature := hex.EncodeToString(digest[:])
	prefix := "tlon_rx_"
	if relation == recordsTemporal {
		prefix = "tlon_rt_"
	}
	indexName := prefix + signature[:20] + "_idx"
	columns := []string{"catalog_id"}
	for _, key := range definition.Keys {
		columns = append(columns, "("+key.Expression+") "+key.Direction)
	}
	createSQL := "CREATE INDEX " + pgx.Identifier{indexName}.Sanitize() +
		" ON " + pgx.Identifier{string(relation)}.Sanitize() + " (" + strings.Join(columns, ", ") + ")"
	return catalogIndexSpec{
		Signature:    signature,
		IndexName:    indexName,
		StorageClass: storageClass,
		Definition:   encoded,
		Automatic:    automatic,
		CreateSQL:    createSQL,
	}, nil
}

func reconcileCatalogIndexes(ctx context.Context, tx pgx.Tx, catalogID string, bundle storeapi.CatalogBundle) error {
	desired, err := desiredCatalogIndexes(bundle)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, catalogIndexAdvisoryLock); err != nil {
		return fmt.Errorf("lock catalog indexes: %w", err)
	}
	existing, err := boundCatalogIndexes(ctx, tx, catalogID)
	if err != nil {
		return err
	}
	signatures := make([]string, 0, len(desired))
	for signature := range desired {
		signatures = append(signatures, signature)
	}
	sort.Strings(signatures)
	createdPhysicalIndex := false
	for _, signature := range signatures {
		spec := desired[signature]
		var physicalExists bool
		if err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, spec.IndexName).Scan(&physicalExists); err != nil {
			return fmt.Errorf("inspect catalog index %s: %w", spec.IndexName, err)
		}
		if !physicalExists {
			if _, err := tx.Exec(ctx, spec.CreateSQL); err != nil {
				return fmt.Errorf("create catalog index %s: %w", spec.IndexName, err)
			}
			createdPhysicalIndex = true
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO catalog_indexes(catalog_id, signature, index_name, storage_class, definition, automatic)
			VALUES($1,$2,$3,$4,$5,$6)
			ON CONFLICT(catalog_id,signature) DO UPDATE SET
				index_name=EXCLUDED.index_name, storage_class=EXCLUDED.storage_class,
				definition=EXCLUDED.definition, automatic=EXCLUDED.automatic`,
			catalogID, spec.Signature, spec.IndexName, spec.StorageClass, spec.Definition, spec.Automatic); err != nil {
			return fmt.Errorf("bind catalog index %s: %w", spec.IndexName, err)
		}
		delete(existing, signature)
	}
	for signature, indexName := range existing {
		if _, err := tx.Exec(ctx, `DELETE FROM catalog_indexes WHERE catalog_id=$1 AND signature=$2`, catalogID, signature); err != nil {
			return fmt.Errorf("unbind catalog index %s: %w", indexName, err)
		}
		if err := dropUnreferencedIndex(ctx, tx, indexName); err != nil {
			return err
		}
	}
	if createdPhysicalIndex {
		relation, err := relationFor(bundle.Storage.Class)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "ANALYZE "+pgx.Identifier{string(relation)}.Sanitize()); err != nil {
			return fmt.Errorf("analyze catalog indexes: %w", err)
		}
	}
	return nil
}

func removeCatalogIndexes(ctx context.Context, tx pgx.Tx, catalogID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, catalogIndexAdvisoryLock); err != nil {
		return fmt.Errorf("lock catalog indexes: %w", err)
	}
	existing, err := boundCatalogIndexes(ctx, tx, catalogID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM catalog_indexes WHERE catalog_id=$1`, catalogID); err != nil {
		return fmt.Errorf("remove catalog index bindings: %w", err)
	}
	for _, indexName := range existing {
		if err := dropUnreferencedIndex(ctx, tx, indexName); err != nil {
			return err
		}
	}
	return nil
}

func boundCatalogIndexes(ctx context.Context, tx pgx.Tx, catalogID string) (map[string]string, error) {
	rows, err := tx.Query(ctx, `SELECT signature, index_name FROM catalog_indexes WHERE catalog_id=$1`, catalogID)
	if err != nil {
		return nil, fmt.Errorf("list catalog indexes: %w", err)
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var signature, indexName string
		if err := rows.Scan(&signature, &indexName); err != nil {
			return nil, err
		}
		result[signature] = indexName
	}
	return result, rows.Err()
}

func dropUnreferencedIndex(ctx context.Context, tx pgx.Tx, indexName string) error {
	var referenced bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_indexes WHERE index_name=$1)`, indexName).Scan(&referenced); err != nil {
		return err
	}
	if referenced {
		return nil
	}
	if _, err := tx.Exec(ctx, "DROP INDEX IF EXISTS "+pgx.Identifier{indexName}.Sanitize()); err != nil {
		return fmt.Errorf("drop catalog index %s: %w", indexName, err)
	}
	return nil
}

// CatalogIndexes returns the managed index bindings for operational tooling
// and tests without adding datastore-specific details to the reusable Store
// interface.
func (p *Store) CatalogIndexes(ctx context.Context, catalogID string) ([]CatalogIndexStatus, error) {
	var exists bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalogs WHERE id=$1)`, catalogID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, storeapi.ErrNotFound
	}
	rows, err := p.pool.Query(ctx, `
		SELECT ci.signature, ci.index_name, ci.automatic,
		       COALESCE(ix.indisvalid AND ix.indisready, false), ci.definition
		FROM catalog_indexes ci
		LEFT JOIN pg_index ix ON ix.indexrelid=to_regclass(ci.index_name)
		WHERE ci.catalog_id=$1 ORDER BY ci.index_name`, catalogID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CatalogIndexStatus{}
	for rows.Next() {
		var status CatalogIndexStatus
		if err := rows.Scan(&status.Signature, &status.Name, &status.Automatic, &status.Ready, &status.Definition); err != nil {
			return nil, err
		}
		result = append(result, status)
	}
	return result, rows.Err()
}
