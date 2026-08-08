package policy

import "fmt"

// State is a session lifecycle state. The set mirrors the RFC's state machine.
type State string

const (
	StateDraft            State = "draft"
	StateAwaitingApproval State = "awaiting_approval"
	StateRejected         State = "rejected"
	StateApproved         State = "approved"
	StateExpired          State = "expired"
	StateRunning          State = "running"
	StateReview           State = "review"
	StateCompleted        State = "completed"
)

// Transition is a named event that may move a session between states.
type Transition string

const (
	TransitionPublish     Transition = "publish"
	TransitionReject      Transition = "reject"
	TransitionRequestEdit Transition = "request_edit"
	TransitionApprove     Transition = "approve"
	TransitionExpire      Transition = "expire"
	TransitionClaim       Transition = "claim"
	TransitionCancel      Transition = "cancel"
	TransitionFail        Transition = "fail"
	TransitionReview      Transition = "review"
	TransitionComplete    Transition = "complete"
	TransitionRevise      Transition = "revise"
	// TransitionRepublish and TransitionEnd name two product/administrative
	// operations that intentionally live OUTSIDE the core Next() transition
	// matrix (see CanRepublish and CanAdministrativelyEnd). They are named here
	// so events and the store refer to them by a stable constant rather than a
	// literal string.
	//
	// TransitionRepublish: an agent publishes a NEW, higher plan revision,
	// superseding whatever the session was reviewing. The core matrix models
	// only the first publish (draft → awaiting_approval) and the explicit
	// request_edit/revise loops; republish is a broker-level operation whose
	// legality is defined by CanRepublish, not by Next().
	//
	// TransitionEnd: the user ends a session administratively. This is a
	// broker-level operation (CanAdministrativelyEnd), deliberately not a matrix
	// edge, so any non-terminal session can be closed without threading an end
	// edge through every state.
	TransitionRepublish Transition = "republish"
	TransitionEnd       Transition = "end"
)

// transitionMatrix is the authoritative transition policy. It maps a current
// state to the transitions valid from it and the resulting state.
//
// This is the single source of truth for what moves are allowed. Any move not
// present here is rejected (fail closed). It mirrors the RFC's diagram:
//
//	draft
//	  └── publish ──▶ awaiting_approval
//	                       ├── reject ───────▶ rejected
//	                       ├── request_edit ─▶ draft
//	                       └── approve ──────▶ approved
//	                                              ├── expire ─▶ expired
//	                                              └── claim ──▶ running
//	                                                               ├── cancel ─▶ completed (terminal-cancel)
//	                                                               ├── fail ───▶ completed (terminal-fail)
//	                                                               └── review ─▶ review
//	                                                                              ├── complete ─▶ completed
//	                                                                              └── revise ───▶ draft
//
// Note on cancel/fail: the RFC diagram lists cancel and fail as outcomes of
// running without drawing an explicit target node. Phase 0 models both as
// moving the session to a terminal completed state; the distinction between a
// cancelled, failed, and cleanly completed run is carried in the event history
// (out of scope for this slice), not in the coarse session state. This keeps
// the state set closed and the terminal handling uniform while preserving the
// audit distinction where it belongs.
var transitionMatrix = map[State]map[Transition]State{
	StateDraft: {
		TransitionPublish: StateAwaitingApproval,
	},
	StateAwaitingApproval: {
		TransitionReject:      StateRejected,
		TransitionRequestEdit: StateDraft,
		TransitionApprove:     StateApproved,
	},
	StateApproved: {
		TransitionExpire: StateExpired,
		TransitionClaim:  StateRunning,
	},
	StateRunning: {
		TransitionCancel: StateCompleted,
		TransitionFail:   StateCompleted,
		TransitionReview: StateReview,
	},
	StateReview: {
		TransitionComplete: StateCompleted,
		TransitionRevise:   StateDraft,
	},
	// Terminal states have no outgoing transitions.
	StateRejected:  {},
	StateExpired:   {},
	StateCompleted: {},
}

// InvalidTransitionError describes a rejected state transition.
type InvalidTransitionError struct {
	From       State
	Transition Transition
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("policy: transition %q not allowed from state %q", e.Transition, e.From)
}

// IsValidState reports whether s is a known session state.
func IsValidState(s State) bool {
	_, ok := transitionMatrix[s]
	return ok
}

// IsTerminal reports whether s has no outgoing transitions.
func IsTerminal(s State) bool {
	moves, ok := transitionMatrix[s]
	return ok && len(moves) == 0
}

// Next returns the state resulting from applying t to from, or an
// *InvalidTransitionError if the transition is not permitted. It fails closed
// on unknown states and unknown transitions.
func Next(from State, t Transition) (State, error) {
	moves, ok := transitionMatrix[from]
	if !ok {
		return "", &InvalidTransitionError{From: from, Transition: t}
	}
	to, ok := moves[t]
	if !ok {
		return "", &InvalidTransitionError{From: from, Transition: t}
	}
	return to, nil
}

// Broker-level operations that sit OUTSIDE the core Next() matrix.
//
// The RFC state machine (encoded in transitionMatrix / Next) describes the
// review→approve→execute lifecycle. Two real operations do not fit cleanly as
// single matrix edges and are modeled here explicitly, so the code's source of
// truth matches what the store actually does rather than pretending the matrix
// governs everything:
//
//   - Republish: an agent publishes a newer revision, superseding the current
//     one. Legal whenever the session is not terminal and not mid-execution
//     (running). Always lands in awaiting_approval. The store additionally
//     enforces monotonic revision numbers and marks prior approvals superseded.
//   - Administrative end: the user closes a session. Legal from any
//     non-terminal state and lands in completed.
//
// Keeping these as named, tested helpers (rather than folding extra edges into
// the matrix) preserves the matrix's clarity and its terminal-state invariants
// while making the divergence explicit.

// RepublishTarget is the state a successful republish lands in.
const RepublishTarget = StateAwaitingApproval

// CanRepublish reports whether a new plan revision may be published from the
// given state. Republish is a broker-level operation OUTSIDE the core matrix,
// so it deliberately permits some states the matrix treats as terminal
// (rejected, expired): an agent may address a rejection or a lapsed approval
// with a fresh, higher revision. It is disallowed only from:
//
//   - completed (the session is truly finished/ended), and
//   - running (an in-flight execution must not be silently superseded),
//
// plus any unknown state (fail closed). A successful republish lands in
// RepublishTarget (awaiting_approval).
func CanRepublish(from State) bool {
	if !IsValidState(from) {
		return false
	}
	switch from {
	case StateCompleted, StateRunning:
		return false
	default:
		return true
	}
}

// CanAdministrativelyEnd reports whether a session in the given state may be
// ended by the user. Any known, non-terminal state may be ended; a terminal
// session is already closed.
func CanAdministrativelyEnd(from State) bool {
	return IsValidState(from) && !IsTerminal(from)
}
