package broker

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/interactionlabs/devtools/control-room/internal/protocol"
	"github.com/interactionlabs/devtools/control-room/internal/store"
)

// Client is a thin control-channel client used by the CLI (and tests). It
// performs one request per call over a fresh connection, matching the broker's
// one-shot connection model.
type Client struct {
	socketPath string
	secret     string
	timeout    time.Duration
}

// NewClient builds a control-channel client.
func NewClient(socketPath, secret string) *Client {
	return &Client{socketPath: socketPath, secret: secret, timeout: 30 * time.Second}
}

// call sends one request and decodes the response envelope. A non-OK response
// is returned as a *CallError so callers can branch on the stable Code.
func (c *Client) call(op Op, payload any) (json.RawMessage, error) {
	var rawPayload json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("broker client: marshalling payload: %w", err)
		}
		rawPayload = b
	}
	req := Request{Version: BrokerProtocolVersion, Secret: c.secret, Op: op, Payload: rawPayload}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("broker client: marshalling request: %w", err)
	}

	conn, err := net.DialTimeout("unix", c.socketPath, c.timeout)
	if err != nil {
		return nil, fmt.Errorf("broker client: dialing %s: %w", c.socketPath, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(c.timeout))

	if err := protocol.WriteFrame(conn, reqBytes); err != nil {
		return nil, fmt.Errorf("broker client: writing request: %w", err)
	}
	respBytes, err := protocol.ReadFrame(conn)
	if err != nil {
		return nil, fmt.Errorf("broker client: reading response: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("broker client: decoding response: %w", err)
	}
	if !resp.OK {
		return nil, &CallError{Code: resp.Code, Message: resp.Error}
	}
	return resp.Result, nil
}

// CallError is a typed broker error carrying the stable machine-readable code.
type CallError struct {
	Code    string
	Message string
}

func (e *CallError) Error() string { return fmt.Sprintf("broker error [%s]: %s", e.Code, e.Message) }

// SessionCreate mints a new session.
func (c *Client) SessionCreate(workspaceID, workspaceName string) (*store.Session, error) {
	raw, err := c.call(OpSessionCreate, SessionCreateRequest{WorkspaceID: workspaceID, WorkspaceName: workspaceName})
	if err != nil {
		return nil, err
	}
	var res SessionResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return res.Session, nil
}

// SessionGet fetches a session projection.
func (c *Client) SessionGet(sessionID string) (*store.Session, error) {
	raw, err := c.call(OpSessionGet, SessionGetRequest{SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	var res SessionResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return res.Session, nil
}

// SessionEnd terminates a session.
func (c *Client) SessionEnd(sessionID string) (*store.Session, error) {
	raw, err := c.call(OpSessionEnd, SessionEndRequest{SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	var res SessionResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return res.Session, nil
}

// PlanPublish publishes a validated plan revision (raw JSON).
func (c *Client) PlanPublish(planJSON []byte) (*store.Session, error) {
	raw, err := c.call(OpPlanPublish, PlanPublishRequest{Plan: json.RawMessage(planJSON)})
	if err != nil {
		return nil, err
	}
	var res PlanPublishResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return res.Session, nil
}

// DecisionPoll returns whether the current revision has a durable decision.
func (c *Client) DecisionPoll(sessionID string) (*DecisionPollResult, error) {
	raw, err := c.call(OpDecisionPoll, DecisionPollRequest{SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	var res DecisionPollResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ApprovalClaim atomically claims an approval. The returned raw result carries
// the approval and claim sequence.
func (c *Client) ApprovalClaim(sessionID, digest string) (json.RawMessage, error) {
	return c.call(OpApprovalClaim, ApprovalClaimRequest{SessionID: sessionID, Digest: digest})
}

// SessionOpen mints a one-time browser review URL for a session.
func (c *Client) SessionOpen(sessionID string) (string, error) {
	raw, err := c.call(OpSessionOpen, SessionOpenRequest{SessionID: sessionID})
	if err != nil {
		return "", err
	}
	var res SessionOpenResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	return res.URL, nil
}

// Ping reports whether a broker is answering on the socket by issuing a cheap
// session.get for a non-existent id: a not_found response proves the broker is
// alive and authenticated. A dial/auth failure returns an error.
func (c *Client) Ping() error {
	_, err := c.SessionGet("cr-ping-nonexistent")
	if err == nil {
		return nil
	}
	var ce *CallError
	if errors.As(err, &ce) && ce.Code == CodeNotFound {
		return nil
	}
	return err
}
