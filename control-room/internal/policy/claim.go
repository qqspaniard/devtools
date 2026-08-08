package policy

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Claim outcomes.
var (
	// ErrUnknownApproval is returned when a claim references a digest the
	// authority has never registered.
	ErrUnknownApproval = errors.New("policy: unknown approval digest")

	// ErrDigestMismatch is returned when the digest a caller presents does not
	// match the registered approval for that session (e.g. a stale or
	// substituted digest).
	ErrDigestMismatch = errors.New("policy: approval digest mismatch")

	// ErrExpired is returned when a claim arrives after the approval's
	// expiration.
	ErrExpired = errors.New("policy: approval expired")

	// ErrClaimsExhausted is returned when an approval's max_claims has already
	// been consumed (single-use replay is the max_claims == 1 case).
	ErrClaimsExhausted = errors.New("policy: approval claims exhausted")

	// ErrSuperseded is returned when an approval has been invalidated because a
	// newer plan revision was approved for the same session.
	ErrSuperseded = errors.New("policy: approval superseded by newer revision")
)

// ClaimAuthority is a concurrency-safe registry of approvals that enforces
// single-use / max-claims, expiration, digest-match, and supersession
// semantics.
//
// IMPORTANT: This is domain-policy code, not persistence. It holds approvals in
// memory only. Phase 0 has no durable store; this type exists so the claim
// rules can be exercised and adversarially tested (replay, concurrent claims,
// stale/superseded digests) without pretending to be the durable authority the
// RFC's SQLite event store will later provide. A process restart discards all
// registered approvals. Do not treat it as recovery-safe.
type ClaimAuthority struct {
	// now returns the current time. It is injected so tests are deterministic
	// and policy does not reach for the wall clock internally.
	now func() time.Time

	mu       sync.Mutex
	byDigest map[string]*claimState
	// current maps a session ID to the digest of its currently-active approval,
	// so a newer approval supersedes an older one for the same session.
	current map[string]string
}

type claimState struct {
	approval Approval
	claims   int
	// superseded is set when a newer approval is registered for the same
	// session; a superseded approval fails closed on claim even if unexpired
	// and unexhausted.
	superseded bool
}

// NewClaimAuthority constructs a ClaimAuthority. If now is nil, time.Now is
// used. Callers that need determinism (tests) should pass an explicit clock.
func NewClaimAuthority(now func() time.Time) *ClaimAuthority {
	if now == nil {
		now = time.Now
	}
	return &ClaimAuthority{
		now:      now,
		byDigest: make(map[string]*claimState),
		current:  make(map[string]string),
	}
}

// Register records an approval as the current approval for its session.
//
// Registering an approval for a session that already has one supersedes the
// prior approval: subsequent claims against the old digest fail with
// ErrSuperseded. This models the RFC rule that a stale plan revision cannot
// receive a new claim once a newer revision is approved.
//
// Registering the exact same approval (same digest) again is idempotent and
// preserves the existing claim count, so a duplicate registration cannot reset
// single-use protection.
func (c *ClaimAuthority) Register(a *Approval) error {
	if a == nil {
		return fmt.Errorf("policy: nil approval")
	}
	if a.Digest == "" {
		return fmt.Errorf("policy: approval has empty digest")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.byDigest[a.Digest]; ok {
		// Idempotent re-registration of the same approval: keep claim state.
		c.current[a.SessionID] = a.Digest
		return nil
	}

	// Supersede any prior current approval for this session.
	if prevDigest, ok := c.current[a.SessionID]; ok {
		if prev, ok := c.byDigest[prevDigest]; ok {
			prev.superseded = true
		}
	}

	cp := *a
	c.byDigest[a.Digest] = &claimState{approval: cp}
	c.current[a.SessionID] = a.Digest
	return nil
}

// Claim atomically attempts to consume one claim against the approval
// identified by (sessionID, digest).
//
// It fails closed, in this order, on: unknown digest, session/digest mismatch,
// supersession, expiration, and exhausted claims. On success it returns the
// approval and increments the consumed-claim count. Concurrent Claim calls are
// serialized; at most max_claims of them can succeed.
func (c *ClaimAuthority) Claim(sessionID, digest string) (*Approval, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	st, ok := c.byDigest[digest]
	if !ok {
		return nil, ErrUnknownApproval
	}
	if st.approval.SessionID != sessionID {
		return nil, ErrDigestMismatch
	}
	// Ensure this digest is still the session's current approval AND not
	// explicitly superseded. Both checks matter: superseded guards the case
	// where the map still points elsewhere; the current-pointer check guards
	// stale references directly.
	if st.superseded {
		return nil, ErrSuperseded
	}
	if cur, ok := c.current[sessionID]; ok && cur != digest {
		return nil, ErrSuperseded
	}
	if !c.now().Before(st.approval.ExpiresAt) {
		return nil, ErrExpired
	}
	if st.claims >= st.approval.MaxClaims {
		return nil, ErrClaimsExhausted
	}
	st.claims++
	out := st.approval
	return &out, nil
}

// ClaimsConsumed returns how many claims have been consumed for a digest, or
// (0, false) if the digest is unknown. Intended for tests and diagnostics.
func (c *ClaimAuthority) ClaimsConsumed(digest string) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.byDigest[digest]
	if !ok {
		return 0, false
	}
	return st.claims, true
}
