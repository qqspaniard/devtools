package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/interactionlabs/devtools/control-room/internal/policy"
	"github.com/interactionlabs/devtools/control-room/internal/store"
)

// maxDecideBody bounds the decision request body.
const maxDecideBody = 64 * 1024

// decideRequest is the JSON body posted by the review page's script.
type decideRequest struct {
	CSRF              string   `json:"csrf"`
	Session           string   `json:"session"`
	Revision          int      `json:"revision"`
	Decision          string   `json:"decision"`
	Reason            string   `json:"reason"`
	SelectedActionIDs []string `json:"selected_action_ids"`
}

// decideResponse is the JSON reply.
type decideResponse struct {
	OK       bool   `json:"ok"`
	Decision string `json:"decision,omitempty"`
	Error    string `json:"error,omitempty"`
}

// handleDecide records a user's approve/reject/request-changes decision. It is
// the single browser mutation endpoint and enforces, in order:
//
//   - POST only;
//   - application/json content type;
//   - exact same-origin Origin header (rejects missing/foreign/null);
//   - a valid, session-scoped browser cookie;
//   - a CSRF token matching the cookie's token (constant-time);
//   - a bounded body;
//   - a decision that targets the session's CURRENT revision (stale fails
//     closed in the store).
//
// On approve it builds a digest-bound approval from the loaded plan and the
// selected actions and stores it durably with the decision, all inside the
// store transaction.
func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, decideResponse{Error: "method not allowed"})
		return
	}
	if ct := r.Header.Get("Content-Type"); !isJSONContentType(ct) {
		writeJSON(w, http.StatusUnsupportedMediaType, decideResponse{Error: "content-type must be application/json"})
		return
	}
	// Same-origin Origin check: the Origin must be exactly our loopback origin.
	// A missing, null, or foreign Origin is rejected — a foreign site cannot set
	// a matching Origin, and same-origin fetch always includes it.
	origin := r.Header.Get("Origin")
	if origin != s.BaseURL() {
		writeJSON(w, http.StatusForbidden, decideResponse{Error: "bad origin"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxDecideBody+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, decideResponse{Error: "reading body"})
		return
	}
	if len(body) > maxDecideBody {
		writeJSON(w, http.StatusRequestEntityTooLarge, decideResponse{Error: "body too large"})
		return
	}

	var req decideRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, decideResponse{Error: "malformed body"})
		return
	}

	bsn, ok := s.authBrowser(w, r, req.Session)
	if !ok {
		// authBrowser already wrote a plain error; but for the JSON API we
		// prefer a JSON body. authBrowser writes text/plain; that's acceptable
		// for a rejected mutation. Return here.
		return
	}
	if !tokensEqual(req.CSRF, bsn.csrfToken) {
		writeJSON(w, http.StatusForbidden, decideResponse{Error: "bad csrf token"})
		return
	}

	kind, err := parseDecisionKind(req.Decision)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, decideResponse{Error: err.Error()})
		return
	}
	if (kind == store.DecisionReject || kind == store.DecisionRequestChanges) && req.Reason == "" {
		writeJSON(w, http.StatusBadRequest, decideResponse{Error: "reason required for reject/request_changes"})
		return
	}

	var approval *policy.Approval
	if kind == store.DecisionApprove {
		plan, err := s.store.LoadCurrentPlan(req.Session)
		if err != nil {
			writeJSON(w, http.StatusConflict, decideResponse{Error: "no current plan to approve"})
			return
		}
		// Default to all actions if the client sent none (checkboxes default
		// checked, but be defensive).
		selected := req.SelectedActionIDs
		if len(selected) == 0 {
			for _, a := range plan.Actions {
				selected = append(selected, a.ID)
			}
		}
		if len(selected) == 0 {
			writeJSON(w, http.StatusBadRequest, decideResponse{Error: "no actions to approve"})
			return
		}
		built, err := s.buildApprovalForSelection(plan, selected)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, decideResponse{Error: "building approval: " + err.Error()})
			return
		}
		approval = built
	}

	if _, err := s.store.RecordDecision(req.Session, req.Revision, kind, req.Reason, approval); err != nil {
		writeJSON(w, decisionErrStatus(err), decideResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, decideResponse{OK: true, Decision: string(kind)})
}

// parseDecisionKind maps the wire decision string to a store.DecisionKind.
func parseDecisionKind(s string) (store.DecisionKind, error) {
	switch store.DecisionKind(s) {
	case store.DecisionApprove:
		return store.DecisionApprove, nil
	case store.DecisionReject:
		return store.DecisionReject, nil
	case store.DecisionRequestChanges:
		return store.DecisionRequestChanges, nil
	default:
		return "", errors.New("unknown decision")
	}
}

// decisionErrStatus maps store errors to HTTP status codes for the browser API.
func decisionErrStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrStaleRevision):
		return http.StatusConflict
	case errors.Is(err, store.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// isJSONContentType reports whether ct is application/json (ignoring params).
func isJSONContentType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && mt == "application/json"
}
