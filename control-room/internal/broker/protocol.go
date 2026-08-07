// Package broker is Control Room's long-lived local process: it owns the
// SQLite store and serves a private Unix-domain-socket control channel to CLI
// and agent clients.
//
// The broker never executes anything. Its operations are a small, strictly
// versioned request/response set covering exactly the usable journey: session
// create/get/end, plan publish, decision poll, and approval claim. Every request
// is length-prefixed JSON (using internal/protocol's frame codec), authenticated
// by a random persisted control secret, and bounded.
package broker

import (
	"encoding/json"
	"fmt"

	"github.com/interactionlabs/devtools/control-room/internal/protocol"
	"github.com/interactionlabs/devtools/control-room/internal/store"
)

// BrokerProtocolVersion versions the broker control protocol independently of
// the agent plan protocol (protocol.ProtocolVersion). A client speaking a
// different version is rejected.
const BrokerProtocolVersion = 1

// Op names the requested operation. Unknown ops are rejected (fail closed).
type Op string

const (
	OpSessionCreate Op = "session.create"
	OpSessionGet    Op = "session.get"
	OpSessionEnd    Op = "session.end"
	OpPlanPublish   Op = "plan.publish"
	OpDecisionPoll  Op = "decision.poll"
	OpApprovalClaim Op = "approval.claim"
	OpSessionOpen   Op = "session.open"
)

var validOps = map[Op]struct{}{
	OpSessionCreate: {}, OpSessionGet: {}, OpSessionEnd: {},
	OpPlanPublish: {}, OpDecisionPoll: {}, OpApprovalClaim: {},
	OpSessionOpen: {},
}

// Request is the envelope for every control-channel call. Secret authenticates
// the caller; Version pins the protocol; Op selects the operation; Payload is
// the operation-specific body (validated per-op).
type Request struct {
	Version int             `json:"version"`
	Secret  string          `json:"secret"`
	Op      Op              `json:"op"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Response is the envelope for every reply. Exactly one of Result/Error is set.
// Code is a stable machine-readable error code an adapter can branch on.
type Response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Code   string          `json:"code,omitempty"`
}

// Error codes. These are stable and machine-consumable.
const (
	CodeBadRequest   = "bad_request"
	CodeUnauthorized = "unauthorized"
	CodeUnsupported  = "unsupported_version"
	CodeUnknownOp    = "unknown_op"
	CodeNotFound     = "not_found"
	CodeConflict     = "conflict"
	CodeStale        = "stale_revision"
	CodeExpired      = "expired"
	CodeSuperseded   = "superseded"
	CodeExhausted    = "claims_exhausted"
	CodeMismatch     = "digest_mismatch"
	CodeInternal     = "internal"
)

// --- per-operation payloads ---

// SessionCreateRequest asks the broker to mint a new session. The broker
// generates the high-entropy session id; the caller supplies workspace
// identity.
type SessionCreateRequest struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceName string `json:"workspace_name,omitempty"`
}

// SessionResult is returned by session.create / session.get / session.end.
type SessionResult struct {
	Session *store.Session `json:"session"`
}

// SessionGetRequest / SessionEndRequest reference a session by id.
type SessionGetRequest struct {
	SessionID string `json:"session_id"`
}
type SessionEndRequest struct {
	SessionID string `json:"session_id"`
}

// PlanPublishRequest carries the raw plan JSON. The broker parses and validates
// it with protocol.ParsePlan; the session_id and workspace inside must match an
// existing session.
type PlanPublishRequest struct {
	Plan json.RawMessage `json:"plan"`
}

// PlanPublishResult reports the resulting session projection.
type PlanPublishResult struct {
	Session *store.Session `json:"session"`
}

// DecisionPollRequest references the session whose current-revision decision is
// wanted.
type DecisionPollRequest struct {
	SessionID string `json:"session_id"`
}

// DecisionPollResult reports whether a decision exists yet and, if so, the
// durable decision. Decided is false when the current revision is still
// awaiting a user decision.
type DecisionPollResult struct {
	Decided  bool            `json:"decided"`
	Decision *store.Decision `json:"decision,omitempty"`
}

// ApprovalClaimRequest atomically claims an approval by (session, digest).
type ApprovalClaimRequest struct {
	SessionID string `json:"session_id"`
	Digest    string `json:"digest"`
}

// ApprovalClaimResult reports the claimed approval and claim sequence.
type ApprovalClaimResult struct {
	Approval interface{} `json:"approval"`
	ClaimSeq int         `json:"claim_seq"`
}

// SessionOpenRequest asks the broker to mint a one-time browser review URL for
// a session.
type SessionOpenRequest struct {
	SessionID string `json:"session_id"`
}

// SessionOpenResult carries the loopback review URL a browser should open. The
// URL embeds a single-use, expiring bootstrap capability.
type SessionOpenResult struct {
	URL string `json:"url"`
}

// decodePayload strictly decodes a request payload into v, rejecting unknown
// fields so an adapter typo fails closed instead of being silently ignored.
func decodePayload(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return fmt.Errorf("missing payload")
	}
	return protocol.DecodeStrict(raw, v)
}
