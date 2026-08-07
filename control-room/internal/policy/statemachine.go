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
