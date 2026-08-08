package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/interactionlabs/devtools/control-room/internal/protocol"
	"github.com/interactionlabs/devtools/control-room/internal/store"
)

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// testWeb sets up a store with a published plan and a running httptest server
// wrapping the web handler. It returns the server, the web.Server, the store,
// the clock, and the created session id.
func testWeb(t *testing.T) (*httptest.Server, *Server, *store.Store, *testClock, string) {
	t.Helper()
	clock := &testClock{t: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)}
	st, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "web.db"), Now: clock.now})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sess, err := st.CreateSession("cr-sess", "ws-1", "devtools")
	if err != nil {
		t.Fatal(err)
	}
	plan := &protocol.Plan{
		ProtocolVersion: protocol.ProtocolVersion,
		SessionID:       sess.ID,
		Revision:        1,
		Goal:            "do it <script>alert(1)</script>",
		Summary:         "safe",
		Workspace:       protocol.Workspace{ID: "ws-1", DisplayName: "devtools"},
		Actions: []protocol.Action{
			{ID: "a1", Kind: protocol.ActionWritePatch, Title: "patch", Risk: protocol.RiskWorkspaceWrite},
			{ID: "a2", Kind: protocol.ActionRunCommand, Title: "test", Program: "go", Args: []string{"test"}, Cwd: "workspace://root", Risk: protocol.RiskLocalProcess},
		},
	}
	if _, err := st.PublishPlan(plan); err != nil {
		t.Fatal(err)
	}

	ws := NewServer(st, clock.now)
	// The web.Server needs its host to match the httptest server. httptest
	// assigns a 127.0.0.1:port; we set ws.host to it so Host/Origin checks pass.
	ts := httptest.NewServer(ws.routes())
	t.Cleanup(ts.Close)
	ws.host = strings.TrimPrefix(ts.URL, "http://")

	return ts, ws, st, clock, sess.ID
}

// doGET issues a GET with the correct Host, following no redirects, returning
// the response.
func doGET(t *testing.T, ts *httptest.Server, path string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func TestHostValidationRejectsForeignHost(t *testing.T) {
	ts, _, _, _, sid := testWeb(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/session/"+sid, nil)
	req.Host = "evil.example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("foreign Host: status = %d, want 421", resp.StatusCode)
	}
}

func TestBootstrapExchangeSetsCookieAndRedirects(t *testing.T) {
	ts, ws, _, _, sid := testWeb(t)
	url, err := ws.IssueBootstrap(sid)
	if err != nil {
		t.Fatal(err)
	}
	// Extract query from the issued URL and hit the httptest server.
	idx := strings.Index(url, "/bootstrap")
	resp := doGET(t, ts, url[idx:], nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("bootstrap status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/session/"+sid {
		t.Fatalf("redirect to %q, want capability-free session url", loc)
	}
	var found bool
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			found = true
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Fatalf("cookie not HttpOnly+SameSite=Strict: %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("expected session cookie to be set")
	}
}

func TestBootstrapIsSingleUse(t *testing.T) {
	ts, ws, _, _, sid := testWeb(t)
	url, _ := ws.IssueBootstrap(sid)
	idx := strings.Index(url, "/bootstrap")
	first := doGET(t, ts, url[idx:], nil)
	first.Body.Close()
	if first.StatusCode != http.StatusSeeOther {
		t.Fatalf("first exchange status %d", first.StatusCode)
	}
	// Replaying the same token must fail.
	second := doGET(t, ts, url[idx:], nil)
	second.Body.Close()
	if second.StatusCode != http.StatusForbidden {
		t.Fatalf("replayed bootstrap status = %d, want 403", second.StatusCode)
	}
}

func TestBootstrapExpires(t *testing.T) {
	ts, ws, _, clock, sid := testWeb(t)
	url, _ := ws.IssueBootstrap(sid)
	clock.advance(2 * time.Minute) // beyond bootstrapTTL
	idx := strings.Index(url, "/bootstrap")
	resp := doGET(t, ts, url[idx:], nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expired bootstrap status = %d, want 403", resp.StatusCode)
	}
}

// authedCookie runs the bootstrap exchange and returns the resulting cookie.
func authedCookie(t *testing.T, ts *httptest.Server, ws *Server, sid string) *http.Cookie {
	t.Helper()
	url, _ := ws.IssueBootstrap(sid)
	idx := strings.Index(url, "/bootstrap")
	resp := doGET(t, ts, url[idx:], nil)
	resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	t.Fatal("no cookie issued")
	return nil
}

func TestSessionPageRequiresCookie(t *testing.T) {
	ts, _, _, _, sid := testWeb(t)
	resp := doGET(t, ts, "/session/"+sid, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("uncookied page status = %d, want 403", resp.StatusCode)
	}
}

func TestSessionPageEscapesPlanContent(t *testing.T) {
	ts, ws, _, _, sid := testWeb(t)
	cookie := authedCookie(t, ts, ws, sid)
	resp := doGET(t, ts, "/session/"+sid, cookie)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page status = %d", resp.StatusCode)
	}
	// The goal contained a <script> tag; it must appear escaped, never raw.
	if strings.Contains(string(body), "<script>alert(1)</script>") {
		t.Fatal("plan content was rendered as raw HTML — XSS")
	}
	if !strings.Contains(string(body), "&lt;script&gt;") {
		t.Fatal("expected escaped plan content in page")
	}
}

// csrfFromPage fetches the page and extracts the CSRF token.
func csrfFromPage(t *testing.T, ts *httptest.Server, cookie *http.Cookie, sid string) string {
	t.Helper()
	resp := doGET(t, ts, "/session/"+sid, cookie)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	const marker = `name="csrf" value="`
	i := strings.Index(string(body), marker)
	if i < 0 {
		t.Fatal("csrf token not found on page")
	}
	rest := string(body)[i+len(marker):]
	j := strings.IndexByte(rest, '"')
	return rest[:j]
}

// postDecide posts a decision with configurable origin/csrf/content-type.
func postDecide(t *testing.T, ts *httptest.Server, ws *Server, cookie *http.Cookie, origin, contentType string, body map[string]any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/decide", bytes.NewReader(b))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST decide: %v", err)
	}
	return resp
}

func TestDecideRejectsForeignOrigin(t *testing.T) {
	ts, ws, _, _, sid := testWeb(t)
	cookie := authedCookie(t, ts, ws, sid)
	csrf := csrfFromPage(t, ts, cookie, sid)
	resp := postDecide(t, ts, ws, cookie, "http://evil.example.com", "application/json", map[string]any{
		"csrf": csrf, "session": sid, "revision": 1, "decision": "approve", "selected_action_ids": []string{"a1"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin status = %d, want 403", resp.StatusCode)
	}
}

func TestDecideRejectsMissingOrigin(t *testing.T) {
	ts, ws, _, _, sid := testWeb(t)
	cookie := authedCookie(t, ts, ws, sid)
	csrf := csrfFromPage(t, ts, cookie, sid)
	resp := postDecide(t, ts, ws, cookie, "", "application/json", map[string]any{
		"csrf": csrf, "session": sid, "revision": 1, "decision": "approve",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing origin status = %d, want 403", resp.StatusCode)
	}
}

func TestDecideRejectsBadCSRF(t *testing.T) {
	ts, ws, _, _, sid := testWeb(t)
	cookie := authedCookie(t, ts, ws, sid)
	resp := postDecide(t, ts, ws, cookie, ws.BaseURL(), "application/json", map[string]any{
		"csrf": "not-the-token", "session": sid, "revision": 1, "decision": "approve", "selected_action_ids": []string{"a1"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bad csrf status = %d, want 403", resp.StatusCode)
	}
}

func TestDecideRejectsNonJSON(t *testing.T) {
	ts, ws, _, _, sid := testWeb(t)
	cookie := authedCookie(t, ts, ws, sid)
	csrf := csrfFromPage(t, ts, cookie, sid)
	resp := postDecide(t, ts, ws, cookie, ws.BaseURL(), "text/plain", map[string]any{
		"csrf": csrf, "session": sid, "revision": 1, "decision": "approve",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("non-json status = %d, want 415", resp.StatusCode)
	}
}

func TestDecideApproveHappyPathStoresDurableApproval(t *testing.T) {
	ts, ws, st, _, sid := testWeb(t)
	cookie := authedCookie(t, ts, ws, sid)
	csrf := csrfFromPage(t, ts, cookie, sid)
	resp := postDecide(t, ts, ws, cookie, ws.BaseURL(), "application/json", map[string]any{
		"csrf": csrf, "session": sid, "revision": 1, "decision": "approve", "selected_action_ids": []string{"a1", "a2"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("approve status = %d body=%s", resp.StatusCode, body)
	}
	d, err := st.GetDecision(sid)
	if err != nil || d.Kind != store.DecisionApprove || d.ApprovalID == "" {
		t.Fatalf("durable approval not recorded: %+v err=%v", d, err)
	}
}

func TestDecideStaleRevisionFailsClosed(t *testing.T) {
	ts, ws, st, _, sid := testWeb(t)
	cookie := authedCookie(t, ts, ws, sid)
	csrf := csrfFromPage(t, ts, cookie, sid)
	// Publish revision 2 so the browser's revision 1 is now stale.
	p2 := &protocol.Plan{
		ProtocolVersion: protocol.ProtocolVersion, SessionID: sid, Revision: 2,
		Goal: "v2", Workspace: protocol.Workspace{ID: "ws-1", DisplayName: "devtools"},
		Actions: []protocol.Action{{ID: "a1", Kind: protocol.ActionWritePatch, Title: "p", Risk: protocol.RiskWorkspaceWrite}},
	}
	if _, err := st.PublishPlan(p2); err != nil {
		t.Fatal(err)
	}
	resp := postDecide(t, ts, ws, cookie, ws.BaseURL(), "application/json", map[string]any{
		"csrf": csrf, "session": sid, "revision": 1, "decision": "approve", "selected_action_ids": []string{"a1"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale revision decision status = %d, want 409", resp.StatusCode)
	}
}

func TestDecideRejectRequiresReason(t *testing.T) {
	ts, ws, _, _, sid := testWeb(t)
	cookie := authedCookie(t, ts, ws, sid)
	csrf := csrfFromPage(t, ts, cookie, sid)
	resp := postDecide(t, ts, ws, cookie, ws.BaseURL(), "application/json", map[string]any{
		"csrf": csrf, "session": sid, "revision": 1, "decision": "reject", "reason": "",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("reject without reason status = %d, want 400", resp.StatusCode)
	}
}

func TestCSPHeaderPresent(t *testing.T) {
	ts, ws, _, _, sid := testWeb(t)
	cookie := authedCookie(t, ts, ws, sid)
	resp := doGET(t, ts, "/session/"+sid, cookie)
	defer resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP missing %q: %s", want, csp)
		}
	}
}

func TestBootstrapMapIsBounded(t *testing.T) {
	_, ws, _, _, _ := testWeb(t)
	// Issue far more bootstraps than the cap; the map must stay bounded.
	for i := 0; i < maxBootstraps*3; i++ {
		if _, err := ws.IssueBootstrap("cr-sess"); err != nil {
			t.Fatalf("IssueBootstrap %d: %v", i, err)
		}
	}
	ws.mu.Lock()
	n := len(ws.bootstraps)
	ws.mu.Unlock()
	if n > maxBootstraps {
		t.Fatalf("bootstrap map grew to %d, exceeding cap %d", n, maxBootstraps)
	}
}

func TestBootstrapMapPrunesExpired(t *testing.T) {
	_, ws, _, clock, _ := testWeb(t)
	for i := 0; i < 10; i++ {
		if _, err := ws.IssueBootstrap("cr-sess"); err != nil {
			t.Fatal(err)
		}
	}
	// Advance beyond bootstrapTTL, then issue one more; the prune-on-issue path
	// must drop all the now-expired entries.
	clock.advance(2 * time.Minute)
	if _, err := ws.IssueBootstrap("cr-sess"); err != nil {
		t.Fatal(err)
	}
	ws.mu.Lock()
	n := len(ws.bootstraps)
	ws.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected only the freshly-issued bootstrap to remain, got %d", n)
	}
}

func TestCookieMapIsBounded(t *testing.T) {
	_, ws, _, _, _ := testWeb(t)
	for i := 0; i < maxCookies*3; i++ {
		if _, _, err := ws.newBrowserSession("cr-sess"); err != nil {
			t.Fatalf("newBrowserSession %d: %v", i, err)
		}
	}
	ws.mu.Lock()
	n := len(ws.cookies)
	ws.mu.Unlock()
	if n > maxCookies {
		t.Fatalf("cookie map grew to %d, exceeding cap %d", n, maxCookies)
	}
}
