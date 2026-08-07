package broker

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/interactionlabs/devtools/control-room/internal/policy"
	"github.com/interactionlabs/devtools/control-room/internal/protocol"
	"github.com/interactionlabs/devtools/control-room/internal/store"
)

// DefaultApprovalTTL is the lifetime granted to a browser approval when the
// approval is created. It is deliberately short: an approval is a live
// capability, and a stale one should fail closed rather than linger.
const DefaultApprovalTTL = 15 * time.Minute

// dispatch authenticates, version-checks, and routes a single request,
// returning the response envelope. It never panics out; any internal error is
// mapped to a typed error response.
func (b *Broker) dispatch(raw []byte) Response {
	var req Request
	if err := protocol.DecodeStrict(raw, &req); err != nil {
		return errResponse(CodeBadRequest, "malformed request: "+err.Error())
	}
	if req.Version != BrokerProtocolVersion {
		return errResponse(CodeUnsupported,
			fmt.Sprintf("broker protocol v%d required, got v%d", BrokerProtocolVersion, req.Version))
	}
	if !secretsEqual(req.Secret, b.secret) {
		return errResponse(CodeUnauthorized, "invalid control secret")
	}
	if _, ok := validOps[req.Op]; !ok {
		return errResponse(CodeUnknownOp, fmt.Sprintf("unknown op %q", req.Op))
	}

	switch req.Op {
	case OpSessionCreate:
		return b.opSessionCreate(req.Payload)
	case OpSessionGet:
		return b.opSessionGet(req.Payload)
	case OpSessionEnd:
		return b.opSessionEnd(req.Payload)
	case OpPlanPublish:
		return b.opPlanPublish(req.Payload)
	case OpDecisionPoll:
		return b.opDecisionPoll(req.Payload)
	case OpApprovalClaim:
		return b.opApprovalClaim(req.Payload)
	default:
		return errResponse(CodeUnknownOp, fmt.Sprintf("unhandled op %q", req.Op))
	}
}

func (b *Broker) opSessionCreate(payload json.RawMessage) Response {
	var p SessionCreateRequest
	if err := decodePayload(payload, &p); err != nil {
		return errResponse(CodeBadRequest, err.Error())
	}
	id, err := randomSessionID()
	if err != nil {
		return errResponse(CodeInternal, err.Error())
	}
	sess, err := b.store.CreateSession(id, p.WorkspaceID, p.WorkspaceName)
	if err != nil {
		return mapStoreErr(err)
	}
	return okResponse(SessionResult{Session: sess})
}

func (b *Broker) opSessionGet(payload json.RawMessage) Response {
	var p SessionGetRequest
	if err := decodePayload(payload, &p); err != nil {
		return errResponse(CodeBadRequest, err.Error())
	}
	sess, err := b.store.GetSession(p.SessionID)
	if err != nil {
		return mapStoreErr(err)
	}
	return okResponse(SessionResult{Session: sess})
}

func (b *Broker) opSessionEnd(payload json.RawMessage) Response {
	var p SessionEndRequest
	if err := decodePayload(payload, &p); err != nil {
		return errResponse(CodeBadRequest, err.Error())
	}
	sess, err := b.store.EndSession(p.SessionID)
	if err != nil {
		return mapStoreErr(err)
	}
	return okResponse(SessionResult{Session: sess})
}

func (b *Broker) opPlanPublish(payload json.RawMessage) Response {
	var p PlanPublishRequest
	if err := decodePayload(payload, &p); err != nil {
		return errResponse(CodeBadRequest, err.Error())
	}
	plan, err := protocol.ParsePlan(p.Plan)
	if err != nil {
		return errResponse(CodeBadRequest, err.Error())
	}
	sess, err := b.store.PublishPlan(plan)
	if err != nil {
		return mapStoreErr(err)
	}
	return okResponse(PlanPublishResult{Session: sess})
}

func (b *Broker) opDecisionPoll(payload json.RawMessage) Response {
	var p DecisionPollRequest
	if err := decodePayload(payload, &p); err != nil {
		return errResponse(CodeBadRequest, err.Error())
	}
	// Confirm the session exists so a poll on a bad id is not silently
	// "undecided".
	if _, err := b.store.GetSession(p.SessionID); err != nil {
		return mapStoreErr(err)
	}
	d, err := b.store.GetDecision(p.SessionID)
	if errors.Is(err, store.ErrNotFound) {
		return okResponse(DecisionPollResult{Decided: false})
	}
	if err != nil {
		return mapStoreErr(err)
	}
	return okResponse(DecisionPollResult{Decided: true, Decision: d})
}

func (b *Broker) opApprovalClaim(payload json.RawMessage) Response {
	var p ApprovalClaimRequest
	if err := decodePayload(payload, &p); err != nil {
		return errResponse(CodeBadRequest, err.Error())
	}
	res, err := b.store.Claim(p.SessionID, p.Digest)
	if err != nil {
		return mapStoreErr(err)
	}
	return okResponse(ApprovalClaimResult{Approval: res.Approval, ClaimSeq: res.ClaimSeq})
}

// --- response helpers ---

func okResponse(result any) Response {
	raw, err := json.Marshal(result)
	if err != nil {
		return errResponse(CodeInternal, "marshalling result: "+err.Error())
	}
	return Response{OK: true, Result: raw}
}

func errResponse(code, msg string) Response {
	return Response{OK: false, Code: code, Error: msg}
}

func marshalResponse(resp Response) ([]byte, error) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("broker: marshalling response: %w", err)
	}
	return raw, nil
}

// mapStoreErr translates store/policy sentinel errors into stable protocol
// error codes. Anything unrecognized is CodeInternal — fail closed with a
// generic code rather than leaking internals into a client-branchable code.
func mapStoreErr(err error) Response {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return errResponse(CodeNotFound, err.Error())
	case errors.Is(err, store.ErrStaleRevision):
		return errResponse(CodeStale, err.Error())
	case errors.Is(err, store.ErrConflict):
		return errResponse(CodeConflict, err.Error())
	case errors.Is(err, policy.ErrExpired):
		return errResponse(CodeExpired, err.Error())
	case errors.Is(err, policy.ErrSuperseded):
		return errResponse(CodeSuperseded, err.Error())
	case errors.Is(err, policy.ErrClaimsExhausted):
		return errResponse(CodeExhausted, err.Error())
	case errors.Is(err, policy.ErrDigestMismatch):
		return errResponse(CodeMismatch, err.Error())
	default:
		return errResponse(CodeInternal, err.Error())
	}
}

// randomSessionID mints a 256-bit opaque session id.
func randomSessionID() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("broker: generating session id: %w", err)
	}
	return "cr-" + hex.EncodeToString(buf[:]), nil
}
