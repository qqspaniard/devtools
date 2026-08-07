package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migration is one versioned schema step. Migrations are embedded so the single
// binary carries its own schema; there is no external migrations directory to
// ship or lose.
type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads and parses the embedded migration files. Each file is
// named NNNN_description.sql where NNNN is a zero-padded positive version. The
// set must be gap-free starting at 1; a gap is a build-time authoring error and
// fails closed.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("store: reading embedded migrations: %w", err)
	}
	migs := make([]migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		verStr, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("store: migration %q not named NNNN_description.sql", e.Name())
		}
		ver, err := strconv.Atoi(verStr)
		if err != nil || ver < 1 {
			return nil, fmt.Errorf("store: migration %q has invalid version prefix", e.Name())
		}
		body, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("store: reading migration %q: %w", e.Name(), err)
		}
		migs = append(migs, migration{version: ver, name: e.Name(), sql: string(body)})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })
	for i, m := range migs {
		if m.version != i+1 {
			return nil, fmt.Errorf("store: migration versions must be gap-free from 1; got %d at position %d", m.version, i+1)
		}
	}
	if len(migs) == 0 {
		return nil, fmt.Errorf("store: no embedded migrations found")
	}
	return migs, nil
}

// schemaVersion returns the highest applied migration version, or 0 if the
// schema_migrations table does not yet exist (a fresh database).
func schemaVersion(db *sql.DB) (int, error) {
	var name string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='schema_migrations'`,
	).Scan(&name)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: checking schema_migrations existence: %w", err)
	}
	var v sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("store: reading schema version: %w", err)
	}
	if !v.Valid {
		return 0, nil
	}
	return int(v.Int64), nil
}

// migrate applies all pending migrations in a single transaction per migration.
// It fails closed if the database is at a HIGHER version than this build knows
// (a downgrade), rather than attempting any destructive reset.
func migrate(db *sql.DB) error {
	migs, err := loadMigrations()
	if err != nil {
		return err
	}
	latest := migs[len(migs)-1].version

	current, err := schemaVersion(db)
	if err != nil {
		return err
	}
	if current > latest {
		return fmt.Errorf(
			"store: database schema version %d is newer than this build supports (%d); refusing to run (no automatic downgrade or reset)",
			current, latest)
	}
	if current == latest {
		return nil
	}

	for _, m := range migs {
		if m.version <= current {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("store: begin migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: applying migration %d (%s): %w", m.version, m.name, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
			m.version, time.Now().UTC().Format(time.RFC3339Nano),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: recording migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: commit migration %d: %w", m.version, err)
		}
	}
	return nil
}
