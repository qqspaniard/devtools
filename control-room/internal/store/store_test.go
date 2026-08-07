package store

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/interactionlabs/devtools/control-room/internal/policy"
	"github.com/interactionlabs/devtools/control-room/internal/protocol"
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

func (c *fixedClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func openTestStore(t *testing.T, clock *fixedClock) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	var nowFn func() time.Time
	if clock != nil {
		nowFn = clock.now
	}
	s, err := Open(Options{Path: path, Now: nowFn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func samplePlan(session string, revision int) *protocol.Plan {
	return &protocol.Plan{
		ProtocolVersion: protocol.ProtocolVersion,
		SessionID:       session,
		Revision:        revision,
		Goal:            "do the thing",
		Summary:         "a thin slice",
		Workspace:       protocol.Workspace{ID: "ws-1", DisplayName: "devtools"},
		Blocks: []protocol.Block{
			{ID: "b1", Kind: protocol.BlockMarkdown, Content: "hello"},
		},
		Actions: []protocol.Action{
			{ID: "a1", Kind: protocol.ActionWritePatch, Title: "patch", Targets: []string{"workspace://x"}, Risk: protocol.RiskWorkspaceWrite},
			{ID: "a2", Kind: protocol.ActionRunCommand, Title: "test", Program: "go", Args: []string{"test"}, Cwd: "workspace://root", Risk: protocol.RiskLocalProcess},
		},
	}
}

func buildApproval(t *testing.T, p *protocol.Plan, selected []string, expires time.Time) *policy.Approval {
	t.Helper()
	a, err := policy.BuildApproval(policy.ApprovalRequest{
		Plan: p, SelectedActions: selected, ExpiresAt: expires, MaxClaims: 1,
	})
	if err != nil {
		t.Fatalf("BuildApproval: %v", err)
	}
	return a
}

func TestOpenAppliesMigrationsAndIsReopenable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")
	s, err := Open(Options{Path: path})
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := s.CreateSession("sess-1", "ws-1", "devtools"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	_ = s.Close()

	// Reopen: migrations already applied, integrity intact, data durable.
	s2, err := Open(Options{Path: path})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	got, err := s2.GetSession("sess-1")
	if err != nil {
		t.Fatalf("GetSession after reopen: %v", err)
	}
	if got.State != policy.StateDraft {
		t.Fatalf("state = %q, want draft", got.State)
	}
}

func TestPublishRejectsWorkspaceMismatch(t *testing.T) {
	s := openTestStore(t, nil)
	if _, err := s.CreateSession("sess", "ws-1", "d"); err != nil {
		t.Fatal(err)
	}
	p := samplePlan("sess", 1)
	p.Workspace.ID = "ws-OTHER"
	_, err := s.PublishPlan(p)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on workspace mismatch, got %v", err)
	}
}

func TestPublishRejectsNonIncreasingRevision(t *testing.T) {
	s := openTestStore(t, nil)
	if _, err := s.CreateSession("sess", "ws-1", "d"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishPlan(samplePlan("sess", 2)); err != nil {
		t.Fatalf("publish rev 2: %v", err)
	}
	// Republishing the same or a lower revision fails closed.
	if _, err := s.PublishPlan(samplePlan("sess", 2)); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict republishing rev 2, got %v", err)
	}
	if _, err := s.PublishPlan(samplePlan("sess", 1)); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict publishing rev 1 after rev 2, got %v", err)
	}
}

func TestDecisionApproveThenPollThenClaim(t *testing.T) {
	clock := &fixedClock{t: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)}
	s := openTestStore(t, clock)
	if _, err := s.CreateSession("sess", "ws-1", "d"); err != nil {
		t.Fatal(err)
	}
	p := samplePlan("sess", 1)
	if _, err := s.PublishPlan(p); err != nil {
		t.Fatal(err)
	}
	approval := buildApproval(t, p, []string{"a1", "a2"}, clock.now().Add(time.Hour))
	if _, err := s.RecordDecision("sess", 1, DecisionApprove, "", approval); err != nil {
		t.Fatalf("RecordDecision approve: %v", err)
	}

	// Poll returns the durable approve decision.
	d, err := s.GetDecision("sess")
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if d.Kind != DecisionApprove || d.ApprovalID == "" {
		t.Fatalf("unexpected decision %+v", d)
	}

	// Claim succeeds exactly once.
	res, err := s.Claim("sess", approval.Digest)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if res.ClaimSeq != 1 {
		t.Fatalf("claim seq = %d, want 1", res.ClaimSeq)
	}
	// Replay fails closed as exhausted.
	if _, err := s.Claim("sess", approval.Digest); !errors.Is(err, policy.ErrClaimsExhausted) {
		t.Fatalf("expected exhausted on replay, got %v", err)
	}
}

func TestStaleRevisionDecisionFailsClosed(t *testing.T) {
	s := openTestStore(t, nil)
	if _, err := s.CreateSession("sess", "ws-1", "d"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishPlan(samplePlan("sess", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishPlan(samplePlan("sess", 2)); err != nil {
		t.Fatal(err)
	}
	// A browser holding revision 1 tries to decide it; current is 2.
	_, err := s.RecordDecision("sess", 1, DecisionReject, "no", nil)
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("expected ErrStaleRevision, got %v", err)
	}
}

func TestSupersededApprovalFailsClosedOnClaim(t *testing.T) {
	clock := &fixedClock{t: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)}
	s := openTestStore(t, clock)
	if _, err := s.CreateSession("sess", "ws-1", "d"); err != nil {
		t.Fatal(err)
	}
	p1 := samplePlan("sess", 1)
	if _, err := s.PublishPlan(p1); err != nil {
		t.Fatal(err)
	}
	approval := buildApproval(t, p1, []string{"a1"}, clock.now().Add(time.Hour))
	if _, err := s.RecordDecision("sess", 1, DecisionApprove, "", approval); err != nil {
		t.Fatal(err)
	}
	// Publishing revision 2 supersedes the rev-1 approval.
	if _, err := s.PublishPlan(samplePlan("sess", 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim("sess", approval.Digest); !errors.Is(err, policy.ErrSuperseded) {
		t.Fatalf("expected ErrSuperseded after newer publish, got %v", err)
	}
}

func TestExpiredApprovalFailsClosedOnClaim(t *testing.T) {
	clock := &fixedClock{t: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)}
	s := openTestStore(t, clock)
	if _, err := s.CreateSession("sess", "ws-1", "d"); err != nil {
		t.Fatal(err)
	}
	p := samplePlan("sess", 1)
	if _, err := s.PublishPlan(p); err != nil {
		t.Fatal(err)
	}
	approval := buildApproval(t, p, []string{"a1"}, clock.now().Add(time.Minute))
	if _, err := s.RecordDecision("sess", 1, DecisionApprove, "", approval); err != nil {
		t.Fatal(err)
	}
	clock.advance(2 * time.Minute)
	if _, err := s.Claim("sess", approval.Digest); !errors.Is(err, policy.ErrExpired) {
		t.Fatalf("expected ErrExpired, got %v", err)
	}
}

func TestClaimUnknownAndMismatch(t *testing.T) {
	clock := &fixedClock{t: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)}
	s := openTestStore(t, clock)
	if _, err := s.CreateSession("sess", "ws-1", "d"); err != nil {
		t.Fatal(err)
	}
	p := samplePlan("sess", 1)
	if _, err := s.PublishPlan(p); err != nil {
		t.Fatal(err)
	}
	approval := buildApproval(t, p, []string{"a1"}, clock.now().Add(time.Hour))
	if _, err := s.RecordDecision("sess", 1, DecisionApprove, "", approval); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim("sess", "sha256:"+"0000000000000000000000000000000000000000000000000000000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown digest, got %v", err)
	}
	// Right digest, wrong session.
	if _, err := s.CreateSession("other", "ws-1", "d"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim("other", approval.Digest); !errors.Is(err, policy.ErrDigestMismatch) {
		t.Fatalf("expected ErrDigestMismatch, got %v", err)
	}
}

func TestConcurrentClaimsExactlyOneWinner(t *testing.T) {
	clock := &fixedClock{t: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)}
	s := openTestStore(t, clock)
	if _, err := s.CreateSession("sess", "ws-1", "d"); err != nil {
		t.Fatal(err)
	}
	p := samplePlan("sess", 1)
	if _, err := s.PublishPlan(p); err != nil {
		t.Fatal(err)
	}
	approval := buildApproval(t, p, []string{"a1"}, clock.now().Add(time.Hour))
	if _, err := s.RecordDecision("sess", 1, DecisionApprove, "", approval); err != nil {
		t.Fatal(err)
	}

	const n = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	var wins int
	var exhausted int
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, err := s.Claim("sess", approval.Digest)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, policy.ErrClaimsExhausted):
				exhausted++
			default:
				t.Errorf("unexpected claim error: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("expected exactly 1 winner, got %d (exhausted=%d)", wins, exhausted)
	}
}

func TestRestartDoesNotMakeClaimedApprovalReusable(t *testing.T) {
	clock := &fixedClock{t: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "restart.db")
	s, err := Open(Options{Path: path, Now: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession("sess", "ws-1", "d"); err != nil {
		t.Fatal(err)
	}
	p := samplePlan("sess", 1)
	if _, err := s.PublishPlan(p); err != nil {
		t.Fatal(err)
	}
	approval := buildApproval(t, p, []string{"a1"}, clock.now().Add(time.Hour))
	if _, err := s.RecordDecision("sess", 1, DecisionApprove, "", approval); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim("sess", approval.Digest); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	_ = s.Close()

	// Restart the store against the same file.
	s2, err := Open(Options{Path: path, Now: clock.now})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	// The decision survives.
	if d, err := s2.GetDecision("sess"); err != nil || d.Kind != DecisionApprove {
		t.Fatalf("decision not durable after restart: %+v err=%v", d, err)
	}
	// The claimed approval is NOT reusable.
	if _, err := s2.Claim("sess", approval.Digest); !errors.Is(err, policy.ErrClaimsExhausted) {
		t.Fatalf("expected claim exhausted after restart, got %v", err)
	}
}

func TestOpenFailsClosedOnNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	s, err := Open(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a future migration having been applied.
	if _, err := s.db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (9999, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	if _, err := Open(Options{Path: path}); err == nil {
		t.Fatal("expected Open to fail closed on newer schema version")
	}
}

// countEvents returns how many events of a given type exist for a session.
func countEvents(t *testing.T, s *Store, sessionID, typ string) int {
	t.Helper()
	evs, err := s.Events(sessionID, MaxEventsQuery)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	n := 0
	for _, e := range evs {
		if e.Type == typ {
			n++
		}
	}
	return n
}

// TestClaimLoserPathReturnsPromptlyAndAudits is the I2 regression: when many
// claims race a single-use approval, the losers must fail closed PROMPTLY (well
// below the 5s SQLite busy timeout) rather than blocking on the claim
// transaction's write lock, and — since the design promises a rejection audit —
// each loser's rejection must be recorded in the append-only history.
func TestClaimLoserPathReturnsPromptlyAndAudits(t *testing.T) {
	clock := &fixedClock{t: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)}
	s := openTestStore(t, clock)
	if _, err := s.CreateSession("sess", "ws-1", "d"); err != nil {
		t.Fatal(err)
	}
	p := samplePlan("sess", 1)
	if _, err := s.PublishPlan(p); err != nil {
		t.Fatal(err)
	}
	approval := buildApproval(t, p, []string{"a1"}, clock.now().Add(time.Hour))
	if _, err := s.RecordDecision("sess", 1, DecisionApprove, "", approval); err != nil {
		t.Fatal(err)
	}

	const n = 24
	var wg sync.WaitGroup
	var mu sync.Mutex
	var wins, losers int
	var maxDur time.Duration
	wg.Add(n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			t0 := time.Now()
			_, err := s.Claim("sess", approval.Digest)
			d := time.Since(t0)
			mu.Lock()
			defer mu.Unlock()
			if d > maxDur {
				maxDur = d
			}
			switch {
			case err == nil:
				wins++
			case errors.Is(err, policy.ErrClaimsExhausted):
				losers++
			default:
				t.Errorf("unexpected claim error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if wins != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", wins)
	}
	if losers != n-1 {
		t.Fatalf("expected %d losers, got %d", n-1, losers)
	}
	// The busy timeout is 5s; a self-contending audit would push a loser toward
	// that. Assert every claim completed far below it.
	if maxDur >= 2*time.Second {
		t.Fatalf("a claim took %v, dangerously close to the 5s busy timeout (self-contention regression)", maxDur)
	}
	// Every loser recorded a rejection audit (winner records execution.claimed).
	rejects := countEvents(t, s, "sess", "execution.claim_rejected")
	if rejects != n-1 {
		t.Fatalf("expected %d claim_rejected audit events, got %d", n-1, rejects)
	}
	if claimed := countEvents(t, s, "sess", "execution.claimed"); claimed != 1 {
		t.Fatalf("expected 1 execution.claimed event, got %d", claimed)
	}
}

// TestClaimRejectionAuditsAreStored asserts each distinct fail-closed rejection
// reason is durably audited (I2: audit must survive now that it runs after the
// claim tx is finalized).
func TestClaimRejectionAuditsAreStored(t *testing.T) {
	clock := &fixedClock{t: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)}
	s := openTestStore(t, clock)
	if _, err := s.CreateSession("sess", "ws-1", "d"); err != nil {
		t.Fatal(err)
	}
	p := samplePlan("sess", 1)
	if _, err := s.PublishPlan(p); err != nil {
		t.Fatal(err)
	}
	approval := buildApproval(t, p, []string{"a1"}, clock.now().Add(time.Minute))
	if _, err := s.RecordDecision("sess", 1, DecisionApprove, "", approval); err != nil {
		t.Fatal(err)
	}
	// Unknown digest → audited under this session id.
	unknown := "sha256:" + "1111111111111111111111111111111111111111111111111111111111111111"
	if _, err := s.Claim("sess", unknown); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	// Expire, then claim → audited.
	clock.advance(2 * time.Minute)
	if _, err := s.Claim("sess", approval.Digest); !errors.Is(err, policy.ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
	if got := countEvents(t, s, "sess", "execution.claim_rejected"); got != 2 {
		t.Fatalf("expected 2 claim_rejected audits (unknown + expired), got %d", got)
	}
}

// TestPermissionEnvelopePersisted is the N5 regression: the approval's real
// permission envelope (sorted risk classes of the selected actions) is stored,
// not a placeholder, and is queryable.
func TestPermissionEnvelopePersisted(t *testing.T) {
	clock := &fixedClock{t: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)}
	s := openTestStore(t, clock)
	if _, err := s.CreateSession("sess", "ws-1", "d"); err != nil {
		t.Fatal(err)
	}
	p := samplePlan("sess", 1) // a1=workspace_write, a2=local_process
	if _, err := s.PublishPlan(p); err != nil {
		t.Fatal(err)
	}
	approval := buildApproval(t, p, []string{"a1", "a2"}, clock.now().Add(time.Hour))
	if _, err := s.RecordDecision("sess", 1, DecisionApprove, "", approval); err != nil {
		t.Fatal(err)
	}
	env, err := s.PermissionEnvelope("sess", approval.Digest)
	if err != nil {
		t.Fatalf("PermissionEnvelope: %v", err)
	}
	want := []string{"local_process", "workspace_write"} // sorted
	if len(env) != len(want) {
		t.Fatalf("envelope = %v, want %v", env, want)
	}
	for i := range want {
		if env[i] != want[i] {
			t.Fatalf("envelope[%d] = %q, want %q (full %v)", i, env[i], want[i], env)
		}
	}
	// It must NOT be the old empty placeholder.
	if len(env) == 0 {
		t.Fatal("permission envelope was persisted as empty placeholder (N5 regression)")
	}
}

// TestPublishRepublishTransitionsFromRejected is the N4 regression: after a
// reject (terminal in the core matrix), publishing a newer revision is a legal
// broker-level republish that returns the session to awaiting_approval.
func TestPublishRepublishTransitionsFromRejected(t *testing.T) {
	s := openTestStore(t, nil)
	if _, err := s.CreateSession("sess", "ws-1", "d"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishPlan(samplePlan("sess", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordDecision("sess", 1, DecisionReject, "no", nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSession("sess")
	if err != nil || got.State != policy.StateRejected {
		t.Fatalf("expected rejected, got %+v err=%v", got, err)
	}
	// Republish a newer revision from the rejected state.
	sess, err := s.PublishPlan(samplePlan("sess", 2))
	if err != nil {
		t.Fatalf("republish after reject: %v", err)
	}
	if sess.State != policy.StateAwaitingApproval || sess.CurrentRevision != 2 {
		t.Fatalf("after republish: %+v", sess)
	}
}

// TestEndSessionAdministrativeFromAnyNonTerminal is the N4 regression for the
// administrative end operation: a session in a non-terminal state (e.g.
// awaiting_approval) can be ended, and ending a terminal session is idempotent.
func TestEndSessionAdministrativeFromAnyNonTerminal(t *testing.T) {
	s := openTestStore(t, nil)
	if _, err := s.CreateSession("sess", "ws-1", "d"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishPlan(samplePlan("sess", 1)); err != nil {
		t.Fatal(err)
	}
	// awaiting_approval has no "end" edge in the core matrix; the administrative
	// operation must still close it.
	ended, err := s.EndSession("sess")
	if err != nil || ended.State != policy.StateCompleted {
		t.Fatalf("EndSession: %+v err=%v", ended, err)
	}
	// Idempotent: ending again returns completed without error.
	again, err := s.EndSession("sess")
	if err != nil || again.State != policy.StateCompleted {
		t.Fatalf("EndSession(again): %+v err=%v", again, err)
	}
}

// TestNoIdempotencyTable is the I1 regression: the unused idempotency table was
// removed from the initial migration along with its false retry promise. The
// schema must not contain it.
func TestNoIdempotencyTable(t *testing.T) {
	s := openTestStore(t, nil)
	var name string
	err := s.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='idempotency'`,
	).Scan(&name)
	if err == nil {
		t.Fatal("idempotency table still exists; it must be removed (I1)")
	}
}
