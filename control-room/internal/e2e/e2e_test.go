// Package e2e exercises the full Control Room usable loop against a real broker,
// a real loopback web server, and a real SQLite store — the same wiring `serve`
// uses — driving publish → browser decision (via an HTTP test client) → poll →
// claim → restart → replay rejection.
//
// It lives in its own package so it composes the store, broker, web, and
// protocol packages exactly as the binary does, without importing test helpers
// from any single layer.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/interactionlabs/devtools/control-room/internal/broker"
	"github.com/interactionlabs/devtools/control-room/internal/protocol"
	"github.com/interactionlabs/devtools/control-room/internal/statedir"
	"github.com/interactionlabs/devtools/control-room/internal/store"
	"github.com/interactionlabs/devtools/control-room/internal/web"
)

// shortDir returns a short-path state dir so the Unix socket stays under the OS
// sockaddr_un limit (macOS t.TempDir() paths are too long for a socket).
func shortDir(t *testing.T) string {
	t.Helper()
	base := "/tmp/opencode/cr-e2e"
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	dir := filepath.Join(base, strconv.FormatInt(time.Now().UnixNano(), 36))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// harness is a running broker + web server sharing one store, plus a control
// client and an HTTP client.
type harness struct {
	paths  statedir.Paths
	store  *store.Store
	client *broker.Client
	web    *web.Server
	cancel context.CancelFunc
	done   chan struct{}
}

func startHarness(t *testing.T, dir string) *harness {
	t.Helper()
	paths, err := statedir.Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(store.Options{Path: paths.DB})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	secret, err := broker.LoadOrCreateSecret(paths.Secret)
	if err != nil {
		t.Fatal(err)
	}
	b, err := broker.New(broker.Config{Paths: paths, Store: st, Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Listen(); err != nil {
		t.Fatalf("broker.Listen: %v", err)
	}
	wsrv := web.NewServer(st, nil)
	if err := wsrv.Listen(); err != nil {
		t.Fatalf("web.Listen: %v", err)
	}
	b.SetWebBaseURL(wsrv.BaseURL())
	b.SetBootstrapIssuer(wsrv)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 2)
	go func() { _ = b.Serve(ctx); done <- struct{}{} }()
	go func() { _ = wsrv.Serve(ctx); done <- struct{}{} }()

	h := &harness{
		paths:  paths,
		store:  st,
		client: broker.NewClient(paths.Socket, secret),
		web:    wsrv,
		cancel: cancel,
		done:   done,
	}
	return h
}

func (h *harness) stop() {
	h.cancel()
	<-h.done
	<-h.done
	_ = h.store.Close()
}

// browserDecide runs the real bootstrap → cookie → decide flow against the web
// server using an http.Client, mirroring what a browser does.
func browserDecide(t *testing.T, h *harness, sessionID string, decision string, selected []string) *http.Response {
	t.Helper()
	// 1. Mint a bootstrap URL via the broker (as `open` does).
	bootstrapURL, err := h.client.SessionOpen(sessionID)
	if err != nil {
		t.Fatalf("SessionOpen: %v", err)
	}
	jar := &simpleJar{}
	httpClient := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	// 2. Exchange bootstrap for a cookie (the redirect response carries it; the
	// jar stores it automatically).
	resp, err := httpClient.Get(bootstrapURL)
	if err != nil {
		t.Fatalf("bootstrap GET: %v", err)
	}
	resp.Body.Close()

	// 3. Load the review page to obtain the CSRF token.
	pageURL := h.web.BaseURL() + "/session/" + sessionID
	presp, err := httpClient.Get(pageURL)
	if err != nil {
		t.Fatalf("page GET: %v", err)
	}
	pageBody, _ := io.ReadAll(presp.Body)
	presp.Body.Close()
	csrf := extractCSRF(t, string(pageBody))

	// 4. POST the decision as the page script does.
	body, _ := json.Marshal(map[string]any{
		"csrf": csrf, "session": sessionID, "revision": 1,
		"decision": decision, "selected_action_ids": selected,
		"reason": reasonFor(decision),
	})
	dreq, _ := http.NewRequest(http.MethodPost, h.web.BaseURL()+"/api/decide", bytes.NewReader(body))
	dreq.Header.Set("Content-Type", "application/json")
	dreq.Header.Set("Origin", h.web.BaseURL())
	dresp, err := httpClient.Do(dreq)
	if err != nil {
		t.Fatalf("decide POST: %v", err)
	}
	return dresp
}

func reasonFor(decision string) string {
	if decision == "reject" || decision == "request_changes" {
		return "e2e reason"
	}
	return ""
}

func extractCSRF(t *testing.T, page string) string {
	t.Helper()
	const marker = `name="csrf" value="`
	i := strings.Index(page, marker)
	if i < 0 {
		t.Fatal("no csrf token in page")
	}
	rest := page[i+len(marker):]
	return rest[:strings.IndexByte(rest, '"')]
}

func TestEndToEndUsableLoop(t *testing.T) {
	dir := shortDir(t)
	h := startHarness(t, dir)

	// 1. Create a session.
	sess, err := h.client.SessionCreate("ws-e2e", "devtools")
	if err != nil {
		t.Fatalf("SessionCreate: %v", err)
	}

	// 2. Publish a plan (as the CLI does).
	plan := map[string]any{
		"protocol_version": protocol.ProtocolVersion,
		"session_id":       sess.ID,
		"revision":         1,
		"goal":             "wire the usable loop",
		"summary":          "thin vertical slice",
		"workspace":        map[string]any{"id": "ws-e2e", "display_name": "devtools"},
		"blocks": []map[string]any{
			{"id": "b1", "kind": "markdown", "content": "The broker separates review from execution."},
		},
		"actions": []map[string]any{
			{"id": "a1", "kind": "write_patch", "title": "add store", "targets": []string{"workspace://migrations"}, "risk": "workspace_write"},
			{"id": "a2", "kind": "run_command", "title": "run tests", "program": "go", "args": []string{"test", "./..."}, "cwd": "workspace://root", "risk": "local_process"},
		},
	}
	planJSON, _ := json.Marshal(plan)
	if _, err := h.client.PlanPublish(planJSON); err != nil {
		t.Fatalf("PlanPublish: %v", err)
	}

	// 3. Poll before any decision: pending.
	poll, err := h.client.DecisionPoll(sess.ID)
	if err != nil {
		t.Fatalf("DecisionPoll(before): %v", err)
	}
	if poll.Decided {
		t.Fatal("expected pending before browser decision")
	}

	// 4. Browser approves both actions.
	resp := browserDecide(t, h, sess.ID, "approve", []string{"a1", "a2"})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("approve status %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()

	// 5. Poll after: decided approve, with an approval id and digest.
	poll, err = h.client.DecisionPoll(sess.ID)
	if err != nil {
		t.Fatalf("DecisionPoll(after): %v", err)
	}
	if !poll.Decided || poll.Decision.Kind != store.DecisionApprove {
		t.Fatalf("expected approve decision, got %+v", poll)
	}

	// The digest is surfaced in the decision so an adapter can claim directly.
	digest := poll.Decision.Digest
	if digest == "" {
		t.Fatal("decision poll did not surface the approval digest")
	}

	// 6. Claim the approval exactly once.
	claimRaw, err := h.client.ApprovalClaim(sess.ID, digest)
	if err != nil {
		t.Fatalf("ApprovalClaim: %v", err)
	}
	var claim broker.ApprovalClaimResult
	_ = json.Unmarshal(claimRaw, &claim)
	if claim.ClaimSeq != 1 {
		t.Fatalf("claim seq = %d, want 1", claim.ClaimSeq)
	}

	// 7. Restart the broker (same state dir).
	h.stop()
	h2 := startHarness(t, dir)

	// 7a. Decision remains durable after restart.
	poll2, err := h2.client.DecisionPoll(sess.ID)
	if err != nil || !poll2.Decided || poll2.Decision.Kind != store.DecisionApprove {
		t.Fatalf("decision not durable across restart: %+v err=%v", poll2, err)
	}

	// 8. Replaying the claim after restart fails closed (exhausted).
	_, err = h2.client.ApprovalClaim(sess.ID, digest)
	var ce *broker.CallError
	if err == nil || !errors.As(err, &ce) || ce.Code != broker.CodeExhausted {
		t.Fatalf("expected claims_exhausted on replay after restart, got %v", err)
	}
	h2.stop()
}

// TestEndToEndRejectPath exercises the reject branch through the browser flow.
func TestEndToEndRejectPath(t *testing.T) {
	dir := shortDir(t)
	h := startHarness(t, dir)
	defer h.stop()

	sess, err := h.client.SessionCreate("ws-rej", "devtools")
	if err != nil {
		t.Fatal(err)
	}
	plan := map[string]any{
		"protocol_version": protocol.ProtocolVersion,
		"session_id":       sess.ID,
		"revision":         1,
		"goal":             "reject me",
		"workspace":        map[string]any{"id": "ws-rej", "display_name": "devtools"},
		"actions": []map[string]any{
			{"id": "a1", "kind": "write_patch", "title": "x", "risk": "workspace_write"},
		},
	}
	planJSON, _ := json.Marshal(plan)
	if _, err := h.client.PlanPublish(planJSON); err != nil {
		t.Fatal(err)
	}
	resp := browserDecide(t, h, sess.ID, "reject", nil)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("reject status %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()

	poll, err := h.client.DecisionPoll(sess.ID)
	if err != nil || !poll.Decided || poll.Decision.Kind != store.DecisionReject {
		t.Fatalf("expected reject decision, got %+v err=%v", poll, err)
	}
}

// simpleJar is a minimal cookie jar sufficient for the single loopback origin
// used in this test. It ignores the URL and returns all stored cookies.
type simpleJar struct{ cookies []*http.Cookie }

func (j *simpleJar) SetCookies(_ *url.URL, cs []*http.Cookie) { j.cookies = append(j.cookies, cs...) }
func (j *simpleJar) Cookies(_ *url.URL) []*http.Cookie        { return j.cookies }
