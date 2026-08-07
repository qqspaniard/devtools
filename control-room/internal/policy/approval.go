// Package policy implements Control Room's approval and session-transition
// domain logic: the session state machine and its transition matrix,
// deterministic approval canonicalization and SHA-256 digest generation, and a
// concurrency-safe approval-claim authority.
//
// This package is pure policy. It contains no persistence, no sockets, and no
// HTTP. The claim authority is an in-memory, concurrency-safe implementation
// that makes single-use / max-claims semantics executable and testable in
// Phase 0; it is explicitly NOT durable (see ClaimAuthority doc comment).
package policy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/gowebpki/jcs"

	"github.com/interactionlabs/devtools/control-room/internal/protocol"
)

// DigestPrefix labels the hex digest so it is self-describing in events and
// wire messages ("sha256:...").
const DigestPrefix = "sha256:"

// canonicalAction is the exact, canonical projection of a protocol.Action that
// is bound into an approval digest. It contains every approval-relevant field
// of an action.
//
// Field order in the struct is irrelevant: the digest is computed over the
// RFC 8785 (JCS) canonical form, which sorts object keys deterministically.
// Slices (targets, args) preserve order because order is semantically
// significant for command arguments and is part of what the user reviewed.
type canonicalAction struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Title   string   `json:"title"`
	Targets []string `json:"targets"`
	Program string   `json:"program"`
	Args    []string `json:"args"`
	Cwd     string   `json:"cwd"`
	Risk    string   `json:"risk"`
}

// canonicalWorkspace binds only the workspace identity into the digest. The
// display name is display-only and is deliberately excluded (see protocol.Plan
// doc comment) so relabeling a workspace does not invalidate an approval.
type canonicalWorkspace struct {
	ID string `json:"id"`
}

// canonicalPlan is the approval-relevant projection of a plan revision. It
// mirrors protocol.Plan minus the display-only fields. Blocks are included in
// full because the RFC's safer-default policy treats the entire published
// revision as approval-relevant unless a field is explicitly display-only.
type canonicalPlan struct {
	ProtocolVersion int                `json:"protocol_version"`
	SessionID       string             `json:"session_id"`
	Revision        int                `json:"revision"`
	Goal            string             `json:"goal"`
	Summary         string             `json:"summary"`
	Workspace       canonicalWorkspace `json:"workspace"`
	Blocks          []canonicalBlock   `json:"blocks"`
	Actions         []canonicalAction  `json:"actions"`
}

type canonicalBlock struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

// digestInput is the top-level structure hashed to produce an approval digest.
// It binds, in one canonical document:
//
//   - protocol version
//   - session ID
//   - the exact canonical plan revision
//   - the canonical selected actions (the subset being approved)
//   - workspace identity
//   - the permission envelope (the set of risk classes across selected actions)
//   - expiration
//   - maximum claims
//
// Changing any bound field changes the digest and invalidates the approval.
type digestInput struct {
	ProtocolVersion    int                `json:"protocol_version"`
	SessionID          string             `json:"session_id"`
	Plan               canonicalPlan      `json:"plan"`
	SelectedActions    []canonicalAction  `json:"selected_actions"`
	Workspace          canonicalWorkspace `json:"workspace"`
	PermissionEnvelope []string           `json:"permission_envelope"`
	// ExpiresAt is bound as a canonical UTC RFC3339Nano string, NOT a numeric
	// timestamp. RFC 8785 (JCS) serializes JSON numbers through IEEE-754
	// float64, so any integer above 2^53 loses precision during
	// canonicalization. Present-day nanosecond timestamps far exceed 2^53, so a
	// numeric nanosecond field would silently fail to bind the exact
	// expiration. Encoding it as a string preserves every digit losslessly.
	ExpiresAt string `json:"expires_at"`
	// MaxClaims and the plan's protocol_version/revision are bounded well below
	// 2^53 (see limits.go: MaxClaimsCeiling, MaxActionsPerRevision, etc.), so
	// they are safe as JSON numbers under the RFC 8785 float64 constraint. Any
	// future field whose value can exceed 2^53 MUST be encoded as a string
	// here, not as a JSON integer.
	MaxClaims int `json:"max_claims"`
}

func toCanonicalAction(a protocol.Action) canonicalAction {
	// Normalize nil slices to empty slices so the canonical form is stable
	// regardless of whether the decoder produced nil or []. JCS then emits [].
	targets := a.Targets
	if targets == nil {
		targets = []string{}
	}
	args := a.Args
	if args == nil {
		args = []string{}
	}
	return canonicalAction{
		ID:      a.ID,
		Kind:    string(a.Kind),
		Title:   a.Title,
		Targets: targets,
		Program: a.Program,
		Args:    args,
		Cwd:     a.Cwd,
		Risk:    string(a.Risk),
	}
}

func toCanonicalPlan(p *protocol.Plan) canonicalPlan {
	blocks := make([]canonicalBlock, len(p.Blocks))
	for i, b := range p.Blocks {
		blocks[i] = canonicalBlock{ID: b.ID, Kind: string(b.Kind), Content: b.Content}
	}
	actions := make([]canonicalAction, len(p.Actions))
	for i := range p.Actions {
		actions[i] = toCanonicalAction(p.Actions[i])
	}
	return canonicalPlan{
		ProtocolVersion: p.ProtocolVersion,
		SessionID:       p.SessionID,
		Revision:        p.Revision,
		Goal:            p.Goal,
		Summary:         p.Summary,
		Workspace:       canonicalWorkspace{ID: p.Workspace.ID},
		Blocks:          blocks,
		Actions:         actions,
	}
}

// ApprovalRequest is the broker-side description of an approval to be created.
// The broker constructs the digest; the agent later references and claims it.
type ApprovalRequest struct {
	Plan            *protocol.Plan
	SelectedActions []string
	ExpiresAt       time.Time
	MaxClaims       int
}

// Approval is the durable capability produced by the broker. The digest binds
// it to an exact plan revision and selection; the agent references Digest when
// claiming.
type Approval struct {
	SessionID      string    `json:"session_id"`
	PlanRevision   int       `json:"plan_revision"`
	Digest         string    `json:"digest"`
	AllowedActions []string  `json:"allowed_action_ids"`
	ExpiresAt      time.Time `json:"expires_at"`
	MaxClaims      int       `json:"max_claims"`
}

// BuildApproval validates the request against the plan and produces an Approval
// with a deterministic digest.
//
// It fails closed on: an invalid plan, an empty or oversized selection, a
// selected action ID that is not present in the plan, duplicate selected IDs,
// a non-positive or oversized max_claims, and a zero expiration. The broker is
// the sole creator of approvals.
func BuildApproval(req ApprovalRequest) (*Approval, error) {
	if req.Plan == nil {
		return nil, fmt.Errorf("policy: nil plan")
	}
	if err := req.Plan.Validate(); err != nil {
		return nil, fmt.Errorf("policy: plan invalid: %w", err)
	}
	if len(req.SelectedActions) == 0 {
		return nil, fmt.Errorf("policy: approval must select at least one action")
	}
	if len(req.SelectedActions) > protocol.MaxSelectedActions {
		return nil, fmt.Errorf("policy: selection exceeds %d actions", protocol.MaxSelectedActions)
	}
	if req.MaxClaims < 1 {
		return nil, fmt.Errorf("policy: max_claims must be >= 1")
	}
	if req.MaxClaims > protocol.MaxClaimsCeiling {
		return nil, fmt.Errorf("policy: max_claims exceeds ceiling %d", protocol.MaxClaimsCeiling)
	}
	if req.ExpiresAt.IsZero() {
		return nil, fmt.Errorf("policy: approval must have an expiration")
	}

	// Resolve selected IDs to their canonical action definitions, rejecting
	// duplicates and unknown IDs.
	seen := make(map[string]struct{}, len(req.SelectedActions))
	selected := make([]canonicalAction, 0, len(req.SelectedActions))
	envelopeSet := make(map[string]struct{})
	for _, id := range req.SelectedActions {
		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("policy: duplicate selected action id %q", id)
		}
		seen[id] = struct{}{}
		var found *protocol.Action
		for i := range req.Plan.Actions {
			if req.Plan.Actions[i].ID == id {
				found = &req.Plan.Actions[i]
				break
			}
		}
		if found == nil {
			return nil, fmt.Errorf("policy: selected action id %q not present in plan", id)
		}
		selected = append(selected, toCanonicalAction(*found))
		envelopeSet[string(found.Risk)] = struct{}{}
	}

	// The permission envelope is the deterministic, sorted set of risk classes
	// spanned by the selected actions.
	envelope := make([]string, 0, len(envelopeSet))
	for r := range envelopeSet {
		envelope = append(envelope, r)
	}
	sort.Strings(envelope)

	// allowedActions preserves the caller's selection order for the
	// human-facing Approval, but the digest binds the selection via the
	// canonical action definitions (whose order follows the selection), so the
	// bound identity is unambiguous.
	allowed := make([]string, len(req.SelectedActions))
	copy(allowed, req.SelectedActions)

	input := digestInput{
		ProtocolVersion:    req.Plan.ProtocolVersion,
		SessionID:          req.Plan.SessionID,
		Plan:               toCanonicalPlan(req.Plan),
		SelectedActions:    selected,
		Workspace:          canonicalWorkspace{ID: req.Plan.Workspace.ID},
		PermissionEnvelope: envelope,
		ExpiresAt:          req.ExpiresAt.UTC().Format(time.RFC3339Nano),
		MaxClaims:          req.MaxClaims,
	}

	digest, err := computeDigest(input)
	if err != nil {
		return nil, err
	}

	return &Approval{
		SessionID:      req.Plan.SessionID,
		PlanRevision:   req.Plan.Revision,
		Digest:         digest,
		AllowedActions: allowed,
		ExpiresAt:      req.ExpiresAt.UTC(),
		MaxClaims:      req.MaxClaims,
	}, nil
}

// computeDigest serializes the input to JSON, canonicalizes it per RFC 8785
// (JCS), and returns the prefixed SHA-256 hex digest.
//
// Canonicalization is delegated to github.com/gowebpki/jcs (a maintained fork
// of the RFC 8785 reference implementation) rather than hand-rolled, so number
// and string canonicalization exactly follow the spec.
func computeDigest(input digestInput) (string, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("policy: marshalling digest input: %w", err)
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		return "", fmt.Errorf("policy: canonicalizing digest input: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return DigestPrefix + fmt.Sprintf("%x", sum), nil
}
