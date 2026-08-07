package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/interactionlabs/devtools/control-room/internal/policy"
)

// RecordDecision durably records a user's review outcome for a specific
// revision and applies the corresponding state transition, all in one
// transaction.
//
// It fails closed if:
//   - the session does not exist;
//   - the target revision is not the session's CURRENT revision (a stale
//     browser decision on a superseded revision is rejected — ErrStaleRevision);
//   - the session is not in awaiting_approval (only a pending review can be
//     decided);
//   - a decision for that revision already exists (ErrConflict — one terminal
//     decision per revision).
//
// For an approve decision the caller supplies an already-built *policy.Approval
// (constructed from the loaded plan and the user's selection). Its digest binds
// the exact revision/selection/workspace/envelope/expiration/max-claims; the
// store persists it atomically with the decision and records approval.granted.
func (s *Store) RecordDecision(sessionID string, revision int, kind DecisionKind, reason string, approval *policy.Approval) (*Decision, error) {
	if len(reason) > MaxReasonBytes {
		return nil, fmt.Errorf("%w: reason exceeds %d bytes", ErrConflict, MaxReasonBytes)
	}
	switch kind {
	case DecisionApprove:
		if approval == nil {
			return nil, fmt.Errorf("store: approve decision requires an approval")
		}
		reason = "" // approve carries no reason
	case DecisionReject, DecisionRequestChanges:
		if approval != nil {
			return nil, fmt.Errorf("store: %s decision must not carry an approval", kind)
		}
	default:
		return nil, fmt.Errorf("store: unknown decision kind %q", kind)
	}

	now := s.nowStr()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: begin RecordDecision: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	sess, err := lockSession(tx, sessionID)
	if err != nil {
		return nil, err
	}
	// Fail closed on a stale-revision decision: the browser may hold a page for
	// an older revision that has since been superseded by a newer publish.
	if revision != sess.CurrentRevision {
		return nil, fmt.Errorf("%w: decision targets revision %d but current is %d",
			ErrStaleRevision, revision, sess.CurrentRevision)
	}
	if sess.State != policy.StateAwaitingApproval {
		return nil, fmt.Errorf("%w: session is %q, not awaiting_approval", ErrConflict, sess.State)
	}

	// Compute the transition first so an illegal move fails before any write.
	var transition policy.Transition
	switch kind {
	case DecisionApprove:
		transition = policy.TransitionApprove
	case DecisionReject:
		transition = policy.TransitionReject
	case DecisionRequestChanges:
		transition = policy.TransitionRequestEdit
	}
	nextState, err := policy.Next(sess.State, transition)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	var approvalID sql.NullString
	if kind == DecisionApprove {
		if approval.SessionID != sessionID || approval.PlanRevision != revision {
			return nil, fmt.Errorf("%w: approval does not bind this session/revision", ErrConflict)
		}
		id, err := insertApprovalTx(tx, approval, now)
		if err != nil {
			return nil, err
		}
		approvalID = sql.NullString{String: id, Valid: true}
	}

	_, err = tx.Exec(
		`INSERT INTO decisions(session_id, revision, kind, reason, approval_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, revision, string(kind), reason, approvalID, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: revision %d already decided", ErrConflict, revision)
		}
		return nil, fmt.Errorf("store: inserting decision: %w", err)
	}

	if _, err := tx.Exec(
		`UPDATE sessions SET state = ?, updated_at = ? WHERE id = ?`,
		string(nextState), now, sessionID,
	); err != nil {
		return nil, fmt.Errorf("store: updating session on decision: %w", err)
	}

	eventType := map[DecisionKind]string{
		DecisionApprove:        "approval.granted",
		DecisionReject:         "approval.denied",
		DecisionRequestChanges: "plan.revise_requested",
	}[kind]
	payload := map[string]any{"revision": revision}
	if approval != nil {
		payload["digest"] = approval.Digest
	}
	if reason != "" {
		payload["reason_len"] = len(reason)
	}
	if err := appendEventTx(tx, sessionID, revNull(revision), ActorUser, eventType, payload, sql.NullInt64{}, now); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit RecordDecision: %w", err)
	}
	d := &Decision{SessionID: sessionID, Revision: revision, Kind: kind, Reason: reason}
	if approvalID.Valid {
		d.ApprovalID = approvalID.String
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, now)
	return d, nil
}

// insertApprovalTx persists an approval row. The digest column is UNIQUE, so a
// duplicate digest cannot create two approvals. The permission envelope is
// persisted as canonical JSON for audit; the digest remains the binding
// authority, but recording the envelope makes the granted risk classes queryable
// without recomputing them from the plan.
func insertApprovalTx(tx *sql.Tx, a *policy.Approval, now string) (string, error) {
	id, err := randomApprovalID()
	if err != nil {
		return "", err
	}
	allowed, err := json.Marshal(a.AllowedActions)
	if err != nil {
		return "", fmt.Errorf("store: marshalling allowed actions: %w", err)
	}
	envelope := a.PermissionEnvelope
	if envelope == nil {
		envelope = []string{}
	}
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("store: marshalling permission envelope: %w", err)
	}
	_, err = tx.Exec(
		`INSERT INTO approvals(id, session_id, plan_revision, digest, allowed_action_ids, permission_envelope, expires_at, max_claims, claims_consumed, superseded, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?)`,
		id, a.SessionID, a.PlanRevision, a.Digest, string(allowed), string(envelopeJSON),
		a.ExpiresAt.UTC().Format(time.RFC3339Nano), a.MaxClaims, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return "", fmt.Errorf("%w: approval digest already recorded", ErrConflict)
		}
		return "", fmt.Errorf("store: inserting approval: %w", err)
	}
	return id, nil
}

// GetDecision returns the durable decision for the session's CURRENT revision,
// or ErrNotFound if none has been made yet. This is what `decision poll`
// surfaces: the latest, authoritative outcome for the revision under review.
func (s *Store) GetDecision(sessionID string) (*Decision, error) {
	sess, err := s.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	if sess.CurrentRevision < 1 {
		return nil, ErrNotFound
	}
	return s.getDecisionForRevision(sessionID, sess.CurrentRevision)
}

func (s *Store) getDecisionForRevision(sessionID string, revision int) (*Decision, error) {
	var (
		d          Decision
		kind       string
		approvalID sql.NullString
		created    string
	)
	err := s.db.QueryRow(
		`SELECT session_id, revision, kind, reason, approval_id, created_at
		 FROM decisions WHERE session_id = ? AND revision = ?`,
		sessionID, revision,
	).Scan(&d.SessionID, &d.Revision, &kind, &d.Reason, &approvalID, &created)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: loading decision: %w", err)
	}
	d.Kind = DecisionKind(kind)
	if approvalID.Valid {
		d.ApprovalID = approvalID.String
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &d, nil
}

// ApprovalByDigest loads the durable approval for a session+digest pair, or
// ErrNotFound. Used by claim to resolve the capability.
func (s *Store) ApprovalByDigest(sessionID, digest string) (*policy.Approval, error) {
	a, _, _, err := s.loadApproval(s.db, sessionID, digest)
	return a, err
}

// PermissionEnvelope returns the persisted permission-envelope risk classes for
// an approval identified by (session, digest), or ErrNotFound. It reads the
// canonical JSON column recorded at approval time, so callers can audit the
// granted risk classes without recomputing them from the plan.
func (s *Store) PermissionEnvelope(sessionID, digest string) ([]string, error) {
	var raw string
	err := s.db.QueryRow(
		`SELECT permission_envelope FROM approvals WHERE session_id = ? AND digest = ?`,
		sessionID, digest,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: loading permission envelope: %w", err)
	}
	var envelope []string
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return nil, fmt.Errorf("store: decoding permission envelope: %w", err)
	}
	return envelope, nil
}
