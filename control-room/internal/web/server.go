// Package web is Control Room's loopback browser interface: a narrow
// presentation-and-decision surface bound only to 127.0.0.1 on an
// OS-assigned random port.
//
// It is defense-in-depth even on loopback (binding to localhost is routing, not
// authorization). Every requirement in the RFC's "Browser security" section is
// enforced here: exact Host validation including the active port, a one-time
// expiring bootstrap capability exchanged for an HttpOnly SameSite=Strict
// cookie, a capability-free redirect, a strict CSP with no remote assets, and
// same-origin + CSRF checks on every mutation. The page renders plan content as
// escaped plaintext (never raw agent HTML) and stores decisions durably through
// the shared store.
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/interactionlabs/devtools/control-room/internal/store"
)

// Server is the loopback HTTP server. It shares the broker's store so browser
// decisions are durable and immediately visible to agent polls.
type Server struct {
	store *store.Store
	now   func() time.Time

	ln      net.Listener
	httpSrv *http.Server
	host    string // exact "127.0.0.1:PORT" this server answers for

	mu sync.Mutex
	// bootstraps maps a one-time bootstrap token to its issuance record.
	bootstraps map[string]bootstrap
	// sessionCookies maps a browser session cookie value to a browser session.
	cookies map[string]browserSession
}

// bootstrap is a one-time capability that a freshly-opened browser exchanges
// for a cookie. It names the control-room session it grants review access to
// and expires quickly.
type bootstrap struct {
	sessionID string
	expiresAt time.Time
}

// browserSession is an authenticated browser session, keyed by an HttpOnly
// cookie. It carries the CSRF token required on mutations and the control-room
// session it is scoped to.
type browserSession struct {
	sessionID string
	csrfToken string
	expiresAt time.Time
}

// bootstrapTTL is how long a one-time bootstrap token remains valid before
// exchange. It is short because the token travels in the opened URL and is
// consumed immediately by the browser's first navigation.
const bootstrapTTL = 60 * time.Second

// cookieTTL bounds a browser session's lifetime.
const cookieTTL = 30 * time.Minute

// cookieName is the browser session cookie. The __Host- prefix is intentionally
// NOT used because it requires the Secure attribute, which a plain-HTTP loopback
// origin cannot set; instead we rely on HttpOnly + SameSite=Strict + the
// loopback bind + Host/Origin checks.
const cookieName = "cr_session"

// NewServer constructs a web server sharing the given store. now defaults to
// time.Now.
func NewServer(st *store.Store, now func() time.Time) *Server {
	if now == nil {
		now = time.Now
	}
	return &Server{
		store:      st,
		now:        now,
		bootstraps: make(map[string]bootstrap),
		cookies:    make(map[string]browserSession),
	}
}

// Listen binds 127.0.0.1 on an OS-assigned random port. It fails closed if it
// cannot bind loopback; it never binds a wildcard or LAN address.
func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("web: binding loopback: %w", err)
	}
	s.ln = ln
	s.host = ln.Addr().String()
	s.httpSrv = &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	return nil
}

// Host returns the exact "127.0.0.1:PORT" this server answers for.
func (s *Server) Host() string { return s.host }

// BaseURL returns the http base URL of the server.
func (s *Server) BaseURL() string { return "http://" + s.host }

// Serve runs the HTTP server until ctx is cancelled, then shuts it down
// gracefully.
func (s *Server) Serve(ctx context.Context) error {
	if s.ln == nil {
		return fmt.Errorf("web: Serve called before Listen")
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutCtx)
	}()
	err := s.httpSrv.Serve(s.ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// maxBootstraps and maxCookies bound the in-memory maps so a flood of
// unredeemed bootstraps or browser sessions cannot grow memory without limit.
// These are generous for a single local user; when the cap is reached, expired
// entries are pruned first, and if still at the cap the oldest-expiring entry is
// evicted. This is intentionally simple bounded cleanup, not a cache framework.
const (
	maxBootstraps = 256
	maxCookies    = 256
)

// IssueBootstrap mints a one-time bootstrap capability for a control-room
// session and returns the review URL a browser should open. The URL carries the
// token as a query parameter; the first navigation exchanges it for a cookie
// and is redirected to a capability-free URL.
func (s *Server) IssueBootstrap(sessionID string) (string, error) {
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.pruneBootstrapsLocked()
	s.bootstraps[tok] = bootstrap{sessionID: sessionID, expiresAt: s.now().Add(bootstrapTTL)}
	s.mu.Unlock()
	// The bootstrap endpoint is capability-carrying; it redirects to /session/:id.
	return fmt.Sprintf("%s/bootstrap?token=%s&session=%s", s.BaseURL(), tok, sessionID), nil
}

// pruneBootstrapsLocked removes expired bootstrap tokens and, if still at the
// cap, evicts the soonest-expiring entry. Caller must hold s.mu.
func (s *Server) pruneBootstrapsLocked() {
	now := s.now()
	for tok, bs := range s.bootstraps {
		if !now.Before(bs.expiresAt) {
			delete(s.bootstraps, tok)
		}
	}
	for len(s.bootstraps) >= maxBootstraps {
		var oldestTok string
		var oldest time.Time
		for tok, bs := range s.bootstraps {
			if oldestTok == "" || bs.expiresAt.Before(oldest) {
				oldestTok, oldest = tok, bs.expiresAt
			}
		}
		if oldestTok == "" {
			break
		}
		delete(s.bootstraps, oldestTok)
	}
}

// consumeBootstrap validates and single-use-consumes a bootstrap token for a
// session. It returns false if the token is unknown, expired, or bound to a
// different session.
func (s *Server) consumeBootstrap(tok, sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	bs, ok := s.bootstraps[tok]
	if !ok {
		return false
	}
	// One-time: remove regardless of outcome so it cannot be replayed.
	delete(s.bootstraps, tok)
	if !s.now().Before(bs.expiresAt) {
		return false
	}
	if bs.sessionID != sessionID {
		return false
	}
	return true
}

// newBrowserSession creates a cookie-backed browser session and returns the
// cookie value.
func (s *Server) newBrowserSession(sessionID string) (string, browserSession, error) {
	cookieVal, err := randomToken()
	if err != nil {
		return "", browserSession{}, err
	}
	csrf, err := randomToken()
	if err != nil {
		return "", browserSession{}, err
	}
	bsn := browserSession{sessionID: sessionID, csrfToken: csrf, expiresAt: s.now().Add(cookieTTL)}
	s.mu.Lock()
	s.pruneCookiesLocked()
	s.cookies[cookieVal] = bsn
	s.mu.Unlock()
	return cookieVal, bsn, nil
}

// pruneCookiesLocked removes expired browser sessions and, if still at the cap,
// evicts the soonest-expiring entry. Caller must hold s.mu.
func (s *Server) pruneCookiesLocked() {
	now := s.now()
	for k, bsn := range s.cookies {
		if !now.Before(bsn.expiresAt) {
			delete(s.cookies, k)
		}
	}
	for len(s.cookies) >= maxCookies {
		var oldestKey string
		var oldest time.Time
		for k, bsn := range s.cookies {
			if oldestKey == "" || bsn.expiresAt.Before(oldest) {
				oldestKey, oldest = k, bsn.expiresAt
			}
		}
		if oldestKey == "" {
			break
		}
		delete(s.cookies, oldestKey)
	}
}

// lookupBrowserSession returns the browser session for a cookie value, or false
// if unknown/expired.
func (s *Server) lookupBrowserSession(cookieVal string) (browserSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bsn, ok := s.cookies[cookieVal]
	if !ok {
		return browserSession{}, false
	}
	if !s.now().Before(bsn.expiresAt) {
		delete(s.cookies, cookieVal)
		return browserSession{}, false
	}
	return bsn, true
}

// randomToken returns a 256-bit URL-safe hex token.
func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("web: generating token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// tokensEqual compares tokens in constant time.
func tokensEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
