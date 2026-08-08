package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/interactionlabs/devtools/control-room/internal/policy"
)

// ClaimResult is returned by a successful claim.
type ClaimResult struct {
	Approval *policy.Approval `json:"approval"`
	// ClaimSeq is the 1-based sequence of this claim within the approval's
	// max_claims budget. For single-use approvals it is always 1.
	ClaimSeq int `json:"claim_seq"`
}

// lockedApproval is the mutable approval row read inside a claim transaction.
type lockedApproval struct {
	rowID      string
	approval   *policy.Approval
	maxClaims  int
	consumed   int
	superseded bool
	expiresAt  time.Time
}

// Claim atomically consumes one claim against the approval identified by
// (sessionID, digest), enforcing the RFC's fail-closed order durably:
//
//	unknown digest      → ErrNotFound
//	session/digest cross → policy.ErrDigestMismatch
//	superseded           → policy.ErrSuperseded
//	expired              → policy.ErrExpired
//	claims exhausted     → policy.ErrClaimsExhausted
//
// The check-and-increment happens inside a single write transaction, so
// concurrent claims are serialized by SQLite and at most max_claims of them can
// succeed. Because both the approvals.claims_consumed counter AND a claims row
// are persisted, a broker restart cannot make a consumed single-use approval
// claimable again: the durable counter already reflects the consumption.
//
// Transaction lifecycle (fixes a self-contention bug): the claim transaction is
// opened, the decision is made, and the transaction is fully committed OR rolled
// back BEFORE any audit write. A rejection audit is a separate short transaction
// written only after the claim transaction has released SQLite's write lock, so
// it never waits on the very lock the claim path holds. This preserves exact
// atomic single-use / restart semantics and the fail-closed error ordering.
func (s *Store) Claim(sessionID, digest string) (*ClaimResult, error) {
	result, rejectReason, err := s.claimTx(sessionID, digest)
	// The claim transaction has been committed or rolled back by claimTx before
	// it returns, so the write lock is released here. Any audit write below runs
	// on a fresh transaction with no contention against the claim path.
	if rejectReason != "" {
		s.auditRejectedClaim(sessionID, digest, rejectReason)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// claimTx performs the entire claim decision inside one transaction and returns
// before doing any audit write. It returns (result, "", nil) on success, or
// (nil, reason, err) on a fail-closed rejection where reason is the audit
// reason string to record and err is the typed sentinel to return. The
// transaction is always finalized (commit or rollback) before returning, so the
// caller can safely audit without contending for the write lock.
func (s *Store) claimTx(sessionID, digest string) (*ClaimResult, string, error) {
	now := s.nowStr()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, "", fmt.Errorf("store: begin Claim: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	la, found, err := lockApprovalTx(tx, digest)
	if err != nil {
		return nil, "", err
	}
	if !found {
		return nil, "unknown_approval", ErrNotFound
	}
	if la.approval.SessionID != sessionID {
		return nil, "digest_mismatch", policy.ErrDigestMismatch
	}
	if la.superseded {
		return nil, "superseded", policy.ErrSuperseded
	}
	if !s.now().Before(la.expiresAt) {
		return nil, "expired", policy.ErrExpired
	}
	if la.consumed >= la.maxClaims {
		return nil, "claims_exhausted", policy.ErrClaimsExhausted
	}

	seq := la.consumed + 1
	// Compare-and-set on the counter: the WHERE claims_consumed = <observed>
	// clause means only one transaction can move consumed from N to N+1. Within
	// a single SQLite write tx this is belt-and-braces; the durable counter is
	// what survives restart.
	res, err := tx.Exec(
		`UPDATE approvals SET claims_consumed = ? WHERE id = ? AND claims_consumed = ?`,
		seq, la.rowID, la.consumed,
	)
	if err != nil {
		return nil, "", fmt.Errorf("store: incrementing claims_consumed: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, "claims_exhausted", policy.ErrClaimsExhausted
	}

	// The (approval_id, claim_seq) primary key is the durable single-use proof.
	if _, err := tx.Exec(
		`INSERT INTO claims(approval_id, claim_seq, claimed_at) VALUES (?, ?, ?)`,
		la.rowID, seq, now,
	); err != nil {
		if isUniqueViolation(err) {
			return nil, "claims_exhausted", policy.ErrClaimsExhausted
		}
		return nil, "", fmt.Errorf("store: inserting claim row: %w", err)
	}

	if err := appendEventTx(tx, sessionID, revNull(la.approval.PlanRevision), ActorAgent,
		"execution.claimed", map[string]any{"digest": digest, "claim_seq": seq}, sql.NullInt64{}, now); err != nil {
		return nil, "", err
	}

	// Move the session to running on the first claim from the approved state.
	sess, err := lockSession(tx, sessionID)
	if err != nil {
		return nil, "", err
	}
	if sess.State == policy.StateApproved {
		next, err := policy.Next(sess.State, policy.TransitionClaim)
		if err != nil {
			return nil, "", fmt.Errorf("store: %w", err)
		}
		if _, err := tx.Exec(
			`UPDATE sessions SET state = ?, updated_at = ? WHERE id = ?`,
			string(next), now, sessionID,
		); err != nil {
			return nil, "", fmt.Errorf("store: updating session on claim: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, "", fmt.Errorf("store: commit Claim: %w", err)
	}
	committed = true
	return &ClaimResult{Approval: la.approval, ClaimSeq: seq}, "", nil
}

// lockApprovalTx loads the mutable approval row inside a transaction. found is
// false for an unknown digest.
func lockApprovalTx(tx *sql.Tx, digest string) (*lockedApproval, bool, error) {
	var (
		id, sessionID, allowedJSON, expiresStr string
		revision, mc, cc, sup                  int
	)
	err := tx.QueryRow(
		`SELECT id, session_id, plan_revision, allowed_action_ids, expires_at, max_claims, claims_consumed, superseded
		 FROM approvals WHERE digest = ?`, digest,
	).Scan(&id, &sessionID, &revision, &allowedJSON, &expiresStr, &mc, &cc, &sup)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: locking approval: %w", err)
	}
	var allowed []string
	if err := json.Unmarshal([]byte(allowedJSON), &allowed); err != nil {
		return nil, false, fmt.Errorf("store: decoding allowed actions: %w", err)
	}
	exp, err := time.Parse(time.RFC3339Nano, expiresStr)
	if err != nil {
		return nil, false, fmt.Errorf("store: parsing approval expiry: %w", err)
	}
	return &lockedApproval{
		rowID: id,
		approval: &policy.Approval{
			SessionID:      sessionID,
			PlanRevision:   revision,
			Digest:         digest,
			AllowedActions: allowed,
			ExpiresAt:      exp,
			MaxClaims:      mc,
		},
		maxClaims:  mc,
		consumed:   cc,
		superseded: sup == 1,
		expiresAt:  exp,
	}, true, nil
}

// loadApproval reads an approval (read-only) for a session+digest. Used by
// ApprovalByDigest for diagnostics/tests.
func (s *Store) loadApproval(q queryer, sessionID, digest string) (*policy.Approval, int, int, error) {
	var (
		id, sess, allowedJSON, envelopeJSON, expiresStr string
		revision, mc, cc                                int
	)
	err := q.QueryRow(
		`SELECT id, session_id, plan_revision, allowed_action_ids, permission_envelope, expires_at, max_claims, claims_consumed
		 FROM approvals WHERE session_id = ? AND digest = ?`, sessionID, digest,
	).Scan(&id, &sess, &revision, &allowedJSON, &envelopeJSON, &expiresStr, &mc, &cc)
	if err == sql.ErrNoRows {
		return nil, 0, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, 0, fmt.Errorf("store: loading approval: %w", err)
	}
	var allowed []string
	_ = json.Unmarshal([]byte(allowedJSON), &allowed)
	var envelope []string
	_ = json.Unmarshal([]byte(envelopeJSON), &envelope)
	exp, _ := time.Parse(time.RFC3339Nano, expiresStr)
	return &policy.Approval{
		SessionID:          sess,
		PlanRevision:       revision,
		Digest:             digest,
		AllowedActions:     allowed,
		PermissionEnvelope: envelope,
		ExpiresAt:          exp,
		MaxClaims:          mc,
	}, mc, cc, nil
}

// queryer is the read subset shared by *sql.DB and *sql.Tx.
type queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

// auditRejectedClaim appends a fail-closed claim rejection to the event history
// in its own short transaction. It is invoked by Claim ONLY after the claim
// transaction has been finalized (committed or rolled back), so it never
// contends for the write lock the claim path held. It is best-effort: an audit
// failure must not mask the security decision, so its error is swallowed after
// the primary rejection has been determined.
func (s *Store) auditRejectedClaim(sessionID, digest, reason string) {
	now := s.nowStr()
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	_ = appendEventTx(tx, sessionID, sql.NullInt64{}, ActorAgent, "execution.claim_rejected",
		map[string]any{"digest": digest, "reason": reason}, sql.NullInt64{}, now)
	_ = tx.Commit()
}

// randomApprovalID returns a 128-bit random hex id for an approval row.
func randomApprovalID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("store: generating approval id: %w", err)
	}
	return "apr-" + hex.EncodeToString(b[:]), nil
}
