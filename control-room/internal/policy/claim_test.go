package policy

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fixedClock returns a controllable clock for deterministic expiration tests.
type fixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fixedClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fixedClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func newApprovalFor(t *testing.T, maxClaims int) *Approval {
	t.Helper()
	return buildOK(t, ApprovalRequest{
		Plan:            testPlan(),
		SelectedActions: []string{"action-1", "action-2"},
		ExpiresAt:       testExpiry(),
		MaxClaims:       maxClaims,
	})
}

func TestClaimSingleUse(t *testing.T) {
	clock := &fixedClock{t: testExpiry().Add(-time.Hour)}
	auth := NewClaimAuthority(clock.now)
	a := newApprovalFor(t, 1)
	if err := auth.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := auth.Claim(a.SessionID, a.Digest); err != nil {
		t.Fatalf("first claim should succeed: %v", err)
	}
	// Replay: the same digest again must fail closed.
	if _, err := auth.Claim(a.SessionID, a.Digest); !errors.Is(err, ErrClaimsExhausted) {
		t.Fatalf("replay should be ErrClaimsExhausted, got %v", err)
	}
}

func TestClaimHonorsMaxClaims(t *testing.T) {
	clock := &fixedClock{t: testExpiry().Add(-time.Hour)}
	auth := NewClaimAuthority(clock.now)
	a := newApprovalFor(t, 3)
	if err := auth.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := auth.Claim(a.SessionID, a.Digest); err != nil {
			t.Fatalf("claim %d should succeed: %v", i, err)
		}
	}
	if _, err := auth.Claim(a.SessionID, a.Digest); !errors.Is(err, ErrClaimsExhausted) {
		t.Fatalf("4th claim should be exhausted, got %v", err)
	}
	if n, _ := auth.ClaimsConsumed(a.Digest); n != 3 {
		t.Fatalf("expected 3 consumed, got %d", n)
	}
}

func TestClaimRejectsUnknownDigest(t *testing.T) {
	auth := NewClaimAuthority(func() time.Time { return testExpiry().Add(-time.Hour) })
	if _, err := auth.Claim("session-abc", "sha256:deadbeef"); !errors.Is(err, ErrUnknownApproval) {
		t.Fatalf("expected ErrUnknownApproval, got %v", err)
	}
}

func TestClaimRejectsDigestSessionMismatch(t *testing.T) {
	auth := NewClaimAuthority(func() time.Time { return testExpiry().Add(-time.Hour) })
	a := newApprovalFor(t, 1)
	if err := auth.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Correct digest, wrong session id.
	if _, err := auth.Claim("some-other-session", a.Digest); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("expected ErrDigestMismatch, got %v", err)
	}
}

func TestClaimRejectsExpired(t *testing.T) {
	clock := &fixedClock{t: testExpiry().Add(-time.Hour)}
	auth := NewClaimAuthority(clock.now)
	a := newApprovalFor(t, 1)
	if err := auth.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Advance the clock to exactly expiry; expiration is exclusive so this is
	// already expired.
	clock.set(testExpiry())
	if _, err := auth.Claim(a.SessionID, a.Digest); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected ErrExpired at expiry boundary, got %v", err)
	}
}

func TestClaimSupersededByNewerApproval(t *testing.T) {
	clock := &fixedClock{t: testExpiry().Add(-time.Hour)}
	auth := NewClaimAuthority(clock.now)

	// Approve revision 4.
	oldA := newApprovalFor(t, 1)
	if err := auth.Register(oldA); err != nil {
		t.Fatalf("Register old: %v", err)
	}

	// A newer revision (5) is approved for the same session.
	p := testPlan()
	p.Revision = 5
	newA := buildOK(t, ApprovalRequest{Plan: p, SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1})
	if err := auth.Register(newA); err != nil {
		t.Fatalf("Register new: %v", err)
	}

	// The stale (revision 4) digest can no longer be claimed.
	if _, err := auth.Claim(oldA.SessionID, oldA.Digest); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("stale digest should be ErrSuperseded, got %v", err)
	}
	// The new digest still claims fine.
	if _, err := auth.Claim(newA.SessionID, newA.Digest); err != nil {
		t.Fatalf("new digest claim should succeed: %v", err)
	}
}

func TestReRegisterSameApprovalIsIdempotent(t *testing.T) {
	clock := &fixedClock{t: testExpiry().Add(-time.Hour)}
	auth := NewClaimAuthority(clock.now)
	a := newApprovalFor(t, 1)
	if err := auth.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := auth.Claim(a.SessionID, a.Digest); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Re-registering the identical approval must not reset the consumed count
	// (otherwise single-use protection could be defeated).
	if err := auth.Register(a); err != nil {
		t.Fatalf("re-Register: %v", err)
	}
	if _, err := auth.Claim(a.SessionID, a.Digest); !errors.Is(err, ErrClaimsExhausted) {
		t.Fatalf("re-registration reset claim count; got %v", err)
	}
}

// TestConcurrentClaimsSingleUse launches many goroutines racing to claim a
// single-use approval; exactly one must win. Run under -race.
func TestConcurrentClaimsSingleUse(t *testing.T) {
	clock := &fixedClock{t: testExpiry().Add(-time.Hour)}
	auth := NewClaimAuthority(clock.now)
	a := newApprovalFor(t, 1)
	if err := auth.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}

	const goroutines = 64
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	exhausted := 0
	other := 0

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := auth.Claim(a.SessionID, a.Digest)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrClaimsExhausted):
				exhausted++
			default:
				other++
			}
		}()
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("expected exactly 1 successful claim, got %d", successes)
	}
	if other != 0 {
		t.Fatalf("expected no unexpected errors, got %d", other)
	}
	if exhausted != goroutines-1 {
		t.Fatalf("expected %d exhausted, got %d", goroutines-1, exhausted)
	}
}

// TestConcurrentClaimsBoundedByMaxClaims races many goroutines against an
// approval with a finite max_claims; exactly max_claims must win.
func TestConcurrentClaimsBoundedByMaxClaims(t *testing.T) {
	clock := &fixedClock{t: testExpiry().Add(-time.Hour)}
	auth := NewClaimAuthority(clock.now)
	const maxClaims = 5
	a := newApprovalFor(t, maxClaims)
	if err := auth.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}

	const goroutines = 100
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if _, err := auth.Claim(a.SessionID, a.Digest); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if successes != maxClaims {
		t.Fatalf("expected exactly %d successful claims, got %d", maxClaims, successes)
	}
}

func TestRegisterRejectsNilAndEmptyDigest(t *testing.T) {
	auth := NewClaimAuthority(func() time.Time { return time.Now() })
	if err := auth.Register(nil); err == nil {
		t.Fatal("expected rejection of nil approval")
	}
	if err := auth.Register(&Approval{SessionID: "s", Digest: ""}); err == nil {
		t.Fatal("expected rejection of empty digest")
	}
}
