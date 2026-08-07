package broker

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/interactionlabs/devtools/control-room/internal/protocol"
	"github.com/interactionlabs/devtools/control-room/internal/statedir"
	"github.com/interactionlabs/devtools/control-room/internal/store"
)

// shortStateDir returns a state directory whose path is short enough that the
// broker.sock inside it stays under the OS sockaddr_un limit. macOS t.TempDir()
// paths are too long for a Unix socket, so tests that bind a socket must use a
// short base. The directory is removed on cleanup.
func shortStateDir(t *testing.T) string {
	t.Helper()
	base := "/tmp/opencode/cr-test"
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	dir := filepath.Join(base, strconv.FormatInt(time.Now().UnixNano(), 36))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// testBroker spins up a real broker on a real Unix socket in a temp dir and
// returns a client plus the raw socket path and secret. It is torn down via
// t.Cleanup.
func testBroker(t *testing.T) (*Client, statedir.Paths, string) {
	t.Helper()
	dir := shortStateDir(t)
	paths, err := statedir.Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := paths.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	st, err := store.Open(store.Options{Path: paths.DB})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	secret, err := LoadOrCreateSecret(paths.Secret)
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	b, err := New(Config{Paths: paths, Store: st, Secret: secret})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := b.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = b.Serve(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = st.Close()
	})
	return NewClient(paths.Socket, secret), paths, secret
}

func TestBrokerFullJourney(t *testing.T) {
	c, _, _ := testBroker(t)

	sess, err := c.SessionCreate("ws-1", "devtools")
	if err != nil {
		t.Fatalf("SessionCreate: %v", err)
	}
	if sess.State != "draft" {
		t.Fatalf("new session state = %q", sess.State)
	}

	plan := map[string]any{
		"protocol_version": protocol.ProtocolVersion,
		"session_id":       sess.ID,
		"revision":         1,
		"goal":             "do it",
		"summary":          "slice",
		"workspace":        map[string]any{"id": "ws-1", "display_name": "devtools"},
		"actions": []map[string]any{
			{"id": "a1", "kind": "write_patch", "title": "patch", "risk": "workspace_write"},
		},
	}
	planJSON, _ := json.Marshal(plan)
	pubSess, err := c.PlanPublish(planJSON)
	if err != nil {
		t.Fatalf("PlanPublish: %v", err)
	}
	if pubSess.State != "awaiting_approval" || pubSess.CurrentRevision != 1 {
		t.Fatalf("unexpected session after publish: %+v", pubSess)
	}

	// No decision yet.
	poll, err := c.DecisionPoll(sess.ID)
	if err != nil {
		t.Fatalf("DecisionPoll: %v", err)
	}
	if poll.Decided {
		t.Fatal("expected undecided before user acts")
	}

	// session.get round-trips.
	got, err := c.SessionGet(sess.ID)
	if err != nil || got.ID != sess.ID {
		t.Fatalf("SessionGet: %v (%+v)", err, got)
	}
}

func TestBrokerRejectsWrongSecret(t *testing.T) {
	_, paths, _ := testBroker(t)
	bad := NewClient(paths.Socket, "deadbeef")
	_, err := bad.SessionCreate("ws-1", "d")
	var ce *CallError
	if err == nil || !errors.As(err, &ce) || ce.Code != CodeUnauthorized {
		t.Fatalf("expected unauthorized, got %v", err)
	}
}

func TestBrokerRejectsWrongVersion(t *testing.T) {
	_, paths, secret := testBroker(t)
	// Hand-craft a request with a bad version.
	req := Request{Version: 999, Secret: secret, Op: OpSessionGet}
	resp := rawCall(t, paths.Socket, req)
	if resp.OK || resp.Code != CodeUnsupported {
		t.Fatalf("expected unsupported_version, got %+v", resp)
	}
}

func TestBrokerRejectsUnknownOp(t *testing.T) {
	_, paths, secret := testBroker(t)
	req := Request{Version: BrokerProtocolVersion, Secret: secret, Op: Op("frobnicate")}
	resp := rawCall(t, paths.Socket, req)
	if resp.OK || resp.Code != CodeUnknownOp {
		t.Fatalf("expected unknown_op, got %+v", resp)
	}
}

func TestBrokerRejectsUnknownPayloadField(t *testing.T) {
	_, paths, secret := testBroker(t)
	// A payload with a field the op struct does not declare must fail closed.
	payload := json.RawMessage(`{"workspace_id":"ws-1","surprise":true}`)
	req := Request{Version: BrokerProtocolVersion, Secret: secret, Op: OpSessionCreate, Payload: payload}
	resp := rawCall(t, paths.Socket, req)
	if resp.OK || resp.Code != CodeBadRequest {
		t.Fatalf("expected bad_request on unknown field, got %+v", resp)
	}
}

func TestBrokerRejectsOversizedFrame(t *testing.T) {
	_, paths, _ := testBroker(t)
	conn, err := net.DialTimeout("unix", paths.Socket, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	// Write a length header claiming more than MaxFrameBytes. The broker must
	// reject it via the frame codec before allocating.
	hdr := []byte{0xFF, 0xFF, 0xFF, 0xFF} // ~4 GiB
	if _, err := conn.Write(hdr); err != nil {
		t.Fatalf("write header: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := protocol.ReadFrame(conn)
	if err != nil {
		// A closed connection is also acceptable fail-closed behavior.
		return
	}
	var r Response
	_ = json.Unmarshal(resp, &r)
	if r.OK {
		t.Fatalf("expected rejection of oversized frame, got %+v", r)
	}
}

func TestBrokerStaleSocketRebind(t *testing.T) {
	dir := shortStateDir(t)
	paths, _ := statedir.Resolve(dir)
	_ = paths.Ensure()
	// Create a stale socket file with no listener behind it.
	f, err := net.Listen("unix", paths.Socket)
	if err != nil {
		t.Fatalf("pre-listen: %v", err)
	}
	_ = f.Close() // leaves the socket file on disk in some OSes; simulate stale

	st, _ := store.Open(store.Options{Path: paths.DB})
	defer st.Close()
	secret, _ := LoadOrCreateSecret(paths.Secret)
	b, _ := New(Config{Paths: paths, Store: st, Secret: secret})
	// Listen should detect the stale socket and rebind without error.
	if err := b.Listen(); err != nil {
		t.Fatalf("expected stale socket to be reclaimed, got %v", err)
	}
}

// rawCall sends a hand-crafted request and returns the decoded response.
func rawCall(t *testing.T, socket string, req Request) Response {
	t.Helper()
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	b, _ := json.Marshal(req)
	if err := protocol.WriteFrame(conn, b); err != nil {
		t.Fatalf("write: %v", err)
	}
	respBytes, err := protocol.ReadFrame(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp Response
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}
