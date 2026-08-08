package web

import (
	"net/http"
	"strings"
)

// routes builds the HTTP handler. Every route passes through securityHeaders,
// which sets the strict CSP and frame protections and validates Host on every
// request (defense against DNS-rebinding-style hostile Host values).
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/session/", s.handleSessionPage)
	mux.HandleFunc("/api/decide", s.handleDecide)
	mux.HandleFunc("/static/app.js", s.handleAppJS)
	mux.HandleFunc("/static/app.css", s.handleAppCSS)
	mux.HandleFunc("/", s.handleRoot)
	return s.securityHeaders(mux)
}

// securityHeaders validates Host and sets response security headers on every
// request. Host validation is the anti-rebinding control: the browser must
// address us by the exact loopback host:port we bound, or the request is
// refused before any handler runs.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != s.host {
			http.Error(w, "bad host", http.StatusMisdirectedRequest)
			return
		}
		h := w.Header()
		// No remote anything. script-src 'self' allows only our bundled app.js;
		// no 'unsafe-inline' is needed because there is no inline script.
		h.Set("Content-Security-Policy",
			"default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
				"font-src 'self'; connect-src 'self'; frame-src 'none'; object-src 'none'; "+
				"base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// handleRoot rejects any unmatched path. There is no index; a browser reaches
// the tool only via an issued bootstrap URL.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

// handleBootstrap exchanges a one-time bootstrap token for an HttpOnly,
// SameSite=Strict cookie and immediately redirects to the capability-free
// session URL, removing the token from the visible URL.
func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tok := r.URL.Query().Get("token")
	sessionID := r.URL.Query().Get("session")
	if tok == "" || sessionID == "" {
		http.Error(w, "missing bootstrap parameters", http.StatusBadRequest)
		return
	}
	if !s.consumeBootstrap(tok, sessionID) {
		http.Error(w, "invalid or expired bootstrap", http.StatusForbidden)
		return
	}
	cookieVal, _, err := s.newBrowserSession(sessionID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    cookieVal,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		// Secure is intentionally omitted: a plain-HTTP loopback origin cannot
		// present a valid TLS connection, and setting Secure would drop the
		// cookie. HttpOnly + SameSite=Strict + loopback + Host/Origin checks are
		// the controls here.
	})
	// Redirect to the capability-free URL so the token never lingers in history.
	http.Redirect(w, r, "/session/"+sessionID, http.StatusSeeOther)
}

// authBrowser resolves and validates the browser session cookie, ensuring it is
// scoped to the requested control-room session. It returns the session and true
// on success; on failure it writes an error response and returns false.
func (s *Server) authBrowser(w http.ResponseWriter, r *http.Request, sessionID string) (browserSession, bool) {
	ck, err := r.Cookie(cookieName)
	if err != nil {
		http.Error(w, "no session cookie", http.StatusForbidden)
		return browserSession{}, false
	}
	bsn, ok := s.lookupBrowserSession(ck.Value)
	if !ok {
		http.Error(w, "expired session", http.StatusForbidden)
		return browserSession{}, false
	}
	if bsn.sessionID != sessionID {
		// A cookie scoped to another session must not review this one.
		http.Error(w, "session mismatch", http.StatusForbidden)
		return browserSession{}, false
	}
	return bsn, true
}

// sessionIDFromPath extracts the control-room session id from /session/:id.
func sessionIDFromPath(path string) string {
	const prefix = "/session/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	return strings.TrimPrefix(path, prefix)
}
