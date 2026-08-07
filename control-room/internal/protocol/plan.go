package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// ActionKind enumerates the kinds of proposed action a plan may contain.
//
// Unknown kinds are rejected (fail closed). New kinds are an explicit, reviewed
// addition here plus the schema.
type ActionKind string

const (
	ActionWritePatch ActionKind = "write_patch"
	ActionRunCommand ActionKind = "run_command"
)

// validActionKinds is the closed set of accepted action kinds.
var validActionKinds = map[ActionKind]struct{}{
	ActionWritePatch: {},
	ActionRunCommand: {},
}

// RiskClass enumerates the permission/risk envelope of an action. It maps to
// the RFC's initial permission classes. Unknown values are rejected.
type RiskClass string

const (
	RiskWorkspaceRead  RiskClass = "workspace_read"
	RiskWorkspaceWrite RiskClass = "workspace_write"
	RiskLocalProcess   RiskClass = "local_process"
	RiskNetwork        RiskClass = "network"
)

// validRiskClasses is the closed set of accepted risk classes.
var validRiskClasses = map[RiskClass]struct{}{
	RiskWorkspaceRead:  {},
	RiskWorkspaceWrite: {},
	RiskLocalProcess:   {},
	RiskNetwork:        {},
}

// BlockKind enumerates the kinds of plan content block. Unknown values are
// rejected.
type BlockKind string

const (
	BlockMarkdown BlockKind = "markdown"
	BlockDiff     BlockKind = "diff"
	BlockQuestion BlockKind = "question"
)

// validBlockKinds is the closed set of accepted block kinds.
var validBlockKinds = map[BlockKind]struct{}{
	BlockMarkdown: {},
	BlockDiff:     {},
	BlockQuestion: {},
}

// Workspace is the broker-issued identity of a registered workspace root.
//
// The browser never nominates filesystem paths; it sees only the opaque id and
// a human-facing display name. The id is approval-relevant; the display name is
// display-only (see Plan doc comment).
type Workspace struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// Block is a single unit of reviewable plan content with a stable semantic ID.
//
// The entire block is approval-relevant. Annotations target Block.ID rather
// than rendered DOM structure.
type Block struct {
	ID      string    `json:"id"`
	Kind    BlockKind `json:"kind"`
	Content string    `json:"content"`
}

// Action is a proposed unit of work the agent may execute after approval.
//
// Every field on an Action is approval-relevant: changing a title, target,
// program, argument, or risk class produces a different approval digest and
// invalidates any prior approval. Program/Args/Cwd are meaningful only for
// run_command actions and must be empty otherwise.
type Action struct {
	ID      string     `json:"id"`
	Kind    ActionKind `json:"kind"`
	Title   string     `json:"title"`
	Targets []string   `json:"targets,omitempty"`
	Program string     `json:"program,omitempty"`
	Args    []string   `json:"args,omitempty"`
	Cwd     string     `json:"cwd,omitempty"`
	Risk    RiskClass  `json:"risk"`
}

// Plan is a single published plan revision.
//
// Approval-relevance policy (the safer default wins): every field of a Plan is
// treated as approval-relevant and is bound into the approval digest EXCEPT
// fields explicitly documented as display-only. The only display-only fields in
// Phase 0 are:
//
//   - Workspace.DisplayName (human label; identity is Workspace.ID)
//
// Everything else — protocol version, session id, revision, goal, summary,
// workspace id, every block, every action and all of its arguments — changes
// the digest when changed.
type Plan struct {
	ProtocolVersion int       `json:"protocol_version"`
	SessionID       string    `json:"session_id"`
	Revision        int       `json:"revision"`
	Goal            string    `json:"goal"`
	Summary         string    `json:"summary"`
	Workspace       Workspace `json:"workspace"`
	Blocks          []Block   `json:"blocks"`
	Actions         []Action  `json:"actions"`
}

// ValidationError is a typed error describing why a message was rejected. It
// names the offending field so callers can fail closed with an actionable
// diagnostic rather than a generic parse failure.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("protocol: invalid %s: %s", e.Field, e.Reason)
}

func newValidationError(field, reason string) error {
	return &ValidationError{Field: field, Reason: reason}
}

// validateID checks an opaque identifier's shape and bounded length. It makes
// no claim about entropy: a caller-supplied id is validated for form only.
//
// Accepted characters are a conservative, URL- and log-safe subset:
// ASCII letters, digits, and the separators '-', '_', '.', ':', '/'. This
// keeps ids embeddable in event payloads and opaque references without escaping
// while still allowing scheme-like forms such as "workspace://root".
func validateID(field, id string) error {
	if len(id) < MinIDBytes {
		return newValidationError(field, "must not be empty")
	}
	if len(id) > MaxIDBytes {
		return newValidationError(field, fmt.Sprintf("exceeds %d bytes", MaxIDBytes))
	}
	if !utf8.ValidString(id) {
		return newValidationError(field, "is not valid UTF-8")
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		ok := (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == ':' || c == '/'
		if !ok {
			return newValidationError(field, fmt.Sprintf("contains disallowed byte 0x%02x", c))
		}
	}
	return nil
}

// validateBounded checks that a UTF-8 string is non-empty (when required) and
// within a byte limit.
func validateBounded(field, s string, maxBytes int, required bool) error {
	if required && len(s) == 0 {
		return newValidationError(field, "must not be empty")
	}
	if len(s) > maxBytes {
		return newValidationError(field, fmt.Sprintf("exceeds %d bytes", maxBytes))
	}
	if !utf8.ValidString(s) {
		return newValidationError(field, "is not valid UTF-8")
	}
	return nil
}

// Validate checks a Workspace's fields against protocol rules.
func (w *Workspace) Validate() error {
	if err := validateID("workspace.id", w.ID); err != nil {
		return err
	}
	// display_name is display-only; it must still be bounded and valid UTF-8,
	// but may be empty.
	if err := validateBounded("workspace.display_name", w.DisplayName, MaxDisplayNameBytes, false); err != nil {
		return err
	}
	return nil
}

// Validate checks a Block against protocol rules.
func (b *Block) Validate() error {
	if err := validateID("block.id", b.ID); err != nil {
		return err
	}
	if _, ok := validBlockKinds[b.Kind]; !ok {
		return newValidationError("block.kind", fmt.Sprintf("unknown kind %q", b.Kind))
	}
	if err := validateBounded("block.content", b.Content, MaxBlockContentBytes, false); err != nil {
		return err
	}
	return nil
}

// Validate checks an Action against protocol rules, including
// kind-conditional field constraints.
func (a *Action) Validate() error {
	if err := validateID("action.id", a.ID); err != nil {
		return err
	}
	if _, ok := validActionKinds[a.Kind]; !ok {
		return newValidationError("action.kind", fmt.Sprintf("unknown kind %q", a.Kind))
	}
	if err := validateBounded("action.title", a.Title, MaxActionTitleBytes, true); err != nil {
		return err
	}
	if _, ok := validRiskClasses[a.Risk]; !ok {
		return newValidationError("action.risk", fmt.Sprintf("unknown risk class %q", a.Risk))
	}
	if len(a.Targets) > MaxTargetsPerAction {
		return newValidationError("action.targets", fmt.Sprintf("exceeds %d entries", MaxTargetsPerAction))
	}
	for i, t := range a.Targets {
		if err := validateBounded(fmt.Sprintf("action.targets[%d]", i), t, MaxTargetBytes, true); err != nil {
			return err
		}
	}

	switch a.Kind {
	case ActionRunCommand:
		if err := validateBounded("action.program", a.Program, MaxProgramBytes, true); err != nil {
			return err
		}
		if len(a.Args) > MaxArgsPerAction {
			return newValidationError("action.args", fmt.Sprintf("exceeds %d entries", MaxArgsPerAction))
		}
		for i, arg := range a.Args {
			if err := validateBounded(fmt.Sprintf("action.args[%d]", i), arg, MaxArgBytes, false); err != nil {
				return err
			}
		}
		if err := validateBounded("action.cwd", a.Cwd, MaxCwdBytes, false); err != nil {
			return err
		}
	case ActionWritePatch:
		// Command-only fields must be empty for non-command actions so they
		// cannot smuggle unreviewed execution parameters.
		if a.Program != "" {
			return newValidationError("action.program", "must be empty for write_patch actions")
		}
		if len(a.Args) != 0 {
			return newValidationError("action.args", "must be empty for write_patch actions")
		}
		if a.Cwd != "" {
			return newValidationError("action.cwd", "must be empty for write_patch actions")
		}
	}
	return nil
}

// Validate checks an entire Plan revision against protocol rules. It fails
// closed on unknown protocol versions, malformed identifiers, unknown
// action/risk/block kinds, oversized collections, and duplicate block or action
// IDs.
//
// Uniqueness of action IDs is enforced here because approval selection and
// digest binding both key on action IDs; duplicates would make selection
// ambiguous.
func (p *Plan) Validate() error {
	if p.ProtocolVersion != ProtocolVersion {
		return newValidationError("protocol_version",
			fmt.Sprintf("unsupported version %d (this build speaks %d)", p.ProtocolVersion, ProtocolVersion))
	}
	if err := validateID("session_id", p.SessionID); err != nil {
		return err
	}
	if p.Revision < 1 {
		return newValidationError("revision", "must be >= 1")
	}
	if err := validateBounded("goal", p.Goal, MaxGoalBytes, true); err != nil {
		return err
	}
	if err := validateBounded("summary", p.Summary, MaxSummaryBytes, false); err != nil {
		return err
	}
	if err := p.Workspace.Validate(); err != nil {
		return err
	}

	if len(p.Blocks) > MaxBlocksPerRevision {
		return newValidationError("blocks", fmt.Sprintf("exceeds %d entries", MaxBlocksPerRevision))
	}
	seenBlocks := make(map[string]struct{}, len(p.Blocks))
	for i := range p.Blocks {
		if err := p.Blocks[i].Validate(); err != nil {
			return err
		}
		if _, dup := seenBlocks[p.Blocks[i].ID]; dup {
			return newValidationError("blocks", fmt.Sprintf("duplicate block id %q", p.Blocks[i].ID))
		}
		seenBlocks[p.Blocks[i].ID] = struct{}{}
	}

	if len(p.Actions) > MaxActionsPerRevision {
		return newValidationError("actions", fmt.Sprintf("exceeds %d entries", MaxActionsPerRevision))
	}
	seenActions := make(map[string]struct{}, len(p.Actions))
	for i := range p.Actions {
		if err := p.Actions[i].Validate(); err != nil {
			return err
		}
		if _, dup := seenActions[p.Actions[i].ID]; dup {
			return newValidationError("actions", fmt.Sprintf("duplicate action id %q", p.Actions[i].ID))
		}
		seenActions[p.Actions[i].ID] = struct{}{}
	}
	return nil
}

// HasAction reports whether the plan contains an action with the given ID.
func (p *Plan) HasAction(id string) bool {
	for i := range p.Actions {
		if p.Actions[i].ID == id {
			return true
		}
	}
	return false
}

// ParsePlan decodes a Plan from JSON, rejecting unknown fields, trailing data,
// and multiple JSON values, then validates it. It is the single entry point for
// turning bytes into a trusted Plan.
//
// Rejecting unknown fields is a fail-closed choice: a field the agent thinks is
// meaningful but the broker does not understand must not silently pass through
// into an approval digest.
func ParsePlan(data []byte) (*Plan, error) {
	if len(data) > MaxPlanBytes {
		return nil, newValidationError("plan", fmt.Sprintf("encoded plan exceeds %d bytes", MaxPlanBytes))
	}
	var p Plan
	if err := decodeSingleStrictJSON(data, &p); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// decodeSingleStrictJSON decodes exactly one JSON value into v, rejecting
// unknown object fields and any trailing (or additional) JSON value.
func decodeSingleStrictJSON(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &ValidationError{Field: "json", Reason: err.Error()}
	}
	// Reject any trailing/second value. A well-formed message is exactly one
	// JSON value.
	if dec.More() {
		return newValidationError("json", "unexpected trailing data after JSON value")
	}
	return nil
}

// DecodeStrict decodes exactly one JSON value into v with the same fail-closed
// discipline the plan parser uses: unknown object fields and any trailing or
// additional JSON value are rejected. It is exported so other packages (e.g.
// the broker control protocol) can decode their own request/response payloads
// with identical strictness rather than a lenient json.Unmarshal.
func DecodeStrict(data []byte, v any) error {
	return decodeSingleStrictJSON(data, v)
}
