// Package store is Control Room's durable persistence layer: an embedded SQLite
// database with versioned migrations, transactional session-state transitions,
// an append-only event history, and durable single-use approval claims.
//
// SQLite runs inside the process via the pure-Go modernc.org/sqlite driver (no
// CGO). The store owns the connection configuration (WAL, foreign keys, busy
// timeout), the startup integrity/schema checks, and every state-changing
// transaction. Policy decisions (digest construction, transition legality) live
// in internal/policy; the store enforces them durably and atomically.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"

	"github.com/interactionlabs/devtools/control-room/internal/statedir"
)

// Sentinel errors the broker translates into typed protocol responses.
var (
	// ErrNotFound is returned when a session or approval does not exist.
	ErrNotFound = errors.New("store: not found")

	// ErrConflict is returned when a durable uniqueness/replay constraint
	// rejects a mutation (e.g. republishing a revision, a stale-revision
	// decision, or a second terminal decision on a revision).
	ErrConflict = errors.New("store: conflict")

	// ErrStaleRevision is returned when a decision or claim targets a revision
	// that is not the session's current revision. Stale decisions fail closed.
	ErrStaleRevision = errors.New("store: stale plan revision")
)

// Store is a handle to the durable database. It is safe for concurrent use; the
// underlying *sql.DB pools connections and each connection is configured with
// the required pragmas.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Options configures Open.
type Options struct {
	// Path is the database file path. Required.
	Path string
	// Now is the clock used for timestamps and expiration comparisons. If nil,
	// time.Now is used. Tests inject a controllable clock.
	Now func() time.Time
}

// Open opens (creating if absent) the SQLite database at opts.Path, applies
// pending migrations, and verifies integrity. It configures WAL, foreign keys,
// and a busy timeout on every pooled connection.
//
// It fails closed on a database newer than this build's schema and on an
// integrity-check failure, with no automatic destructive reset.
func Open(opts Options) (*Store, error) {
	if opts.Path == "" {
		return nil, fmt.Errorf("store: Open requires a Path")
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	// Create the file with owner-only permissions up front so it is never
	// briefly world/group-readable between creation and a later chmod. If it
	// already exists, tighten it.
	if err := ensureDBFilePerms(opts.Path); err != nil {
		return nil, err
	}

	// modernc.org/sqlite honors PRAGMAs passed as connection query parameters
	// via _pragma. Set busy_timeout, journal_mode=WAL, and foreign_keys here so
	// EVERY pooled connection gets them (a session-level PRAGMA on one
	// connection would not apply to others).
	dsn := "file:" + opts.Path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: opening database: %w", err)
	}

	s := &Store{db: db, now: nowFn}

	if err := s.verifyIntegrity(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Re-assert file permissions after WAL/SHM sidecar files are created.
	if err := ensureDBFilePerms(opts.Path); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// ensureDBFilePerms makes the DB file (and its WAL/SHM sidecars if present)
// owner-only. It creates the main file with 0600 if it does not yet exist.
func ensureDBFilePerms(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE, statedir.DBPerm)
	if err != nil {
		return fmt.Errorf("store: creating database file: %w", err)
	}
	_ = f.Close()
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(p); err == nil {
			if err := os.Chmod(p, statedir.DBPerm); err != nil {
				return fmt.Errorf("store: setting permissions on %q: %w", p, err)
			}
		}
	}
	return nil
}

// verifyIntegrity runs PRAGMA integrity_check and fails closed on any result
// other than the single "ok" row. A corrupt database is a hard, actionable
// failure — never a silent reset.
func (s *Store) verifyIntegrity() error {
	rows, err := s.db.Query("PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("store: integrity check query: %w", err)
	}
	defer rows.Close()
	var results []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return fmt.Errorf("store: scanning integrity result: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: integrity check iteration: %w", err)
	}
	if len(results) != 1 || results[0] != "ok" {
		return fmt.Errorf("store: database failed integrity check: %v (refusing to run; no automatic reset)", results)
	}
	return nil
}

// nowStr returns the current time as an RFC3339Nano UTC string, matching the
// approval-digest expiration encoding so timestamp comparisons are exact.
func (s *Store) nowStr() string {
	return s.now().UTC().Format(time.RFC3339Nano)
}
