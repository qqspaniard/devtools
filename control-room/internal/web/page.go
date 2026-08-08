package web

import (
	_ "embed"
	"html/template"
	"net/http"
	"time"

	"github.com/interactionlabs/devtools/control-room/internal/policy"
	"github.com/interactionlabs/devtools/control-room/internal/protocol"
	"github.com/interactionlabs/devtools/control-room/internal/store"
)

//go:embed templates/session.html
var sessionTemplateSrc string

//go:embed static/app.js
var appJS []byte

//go:embed static/app.css
var appCSS []byte

// sessionTemplate renders the review page. It uses html/template, which
// context-escapes all interpolated values, so agent-authored plan content is
// rendered as escaped plaintext and can never inject markup or script. Raw HTML
// is never emitted.
var sessionTemplate = template.Must(template.New("session").Parse(sessionTemplateSrc))

// pageData is the view model for the review page.
type pageData struct {
	Session   *store.Session
	Plan      *protocol.Plan
	CSRFToken string
	Decision  *store.Decision
	Decided   bool
	Events    []store.Event
	// Actions carries per-action display rows with their default-selected flag.
	Actions []actionRow
}

type actionRow struct {
	ID    string
	Kind  string
	Title string
	Risk  string
}

// handleAppJS serves the tiny bundled same-origin script. It is the only script
// permitted by the CSP (script-src 'self'). The script sends decisions as a
// same-origin fetch with the CSRF token, guaranteeing an Origin header is
// present on every mutation (plain form POSTs may omit Origin).
func (s *Server) handleAppJS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write(appJS)
}

// handleAppCSS serves the bundled stylesheet (style-src 'self').
func (s *Server) handleAppCSS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(appCSS)
}

// handleSessionPage renders the SSR review page for a control-room session. It
// requires a valid, session-scoped browser cookie.
func (s *Server) handleSessionPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := sessionIDFromPath(r.URL.Path)
	if sessionID == "" {
		http.NotFound(w, r)
		return
	}
	bsn, ok := s.authBrowser(w, r, sessionID)
	if !ok {
		return
	}

	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	data := pageData{Session: sess, CSRFToken: bsn.csrfToken}

	if sess.CurrentRevision >= 1 {
		plan, err := s.store.LoadCurrentPlan(sessionID)
		if err == nil {
			data.Plan = plan
			for _, a := range plan.Actions {
				data.Actions = append(data.Actions, actionRow{
					ID: a.ID, Kind: string(a.Kind), Title: a.Title, Risk: string(a.Risk),
				})
			}
		}
	}

	if d, err := s.store.GetDecision(sessionID); err == nil {
		data.Decision = d
		data.Decided = true
	}

	if evs, err := s.store.Events(sessionID, 100); err == nil {
		data.Events = evs
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := sessionTemplate.Execute(w, data); err != nil {
		// The header is already partially written; nothing more we can safely do.
		return
	}
}

// buildApprovalForSelection constructs a digest-bound approval for the browser's
// selected actions using the Phase 0 policy. Expiration is DefaultApprovalTTL
// from now. It is called only when the user approves.
func (s *Server) buildApprovalForSelection(plan *protocol.Plan, selected []string) (*policy.Approval, error) {
	return policy.BuildApproval(policy.ApprovalRequest{
		Plan:            plan,
		SelectedActions: selected,
		ExpiresAt:       s.now().Add(defaultApprovalTTL),
		MaxClaims:       1,
	})
}

// defaultApprovalTTL is the browser approval lifetime. Kept short: an approval
// is a live capability and a stale one should fail closed.
const defaultApprovalTTL = 15 * time.Minute
