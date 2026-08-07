package policy

import (
	"errors"
	"testing"
)

func TestStateMachineHappyPath(t *testing.T) {
	// draft -> awaiting_approval -> approved -> running -> review -> completed
	steps := []struct {
		from State
		t    Transition
		want State
	}{
		{StateDraft, TransitionPublish, StateAwaitingApproval},
		{StateAwaitingApproval, TransitionApprove, StateApproved},
		{StateApproved, TransitionClaim, StateRunning},
		{StateRunning, TransitionReview, StateReview},
		{StateReview, TransitionComplete, StateCompleted},
	}
	for _, s := range steps {
		got, err := Next(s.from, s.t)
		if err != nil {
			t.Fatalf("Next(%s,%s): unexpected error %v", s.from, s.t, err)
		}
		if got != s.want {
			t.Fatalf("Next(%s,%s)=%s want %s", s.from, s.t, got, s.want)
		}
	}
}

func TestStateMachineBranches(t *testing.T) {
	cases := []struct {
		from State
		t    Transition
		want State
	}{
		{StateAwaitingApproval, TransitionReject, StateRejected},
		{StateAwaitingApproval, TransitionRequestEdit, StateDraft},
		{StateApproved, TransitionExpire, StateExpired},
		{StateRunning, TransitionCancel, StateCompleted},
		{StateRunning, TransitionFail, StateCompleted},
		{StateReview, TransitionRevise, StateDraft},
	}
	for _, c := range cases {
		got, err := Next(c.from, c.t)
		if err != nil {
			t.Fatalf("Next(%s,%s): %v", c.from, c.t, err)
		}
		if got != c.want {
			t.Fatalf("Next(%s,%s)=%s want %s", c.from, c.t, got, c.want)
		}
	}
}

func TestStateMachineRejectsInvalidTransitions(t *testing.T) {
	cases := []struct {
		from State
		t    Transition
	}{
		{StateDraft, TransitionApprove},          // can't approve a draft
		{StateDraft, TransitionClaim},            // can't claim a draft
		{StateAwaitingApproval, TransitionClaim}, // must be approved first
		{StateApproved, TransitionReview},        // must claim (run) before review
		{StateRejected, TransitionPublish},       // terminal
		{StateExpired, TransitionClaim},          // terminal
		{StateCompleted, TransitionRevise},       // terminal
		{StateRunning, TransitionApprove},        // nonsensical
	}
	for _, c := range cases {
		_, err := Next(c.from, c.t)
		if err == nil {
			t.Fatalf("expected rejection of %s from %s", c.t, c.from)
		}
		var ite *InvalidTransitionError
		if !errors.As(err, &ite) {
			t.Fatalf("expected *InvalidTransitionError, got %T", err)
		}
	}
}

func TestStateMachineRejectsUnknownState(t *testing.T) {
	_, err := Next(State("bogus"), TransitionPublish)
	if err == nil {
		t.Fatal("expected rejection of unknown state")
	}
}

func TestTerminalStates(t *testing.T) {
	for _, s := range []State{StateRejected, StateExpired, StateCompleted} {
		if !IsTerminal(s) {
			t.Fatalf("%s should be terminal", s)
		}
	}
	for _, s := range []State{StateDraft, StateAwaitingApproval, StateApproved, StateRunning, StateReview} {
		if IsTerminal(s) {
			t.Fatalf("%s should not be terminal", s)
		}
	}
}

func TestIsValidState(t *testing.T) {
	if !IsValidState(StateDraft) {
		t.Fatal("draft should be valid")
	}
	if IsValidState(State("nope")) {
		t.Fatal("unknown state should be invalid")
	}
}

func TestCanRepublish(t *testing.T) {
	// Republish is a broker-level op OUTSIDE the core matrix: it permits some
	// matrix-terminal states (rejected, expired) but never running/completed.
	allow := []State{StateDraft, StateAwaitingApproval, StateApproved, StateReview, StateRejected, StateExpired}
	for _, s := range allow {
		if !CanRepublish(s) {
			t.Fatalf("CanRepublish(%s) = false, want true", s)
		}
	}
	deny := []State{StateRunning, StateCompleted, State("bogus")}
	for _, s := range deny {
		if CanRepublish(s) {
			t.Fatalf("CanRepublish(%s) = true, want false", s)
		}
	}
	if RepublishTarget != StateAwaitingApproval {
		t.Fatalf("RepublishTarget = %s, want awaiting_approval", RepublishTarget)
	}
}

func TestCanAdministrativelyEnd(t *testing.T) {
	// Any known, non-terminal state may be ended; terminal/unknown may not.
	end := []State{StateDraft, StateAwaitingApproval, StateApproved, StateRunning, StateReview}
	for _, s := range end {
		if !CanAdministrativelyEnd(s) {
			t.Fatalf("CanAdministrativelyEnd(%s) = false, want true", s)
		}
	}
	noEnd := []State{StateRejected, StateExpired, StateCompleted, State("bogus")}
	for _, s := range noEnd {
		if CanAdministrativelyEnd(s) {
			t.Fatalf("CanAdministrativelyEnd(%s) = true, want false", s)
		}
	}
}

func TestRepublishAndEndAreNotMatrixEdges(t *testing.T) {
	// The republish/end operations live outside Next(); Next must NOT accept
	// them as transitions (they are broker-level operations, not matrix edges).
	if _, err := Next(StateRejected, TransitionRepublish); err == nil {
		t.Fatal("Next must not treat republish as a matrix edge")
	}
	if _, err := Next(StateAwaitingApproval, TransitionEnd); err == nil {
		t.Fatal("Next must not treat end as a matrix edge")
	}
}
