package protocol

import (
	"strings"
	"testing"
)

func TestParsePlanRejectsUnknownFields(t *testing.T) {
	// A field the broker does not understand must not silently pass through.
	data := `{"protocol_version":1,"session_id":"s","revision":1,"goal":"g","summary":"","workspace":{"id":"w","display_name":"d"},"blocks":[],"actions":[],"surprise":true}`
	_, err := ParsePlan([]byte(data))
	if err == nil {
		t.Fatal("expected rejection of unknown field")
	}
}

func TestParsePlanRejectsTrailingData(t *testing.T) {
	data := `{"protocol_version":1,"session_id":"s","revision":1,"goal":"g","summary":"","workspace":{"id":"w","display_name":"d"},"blocks":[],"actions":[]}{"second":1}`
	_, err := ParsePlan([]byte(data))
	if err == nil {
		t.Fatal("expected rejection of trailing JSON value")
	}
	ve, ok := err.(*ValidationError)
	if !ok || ve.Field != "json" {
		t.Fatalf("expected json ValidationError, got %v", err)
	}
}

func TestParsePlanRejectsOversizedInput(t *testing.T) {
	big := make([]byte, MaxPlanBytes+1)
	for i := range big {
		big[i] = ' '
	}
	_, err := ParsePlan(big)
	if err == nil {
		t.Fatal("expected rejection of oversized plan input")
	}
	assertField(t, err, "plan")
}

func TestParsePlanAcceptsAbsentBlocksAndActions(t *testing.T) {
	// blocks and actions are optional (an absent array is equivalent to an
	// empty one). A plan may legitimately be informational and carry neither.
	// This is the contract the v1 schema and the Go validator agree on.
	data := `{"protocol_version":1,"session_id":"s","revision":1,"goal":"g","summary":"","workspace":{"id":"w","display_name":"d"}}`
	p, err := ParsePlan([]byte(data))
	if err != nil {
		t.Fatalf("plan without blocks/actions rejected: %v", err)
	}
	if len(p.Blocks) != 0 || len(p.Actions) != 0 {
		t.Fatalf("expected empty blocks/actions, got %d/%d", len(p.Blocks), len(p.Actions))
	}
}

func TestParsePlanRejectsMalformedJSON(t *testing.T) {
	_, err := ParsePlan([]byte(`{not json`))
	if err == nil {
		t.Fatal("expected rejection of malformed JSON")
	}
}

func TestParsePlanAcceptsRFCExample(t *testing.T) {
	// The plan from the RFC's "Example plan" section, with the placeholder
	// session ID normalized to an opaque id shape.
	data := `{
      "protocol_version": 1,
      "session_id": "random-256-bit-id",
      "revision": 4,
      "goal": "Add approval-bound agent execution",
      "summary": "Introduce a trusted local feedback broker",
      "workspace": {"id": "broker-issued-workspace-id", "display_name": "devtools"},
      "blocks": [
        {"id": "architecture", "kind": "markdown", "content": "The broker separates review from execution."}
      ],
      "actions": [
        {"id": "action-1", "kind": "write_patch", "title": "Add event-store schema", "targets": ["workspace://migrations"], "risk": "workspace_write"},
        {"id": "action-2", "kind": "run_command", "title": "Run the test suite", "program": "go", "args": ["test", "./..."], "cwd": "workspace://root", "risk": "local_process"}
      ]
    }`
	p, err := ParsePlan([]byte(data))
	if err != nil {
		t.Fatalf("RFC example plan rejected: %v", err)
	}
	if p.Revision != 4 || len(p.Actions) != 2 {
		t.Fatalf("unexpected parse result: %+v", p)
	}
	if !strings.HasPrefix(p.Actions[0].Targets[0], "workspace://") {
		t.Fatalf("unexpected target: %q", p.Actions[0].Targets[0])
	}
}
