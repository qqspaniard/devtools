package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// validPlan returns a minimal but complete valid plan for mutation in tests.
func validPlan() *Plan {
	return &Plan{
		ProtocolVersion: ProtocolVersion,
		SessionID:       "session-abc",
		Revision:        4,
		Goal:            "Add approval-bound agent execution",
		Summary:         "Introduce a trusted local feedback broker",
		Workspace:       Workspace{ID: "workspace-1", DisplayName: "devtools"},
		Blocks: []Block{
			{ID: "architecture", Kind: BlockMarkdown, Content: "The broker separates review from execution."},
		},
		Actions: []Action{
			{ID: "action-1", Kind: ActionWritePatch, Title: "Add schema", Targets: []string{"workspace://migrations"}, Risk: RiskWorkspaceWrite},
			{ID: "action-2", Kind: ActionRunCommand, Title: "Run tests", Program: "go", Args: []string{"test", "./..."}, Cwd: "workspace://root", Risk: RiskLocalProcess},
		},
	}
}

func TestPlanValidateAcceptsValid(t *testing.T) {
	if err := validPlan().Validate(); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
}

func TestPlanValidateRejectsUnknownProtocolVersion(t *testing.T) {
	p := validPlan()
	p.ProtocolVersion = 2
	err := p.Validate()
	if err == nil {
		t.Fatal("expected rejection of unknown protocol version")
	}
	assertField(t, err, "protocol_version")
}

func TestPlanValidateRejectsUnknownActionKind(t *testing.T) {
	p := validPlan()
	p.Actions[0].Kind = "delete_everything"
	err := p.Validate()
	if err == nil {
		t.Fatal("expected rejection of unknown action kind")
	}
	assertField(t, err, "action.kind")
}

func TestPlanValidateRejectsUnknownRiskClass(t *testing.T) {
	p := validPlan()
	p.Actions[0].Risk = "nuclear"
	err := p.Validate()
	if err == nil {
		t.Fatal("expected rejection of unknown risk class")
	}
	assertField(t, err, "action.risk")
}

func TestPlanValidateRejectsUnknownBlockKind(t *testing.T) {
	p := validPlan()
	p.Blocks[0].Kind = "iframe"
	err := p.Validate()
	if err == nil {
		t.Fatal("expected rejection of unknown block kind")
	}
	assertField(t, err, "block.kind")
}

func TestPlanValidateRejectsDuplicateActionIDs(t *testing.T) {
	p := validPlan()
	p.Actions[1].ID = p.Actions[0].ID
	err := p.Validate()
	if err == nil {
		t.Fatal("expected rejection of duplicate action IDs")
	}
	assertField(t, err, "actions")
}

func TestPlanValidateRejectsDuplicateBlockIDs(t *testing.T) {
	p := validPlan()
	p.Blocks = append(p.Blocks, Block{ID: "architecture", Kind: BlockMarkdown, Content: "dup"})
	err := p.Validate()
	if err == nil {
		t.Fatal("expected rejection of duplicate block IDs")
	}
	assertField(t, err, "blocks")
}

func TestPlanValidateRejectsRevisionBelowOne(t *testing.T) {
	p := validPlan()
	p.Revision = 0
	err := p.Validate()
	if err == nil {
		t.Fatal("expected rejection of revision < 1")
	}
	assertField(t, err, "revision")
}

func TestPlanValidateRejectsEmptyGoal(t *testing.T) {
	p := validPlan()
	p.Goal = ""
	if err := p.Validate(); err == nil {
		t.Fatal("expected rejection of empty goal")
	}
}

func TestPlanValidateRejectsCommandFieldsOnWritePatch(t *testing.T) {
	p := validPlan()
	// action-1 is write_patch; giving it a program must be rejected so it
	// cannot smuggle unreviewed execution parameters.
	p.Actions[0].Program = "rm"
	err := p.Validate()
	if err == nil {
		t.Fatal("expected rejection of program on write_patch action")
	}
	assertField(t, err, "action.program")
}

func TestPlanValidateRejectsEmptyProgramOnRunCommand(t *testing.T) {
	p := validPlan()
	p.Actions[1].Program = ""
	err := p.Validate()
	if err == nil {
		t.Fatal("expected rejection of empty program on run_command")
	}
	assertField(t, err, "action.program")
}

func TestPlanValidateRejectsTooManyActions(t *testing.T) {
	p := validPlan()
	p.Actions = p.Actions[:0]
	for i := 0; i < MaxActionsPerRevision+1; i++ {
		p.Actions = append(p.Actions, Action{
			ID: "a-" + itoa(i), Kind: ActionWritePatch, Title: "x", Risk: RiskWorkspaceWrite,
		})
	}
	err := p.Validate()
	if err == nil {
		t.Fatal("expected rejection of too many actions")
	}
	assertField(t, err, "actions")
}

func TestValidateIDRejectsDisallowedChars(t *testing.T) {
	p := validPlan()
	p.SessionID = "bad id with spaces"
	err := p.Validate()
	if err == nil {
		t.Fatal("expected rejection of id with spaces")
	}
	assertField(t, err, "session_id")
}

func TestValidateIDRejectsOverlong(t *testing.T) {
	p := validPlan()
	p.SessionID = strings.Repeat("a", MaxIDBytes+1)
	err := p.Validate()
	if err == nil {
		t.Fatal("expected rejection of overlong id")
	}
	assertField(t, err, "session_id")
}

func TestValidateIDAcceptsSchemeLikeForm(t *testing.T) {
	// "workspace://root" style references must be accepted as opaque IDs.
	p := validPlan()
	p.Actions[1].Cwd = "workspace://root"
	if err := p.Validate(); err != nil {
		t.Fatalf("scheme-like cwd rejected: %v", err)
	}
}

func TestHasAction(t *testing.T) {
	p := validPlan()
	if !p.HasAction("action-1") {
		t.Fatal("expected action-1 present")
	}
	if p.HasAction("missing") {
		t.Fatal("did not expect missing action")
	}
}

// assertField asserts the error is a *ValidationError naming the given field.
func assertField(t *testing.T, err error, field string) {
	t.Helper()
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if ve.Field != field {
		t.Fatalf("expected field %q, got %q (%v)", field, ve.Field, err)
	}
}

// itoa is a tiny helper to avoid importing strconv in table setup.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// mustMarshalPlan JSON-encodes a plan for codec/round-trip tests.
func mustMarshalPlan(t *testing.T, p *Plan) []byte {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	return raw
}
