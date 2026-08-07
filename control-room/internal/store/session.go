package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/interactionlabs/devtools/control-room/internal/policy"
	"github.com/interactionlabs/devtools/control-room/internal/protocol"
)

// Actor identifies who caused an event. It mirrors the RFC's actor vocabulary.
type Actor string

const (
	ActorUser   Actor = "user"
	ActorAgent  Actor = "agent"
	ActorSystem Actor = "system"
)

// DecisionKind is the durable outcome of a user's review of a revision.
type DecisionKind string

const (
	DecisionApprove        DecisionKind = "approve"
	DecisionReject         DecisionKind = "reject"
	DecisionRequestChanges DecisionKind = "request_changes"
)

// MaxReasonBytes bounds the free-text reason on reject/request_changes so the
// browser cannot store an unbounded blob.
const MaxReasonBytes = 8 * 1024

// Session is the durable projection of a session.
type Session struct {
	ID              string       `json:"id"`
	State           policy.State `json:"state"`
	WorkspaceID     string       `json:"workspace_id"`
	WorkspaceName   string       `json:"workspace_name"`
	CurrentRevision int          `json:"current_revision"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

// Decision is the durable outcome returned by decision poll.
type Decision struct {
	SessionID  string       `json:"session_id"`
	Revision   int          `json:"revision"`
	Kind       DecisionKind `json:"kind"`
	Reason     string       `json:"reason,omitempty"`
	ApprovalID string       `json:"approval_id,omitempty"`
	// Digest is the approval digest an agent presents to claim. It is populated
	// only for approve decisions. Surfacing it here lets `decision poll` hand an
	// adapter everything it needs to claim, without a separate lookup.
	Digest    string    `json:"digest,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// --- session.create ---

// CreateSession inserts a new draft session. The caller supplies the opaque,
// high-entropy session id (the broker generates it). Workspace identity is
// bound at creation. It appends a session.created event in the same
// transaction.
func (s *Store) CreateSession(id, workspaceID, workspaceName string) (*Session, error) {
	if err := validateOpaqueID("session_id", id); err != nil {
		return nil, err
	}
	if err := validateOpaqueID("workspace_id", workspaceID); err != nil {
		return nil, err
	}
	now := s.nowStr()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: begin CreateSession: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(
		`INSERT INTO sessions(id, state, workspace_id, workspace_name, current_revision, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 0, ?, ?)`,
		id, string(policy.StateDraft), workspaceID, workspaceName, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: session %q already exists", ErrConflict, id)
		}
		return nil, fmt.Errorf("store: inserting session: %w", err)
	}
	if err := appendEventTx(tx, id, sql.NullInt64{}, ActorSystem, "session.created",
		map[string]any{"workspace_id": workspaceID}, sql.NullInt64{}, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit CreateSession: %w", err)
	}
	return &Session{
		ID: id, State: policy.StateDraft, WorkspaceID: workspaceID,
		WorkspaceName: workspaceName, CurrentRevision: 0,
	}, nil
}

// GetSession loads a session projection.
func (s *Store) GetSession(id string) (*Session, error) {
	var (
		sess               Session
		state              string
		createdAt, updated string
	)
	err := s.db.QueryRow(
		`SELECT id, state, workspace_id, workspace_name, current_revision, created_at, updated_at
		 FROM sessions WHERE id = ?`, id,
	).Scan(&sess.ID, &state, &sess.WorkspaceID, &sess.WorkspaceName, &sess.CurrentRevision, &createdAt, &updated)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: loading session: %w", err)
	}
	sess.State = policy.State(state)
	sess.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	sess.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &sess, nil
}

// EndSession ends a session administratively, moving it to the terminal
// completed state. Ending is a broker-level operation defined by
// policy.CanAdministrativelyEnd, intentionally OUTSIDE the core Next() matrix
// (see the policy doc comment): any non-terminal session may be ended, and an
// already-terminal session is returned unchanged (idempotent). This is why the
// legality check is CanAdministrativelyEnd rather than a matrix edge.
func (s *Store) EndSession(id string) (*Session, error) {
	now := s.nowStr()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: begin EndSession: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	sess, err := lockSession(tx, id)
	if err != nil {
		return nil, err
	}
	if !policy.CanAdministrativelyEnd(sess.State) {
		// Already terminal (or unknown); return current state without a
		// spurious transition. Ending is idempotent for a closed session.
		return sess, nil
	}
	if _, err := tx.Exec(
		`UPDATE sessions SET state = ?, updated_at = ? WHERE id = ?`,
		string(policy.StateCompleted), now, id,
	); err != nil {
		return nil, fmt.Errorf("store: ending session: %w", err)
	}
	if err := appendEventTx(tx, id, revNull(sess.CurrentRevision), ActorUser, "session.ended",
		map[string]any{"from": string(sess.State), "transition": string(policy.TransitionEnd)}, sql.NullInt64{}, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit EndSession: %w", err)
	}
	sess.State = policy.StateCompleted
	return sess, nil
}

// --- plan.publish ---

// PublishPlan stores a validated plan revision and moves the session into
// awaiting_approval. It enforces the state machine as the source of truth and
// monotonic revision numbering, and supersedes any prior approval for the
// session (a stale revision cannot later be claimed).
//
// Two legal cases, both defined by policy rather than ad-hoc conditionals here:
//
//   - First publish from draft: policy.Next(draft, publish) → awaiting_approval.
//   - Republish (a newer revision superseding a prior one): governed by
//     policy.CanRepublish, which permits any non-terminal, non-running state and
//     lands in policy.RepublishTarget (awaiting_approval). This is a broker-level
//     operation intentionally outside the core Next() matrix (see the policy
//     doc comment); modeling it explicitly keeps the store's behavior and the
//     policy source of truth in agreement.
func (s *Store) PublishPlan(plan *protocol.Plan) (*Session, error) {
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	payload, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("store: marshalling plan: %w", err)
	}
	now := s.nowStr()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: begin PublishPlan: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	sess, err := lockSession(tx, plan.SessionID)
	if err != nil {
		return nil, err
	}
	if sess.WorkspaceID != plan.Workspace.ID {
		return nil, fmt.Errorf("%w: plan workspace %q does not match session workspace %q",
			ErrConflict, plan.Workspace.ID, sess.WorkspaceID)
	}
	if plan.Revision <= sess.CurrentRevision {
		return nil, fmt.Errorf("%w: revision %d not greater than current %d",
			ErrConflict, plan.Revision, sess.CurrentRevision)
	}

	// Resolve the transition through policy. A first publish from draft uses the
	// core matrix; any other legal source uses the explicit republish operation.
	var transition policy.Transition
	var nextState policy.State
	if sess.State == policy.StateDraft {
		nextState, err = policy.Next(policy.StateDraft, policy.TransitionPublish)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}
		transition = policy.TransitionPublish
	} else if policy.CanRepublish(sess.State) {
		nextState = policy.RepublishTarget
		transition = policy.TransitionRepublish
	} else {
		return nil, fmt.Errorf("%w: cannot publish from state %q", ErrConflict, sess.State)
	}

	_, err = tx.Exec(
		`INSERT INTO plan_revisions(session_id, revision, goal, summary, payload, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		plan.SessionID, plan.Revision, plan.Goal, plan.Summary, string(payload), now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: revision %d already published", ErrConflict, plan.Revision)
		}
		return nil, fmt.Errorf("store: inserting plan revision: %w", err)
	}

	// Supersede any prior live approval for this session: once a newer revision
	// is published, older approvals must fail closed on claim.
	if _, err := tx.Exec(
		`UPDATE approvals SET superseded = 1 WHERE session_id = ? AND superseded = 0`,
		plan.SessionID,
	); err != nil {
		return nil, fmt.Errorf("store: superseding prior approvals: %w", err)
	}

	if _, err := tx.Exec(
		`UPDATE sessions SET state = ?, current_revision = ?, workspace_name = ?, updated_at = ? WHERE id = ?`,
		string(nextState), plan.Revision, plan.Workspace.DisplayName, now, plan.SessionID,
	); err != nil {
		return nil, fmt.Errorf("store: updating session on publish: %w", err)
	}

	if err := appendEventTx(tx, plan.SessionID, revNull(plan.Revision), ActorAgent, "plan.published",
		map[string]any{"revision": plan.Revision, "actions": len(plan.Actions), "from": string(sess.State), "transition": string(transition)},
		sql.NullInt64{}, now); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit PublishPlan: %w", err)
	}
	sess.State = nextState
	sess.CurrentRevision = plan.Revision
	sess.WorkspaceName = plan.Workspace.DisplayName
	return sess, nil
}

// LoadPlan returns the validated plan for a specific revision.
func (s *Store) LoadPlan(sessionID string, revision int) (*protocol.Plan, error) {
	var payload string
	err := s.db.QueryRow(
		`SELECT payload FROM plan_revisions WHERE session_id = ? AND revision = ?`,
		sessionID, revision,
	).Scan(&payload)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: loading plan: %w", err)
	}
	return protocol.ParsePlan([]byte(payload))
}

// LoadCurrentPlan returns the plan for the session's current revision.
func (s *Store) LoadCurrentPlan(sessionID string) (*protocol.Plan, error) {
	sess, err := s.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	if sess.CurrentRevision < 1 {
		return nil, ErrNotFound
	}
	return s.LoadPlan(sessionID, sess.CurrentRevision)
}

// --- helpers ---

// lockSession loads a session row inside a transaction. modernc.org/sqlite
// serializes writers via the busy timeout and WAL; the transaction itself is
// the atomicity boundary. SQLite has no SELECT ... FOR UPDATE, but a write
// transaction promotes to an exclusive lock on first write, so reading the row
// and then writing within the same tx is safe against concurrent writers.
func lockSession(tx *sql.Tx, id string) (*Session, error) {
	var (
		sess               Session
		state              string
		createdAt, updated string
	)
	err := tx.QueryRow(
		`SELECT id, state, workspace_id, workspace_name, current_revision, created_at, updated_at
		 FROM sessions WHERE id = ?`, id,
	).Scan(&sess.ID, &state, &sess.WorkspaceID, &sess.WorkspaceName, &sess.CurrentRevision, &createdAt, &updated)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: locking session: %w", err)
	}
	sess.State = policy.State(state)
	sess.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	sess.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &sess, nil
}

// appendEventTx inserts one append-only audit event within a transaction.
func appendEventTx(tx *sql.Tx, sessionID string, revision sql.NullInt64, actor Actor, typ string, payload map[string]any, parent sql.NullInt64, now string) error {
	raw := []byte("{}")
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("store: marshalling event payload: %w", err)
		}
		raw = b
	}
	_, err := tx.Exec(
		`INSERT INTO events(session_id, revision, actor, type, payload, parent_event_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sessionID, revision, string(actor), typ, string(raw), parent, now,
	)
	if err != nil {
		return fmt.Errorf("store: appending event %q: %w", typ, err)
	}
	return nil
}

// revNull wraps a revision int as a NullInt64 (revision 0 is stored as NULL to
// mean "no revision yet").
func revNull(rev int) sql.NullInt64 {
	if rev < 1 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(rev), Valid: true}
}

// validateOpaqueID reuses the protocol's opaque-id shape rules by parsing a
// throwaway workspace. It keeps id discipline consistent with the wire types
// without exporting protocol internals.
func validateOpaqueID(field, id string) error {
	w := protocol.Workspace{ID: id, DisplayName: ""}
	if err := w.Validate(); err != nil {
		// Re-map the field name for a clearer diagnostic.
		return fmt.Errorf("store: invalid %s: %w", field, err)
	}
	return nil
}

// isUniqueViolation reports whether err is a SQLite UNIQUE/PK constraint
// violation. modernc.org/sqlite surfaces these as errors whose message contains
// "UNIQUE constraint failed" / "PRIMARY KEY"; we match on that rather than a
// driver-specific code to avoid importing the driver's error type.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "PRIMARY KEY")
}
