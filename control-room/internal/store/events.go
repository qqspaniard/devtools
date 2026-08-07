package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Event is one row of the append-only audit history, projected for display.
type Event struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	Revision  int       `json:"revision,omitempty"`
	Actor     Actor     `json:"actor"`
	Type      string    `json:"type"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

// MaxEventsQuery bounds how many events a single query returns, so a long-lived
// session cannot force an unbounded read.
const MaxEventsQuery = 500

// Events returns the most recent events for a session, oldest-first, bounded by
// MaxEventsQuery. It is read-only and used by the review page's timeline.
func (s *Store) Events(sessionID string, limit int) ([]Event, error) {
	if limit <= 0 || limit > MaxEventsQuery {
		limit = MaxEventsQuery
	}
	rows, err := s.db.Query(
		`SELECT id, session_id, revision, actor, type, payload, created_at
		 FROM events WHERE session_id = ? ORDER BY id ASC LIMIT ?`,
		sessionID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: querying events: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var (
			e       Event
			rev     sql.NullInt64
			actor   string
			created string
		)
		if err := rows.Scan(&e.ID, &e.SessionID, &rev, &actor, &e.Type, &e.Payload, &created); err != nil {
			return nil, fmt.Errorf("store: scanning event: %w", err)
		}
		if rev.Valid {
			e.Revision = int(rev.Int64)
		}
		e.Actor = Actor(actor)
		e.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating events: %w", err)
	}
	return out, nil
}
