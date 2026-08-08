package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// schemaDir is the authoritative location of the v1 JSON Schemas, relative to
// this package directory (go test runs with the package dir as CWD). The
// schemas live at control-room/schema/v1 as the single source of truth; this
// test reads them from disk rather than embedding a copy, so there is exactly
// one canonical file per schema.
const schemaDir = "../../schema/v1"

// loadSchema reads and parses one schema file.
func loadSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(schemaDir, name))
	if err != nil {
		t.Fatalf("read schema %s: %v", name, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse schema %s: %v", name, err)
	}
	return m
}

func TestSchemasAreWellFormed(t *testing.T) {
	for _, name := range []string{"plan.json", "approval.json"} {
		m := loadSchema(t, name)
		if m["$schema"] == nil || m["$id"] == nil {
			t.Fatalf("%s missing $schema/$id", name)
		}
	}
}

// TestPlanSchemaLimitsMatchGo cross-checks that the byte/count limits declared
// in the plan schema equal the Go constants. This is the highest-value sync
// check: a schema that permits more than the Go validator (or vice versa) is a
// security-relevant divergence.
func TestPlanSchemaLimitsMatchGo(t *testing.T) {
	m := loadSchema(t, "plan.json")
	props := m["properties"].(map[string]any)
	defs := m["$defs"].(map[string]any)

	checkMax(t, "protocol_version const", intOf(t, props["protocol_version"].(map[string]any)["const"]), ProtocolVersion)
	checkMax(t, "goal maxLength", intOf(t, props["goal"].(map[string]any)["maxLength"]), MaxGoalBytes)
	checkMax(t, "summary maxLength", intOf(t, props["summary"].(map[string]any)["maxLength"]), MaxSummaryBytes)
	checkMax(t, "blocks maxItems", intOf(t, props["blocks"].(map[string]any)["maxItems"]), MaxBlocksPerRevision)
	checkMax(t, "actions maxItems", intOf(t, props["actions"].(map[string]any)["maxItems"]), MaxActionsPerRevision)

	opaque := defs["opaqueId"].(map[string]any)
	checkMax(t, "opaqueId maxLength", intOf(t, opaque["maxLength"]), MaxIDBytes)
	checkMax(t, "opaqueId minLength", intOf(t, opaque["minLength"]), MinIDBytes)

	block := defs["block"].(map[string]any)
	bprops := block["properties"].(map[string]any)
	checkMax(t, "block.content maxLength", intOf(t, bprops["content"].(map[string]any)["maxLength"]), MaxBlockContentBytes)

	action := defs["action"].(map[string]any)
	aprops := action["properties"].(map[string]any)
	checkMax(t, "action.title maxLength", intOf(t, aprops["title"].(map[string]any)["maxLength"]), MaxActionTitleBytes)
	checkMax(t, "action.targets maxItems", intOf(t, aprops["targets"].(map[string]any)["maxItems"]), MaxTargetsPerAction)
	checkMax(t, "action.program maxLength", intOf(t, aprops["program"].(map[string]any)["maxLength"]), MaxProgramBytes)
	checkMax(t, "action.args maxItems", intOf(t, aprops["args"].(map[string]any)["maxItems"]), MaxArgsPerAction)
	checkMax(t, "action.cwd maxLength", intOf(t, aprops["cwd"].(map[string]any)["maxLength"]), MaxCwdBytes)
}

// TestPlanSchemaEnumsMatchGo cross-checks the closed enum sets in the schema
// against the Go closed sets, so a kind/risk added on one side but not the
// other is caught.
func TestPlanSchemaEnumsMatchGo(t *testing.T) {
	m := loadSchema(t, "plan.json")
	defs := m["$defs"].(map[string]any)

	blockKinds := enumOf(t, defs["block"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any))
	assertSameSet(t, "block kinds", blockKinds, goBlockKinds())

	action := defs["action"].(map[string]any)["properties"].(map[string]any)
	actionKinds := enumOf(t, action["kind"].(map[string]any))
	assertSameSet(t, "action kinds", actionKinds, goActionKinds())

	riskClasses := enumOf(t, action["risk"].(map[string]any))
	assertSameSet(t, "risk classes", riskClasses, goRiskClasses())
}

// TestApprovalSchemaLimitsMatchGo cross-checks the approval schema.
func TestApprovalSchemaLimitsMatchGo(t *testing.T) {
	m := loadSchema(t, "approval.json")
	props := m["properties"].(map[string]any)
	checkMax(t, "allowed_action_ids maxItems", intOf(t, props["allowed_action_ids"].(map[string]any)["maxItems"]), MaxSelectedActions)
	checkMax(t, "max_claims maximum", intOf(t, props["max_claims"].(map[string]any)["maximum"]), MaxClaimsCeiling)
}

// --- helpers ---

func goBlockKinds() []string {
	out := make([]string, 0, len(validBlockKinds))
	for k := range validBlockKinds {
		out = append(out, string(k))
	}
	return out
}

func goActionKinds() []string {
	out := make([]string, 0, len(validActionKinds))
	for k := range validActionKinds {
		out = append(out, string(k))
	}
	return out
}

func goRiskClasses() []string {
	out := make([]string, 0, len(validRiskClasses))
	for k := range validRiskClasses {
		out = append(out, string(k))
	}
	return out
}

func intOf(t *testing.T, v any) int {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("expected number, got %T (%v)", v, v)
	}
	return int(f)
}

func enumOf(t *testing.T, schema map[string]any) []string {
	t.Helper()
	raw, ok := schema["enum"].([]any)
	if !ok {
		t.Fatalf("expected enum array, got %T", schema["enum"])
	}
	out := make([]string, len(raw))
	for i, e := range raw {
		out[i] = e.(string)
	}
	return out
}

func checkMax(t *testing.T, name string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: schema=%d Go=%d (schema and Go limits must match)", name, got, want)
	}
}

func assertSameSet(t *testing.T, name string, a, b []string) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("%s: set size mismatch schema=%d Go=%d", name, len(a), len(b))
	}
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := set[s]; !ok {
			t.Fatalf("%s: value %q in Go but not schema", name, s)
		}
	}
}
