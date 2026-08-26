package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type MigrationStatus struct {
	Version int
	Name    string
	Applied bool
}

func (p *Store) Migrate(ctx context.Context) error {
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('tlon-schema-migrations'))`); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('tlon-schema-migrations'))`)
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS tlon_schema_migrations (version integer PRIMARY KEY, name text NOT NULL, applied_at timestamptz NOT NULL DEFAULT statement_timestamp())`); err != nil {
		return err
	}
	entries, err := migrations()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		var applied bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tlon_schema_migrations WHERE version=$1)`, entry.version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return err
		}
		body, err := fs.ReadFile(migrationFS, entry.path)
		if err == nil {
			_, err = tx.Exec(ctx, string(body))
		}
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO tlon_schema_migrations(version,name) VALUES($1,$2)`, entry.version, entry.name)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", entry.name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (p *Store) MigrationStatus(ctx context.Context) ([]MigrationStatus, error) {
	entries, err := migrations()
	if err != nil {
		return nil, err
	}
	var current map[int]bool = map[int]bool{}
	rows, err := p.pool.Query(ctx, `SELECT version FROM tlon_schema_migrations ORDER BY version`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var version int
			if err := rows.Scan(&version); err != nil {
				return nil, err
			}
			current[version] = true
		}
	} else if !strings.Contains(err.Error(), "does not exist") {
		return nil, err
	}
	result := make([]MigrationStatus, 0, len(entries))
	for _, entry := range entries {
		result = append(result, MigrationStatus{Version: entry.version, Name: entry.name, Applied: current[entry.version]})
	}
	return result, nil
}

type migration struct {
	version    int
	name, path string
}

func migrations() ([]migration, error) {
	paths, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	result := make([]migration, 0, len(paths))
	for _, path := range paths {
		base := strings.TrimSuffix(strings.TrimPrefix(path, "migrations/"), ".sql")
		parts := strings.SplitN(base, "_", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid migration name %s", path)
		}
		version, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, err
		}
		result = append(result, migration{version: version, name: parts[1], path: path})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].version < result[j].version })
	return result, nil
}
