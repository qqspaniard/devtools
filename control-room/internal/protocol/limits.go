// Package protocol defines the versioned wire types, explicit resource limits,
// and validation rules for the Control Room agent protocol.
//
// Everything in this package is transport-agnostic: it describes what a valid
// message looks like and how large it may be, independent of how bytes arrive.
// The Phase 0 slice implements the type surface, strict validation, and the
// length-prefixed frame codec; it deliberately contains no sockets, HTTP,
// SQLite, or broker lifecycle.
package protocol

// ProtocolVersion is the only protocol version this build understands.
//
// The broker and agent fail closed on any other value (see Plan.Validate).
// Bumping the wire protocol is an explicit, reviewed change to this constant
// plus the accompanying schema in schema/v1.
const ProtocolVersion = 1

// Resource limits.
//
// These bounds are intentionally explicit and conservative. They exist to make
// oversized or adversarial input fail with a typed error before any large
// allocation, and to keep approval-relevant data from being silently
// truncated. Display-only progress output has separate, looser handling and is
// not modeled in Phase 0.
//
// All byte limits are counted against the raw UTF-8 encoding of the field.
const (
	// MaxFrameBytes is the largest single protocol frame the codec will read.
	// The length prefix is checked against this before any buffer is
	// allocated, so a hostile length header cannot force a large allocation.
	MaxFrameBytes = 1 << 20 // 1 MiB

	// MaxPlanBytes bounds the canonical JSON encoding of an entire plan
	// revision. It is deliberately smaller than MaxFrameBytes to leave room
	// for envelope overhead.
	MaxPlanBytes = 512 * 1024 // 512 KiB

	// MaxIDBytes bounds any opaque identifier (session, workspace, block,
	// action). IDs are opaque strings; the broker validates their shape and
	// length but makes no claim about their entropy when they originate from a
	// caller.
	MaxIDBytes = 256

	// MinIDBytes rejects empty identifiers.
	MinIDBytes = 1

	// MaxGoalBytes bounds the plan goal string.
	MaxGoalBytes = 4 * 1024

	// MaxSummaryBytes bounds the plan summary string.
	MaxSummaryBytes = 16 * 1024

	// MaxDisplayNameBytes bounds a workspace display name.
	MaxDisplayNameBytes = 256

	// MaxBlocksPerRevision bounds the number of plan blocks in one revision.
	MaxBlocksPerRevision = 512

	// MaxBlockContentBytes bounds a single block's content.
	MaxBlockContentBytes = 128 * 1024

	// MaxActionsPerRevision bounds the number of proposed actions in one
	// revision. Approval selects a subset of these, so this also bounds the
	// selection set.
	MaxActionsPerRevision = 128

	// MaxActionTitleBytes bounds an action title.
	MaxActionTitleBytes = 1 * 1024

	// MaxTargetsPerAction bounds the number of opaque workspace targets a
	// single action may reference.
	MaxTargetsPerAction = 256

	// MaxTargetBytes bounds a single target reference string.
	MaxTargetBytes = 2 * 1024

	// MaxProgramBytes bounds the program name of a run_command action.
	MaxProgramBytes = 1 * 1024

	// MaxArgsPerAction bounds the number of command arguments.
	MaxArgsPerAction = 256

	// MaxArgBytes bounds a single command argument.
	MaxArgBytes = 4 * 1024

	// MaxCwdBytes bounds the opaque working-directory reference.
	MaxCwdBytes = 2 * 1024

	// MaxSelectedActions bounds the number of action IDs an approval may cover.
	// It mirrors MaxActionsPerRevision because an approval can, at most, cover
	// every action in the revision.
	MaxSelectedActions = MaxActionsPerRevision

	// MaxClaimsCeiling bounds the max_claims value an approval may specify.
	// Approvals are single-use by default (max_claims == 1); a higher value is
	// permitted up to this ceiling but must be set explicitly.
	MaxClaimsCeiling = 1024
)
