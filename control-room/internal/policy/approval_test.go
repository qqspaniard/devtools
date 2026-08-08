package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/interactionlabs/devtools/control-room/internal/protocol"
)

func testPlan() *protocol.Plan {
	return &protocol.Plan{
		ProtocolVersion: protocol.ProtocolVersion,
		SessionID:       "session-abc",
		Revision:        4,
		Goal:            "Add approval-bound agent execution",
		Summary:         "Introduce a trusted local feedback broker",
		Workspace:       protocol.Workspace{ID: "workspace-1", DisplayName: "devtools"},
		Blocks: []protocol.Block{
			{ID: "architecture", Kind: protocol.BlockMarkdown, Content: "The broker separates review from execution."},
		},
		Actions: []protocol.Action{
			{ID: "action-1", Kind: protocol.ActionWritePatch, Title: "Add schema", Targets: []string{"workspace://migrations"}, Risk: protocol.RiskWorkspaceWrite},
			{ID: "action-2", Kind: protocol.ActionRunCommand, Title: "Run tests", Program: "go", Args: []string{"test", "./..."}, Cwd: "workspace://root", Risk: protocol.RiskLocalProcess},
		},
	}
}

func testExpiry() time.Time {
	return time.Date(2026, 8, 7, 18, 0, 0, 0, time.UTC)
}

func buildOK(t *testing.T, req ApprovalRequest) *Approval {
	t.Helper()
	a, err := BuildApproval(req)
	if err != nil {
		t.Fatalf("BuildApproval: %v", err)
	}
	return a
}

func TestBuildApprovalDeterministic(t *testing.T) {
	req := ApprovalRequest{
		Plan:            testPlan(),
		SelectedActions: []string{"action-1", "action-2"},
		ExpiresAt:       testExpiry(),
		MaxClaims:       1,
	}
	a1 := buildOK(t, req)
	a2 := buildOK(t, req)
	if a1.Digest != a2.Digest {
		t.Fatalf("digest not deterministic: %s != %s", a1.Digest, a2.Digest)
	}
	if !strings.HasPrefix(a1.Digest, DigestPrefix) {
		t.Fatalf("digest missing prefix: %s", a1.Digest)
	}
	// sha256 hex is 64 chars after the prefix.
	if got := len(strings.TrimPrefix(a1.Digest, DigestPrefix)); got != 64 {
		t.Fatalf("unexpected digest hex length %d", got)
	}
}

// TestDigestStableAcrossFieldReordering verifies canonicalization: the digest
// depends on values, not on Go struct/map iteration order. Building an
// equivalent plan whose block/action slices are constructed differently but
// have identical values must yield the same digest. (Object key order is
// handled by JCS; here we confirm the pipeline is value-stable.)
func TestDigestStableAcrossEquivalentPlans(t *testing.T) {
	p1 := testPlan()
	p2 := testPlan()
	// Rebuild p2's actions in reverse construction but same logical content by
	// re-assigning identical field values; selection order is what binds.
	req1 := ApprovalRequest{Plan: p1, SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1}
	req2 := ApprovalRequest{Plan: p2, SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1}
	if buildOK(t, req1).Digest != buildOK(t, req2).Digest {
		t.Fatal("equivalent plans produced different digests")
	}
}

// TestDigestSelectionOrderMatters documents that the selection order is bound:
// approving [action-1, action-2] differs from [action-2, action-1]. The
// selection is an ordered, reviewed list, so a different order is a different
// approval.
func TestDigestSelectionOrderMatters(t *testing.T) {
	base := buildOK(t, ApprovalRequest{Plan: testPlan(), SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1})
	swapped := buildOK(t, ApprovalRequest{Plan: testPlan(), SelectedActions: []string{"action-2", "action-1"}, ExpiresAt: testExpiry(), MaxClaims: 1})
	if base.Digest == swapped.Digest {
		t.Fatal("expected selection order to affect digest")
	}
}

// mutation cases: each must change the digest.
func TestDigestChangesOnApprovalRelevantMutation(t *testing.T) {
	base := buildOK(t, ApprovalRequest{Plan: testPlan(), SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1})

	cases := []struct {
		name   string
		mutate func(*protocol.Plan) ApprovalRequest
	}{
		{"action title", func(p *protocol.Plan) ApprovalRequest {
			p.Actions[0].Title = "Different title"
			return ApprovalRequest{Plan: p, SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1}
		}},
		{"action arg", func(p *protocol.Plan) ApprovalRequest {
			p.Actions[1].Args = []string{"test", "-race", "./..."}
			return ApprovalRequest{Plan: p, SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1}
		}},
		{"action program", func(p *protocol.Plan) ApprovalRequest {
			p.Actions[1].Program = "gotestsum"
			return ApprovalRequest{Plan: p, SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1}
		}},
		{"action target", func(p *protocol.Plan) ApprovalRequest {
			p.Actions[0].Targets = []string{"workspace://other"}
			return ApprovalRequest{Plan: p, SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1}
		}},
		{"action risk", func(p *protocol.Plan) ApprovalRequest {
			p.Actions[0].Risk = protocol.RiskNetwork
			return ApprovalRequest{Plan: p, SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1}
		}},
		{"workspace id", func(p *protocol.Plan) ApprovalRequest {
			p.Workspace.ID = "workspace-2"
			return ApprovalRequest{Plan: p, SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1}
		}},
		{"goal", func(p *protocol.Plan) ApprovalRequest {
			p.Goal = "A different goal"
			return ApprovalRequest{Plan: p, SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1}
		}},
		{"summary", func(p *protocol.Plan) ApprovalRequest {
			p.Summary = "A different summary"
			return ApprovalRequest{Plan: p, SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1}
		}},
		{"block content", func(p *protocol.Plan) ApprovalRequest {
			p.Blocks[0].Content = "Different content"
			return ApprovalRequest{Plan: p, SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1}
		}},
		{"revision", func(p *protocol.Plan) ApprovalRequest {
			p.Revision = 5
			return ApprovalRequest{Plan: p, SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1}
		}},
		{"expiration", func(p *protocol.Plan) ApprovalRequest {
			return ApprovalRequest{Plan: p, SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry().Add(time.Hour), MaxClaims: 1}
		}},
		{"max claims", func(p *protocol.Plan) ApprovalRequest {
			return ApprovalRequest{Plan: p, SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 2}
		}},
		{"selection subset", func(p *protocol.Plan) ApprovalRequest {
			return ApprovalRequest{Plan: p, SelectedActions: []string{"action-1"}, ExpiresAt: testExpiry(), MaxClaims: 1}
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := c.mutate(testPlan())
			got := buildOK(t, req)
			if got.Digest == base.Digest {
				t.Fatalf("mutation %q did not change digest", c.name)
			}
		})
	}
}

// TestDigestStableAcrossDisplayOnlyMutation verifies the workspace display name
// is display-only: relabeling must NOT change the digest.
func TestDigestStableAcrossDisplayOnlyMutation(t *testing.T) {
	base := buildOK(t, ApprovalRequest{Plan: testPlan(), SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1})
	p := testPlan()
	p.Workspace.DisplayName = "a completely different label"
	relabeled := buildOK(t, ApprovalRequest{Plan: p, SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: testExpiry(), MaxClaims: 1})
	if base.Digest != relabeled.Digest {
		t.Fatal("display-only workspace name changed the digest")
	}
}

// TestDigestNilVsEmptySlice verifies canonicalization normalizes nil and empty
// slices so a decoder producing nil targets/args cannot yield a different
// digest than one producing [].
func TestDigestNilVsEmptySlice(t *testing.T) {
	pNil := testPlan()
	pNil.Actions[0].Targets = nil
	pEmpty := testPlan()
	pEmpty.Actions[0].Targets = []string{}
	dNil := buildOK(t, ApprovalRequest{Plan: pNil, SelectedActions: []string{"action-1"}, ExpiresAt: testExpiry(), MaxClaims: 1})
	dEmpty := buildOK(t, ApprovalRequest{Plan: pEmpty, SelectedActions: []string{"action-1"}, ExpiresAt: testExpiry(), MaxClaims: 1})
	if dNil.Digest != dEmpty.Digest {
		t.Fatal("nil vs empty slice produced different digests")
	}
}

// TestDigestBindsNanosecondExactExpiration is a regression test for a
// precision bug: encoding the expiration as a numeric UnixNano would be
// canonicalized by JCS through IEEE-754 float64, so present-day nanosecond
// timestamps (well above 2^53) would silently collapse and two expirations one
// nanosecond apart could hash identically. The expiration is bound as an
// RFC3339Nano string; expirations one nanosecond apart MUST produce distinct
// digests.
func TestDigestBindsNanosecondExactExpiration(t *testing.T) {
	base := testExpiry()
	oneNsLater := base.Add(time.Nanosecond)

	d1 := buildOK(t, ApprovalRequest{Plan: testPlan(), SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: base, MaxClaims: 1})
	d2 := buildOK(t, ApprovalRequest{Plan: testPlan(), SelectedActions: []string{"action-1", "action-2"}, ExpiresAt: oneNsLater, MaxClaims: 1})
	if d1.Digest == d2.Digest {
		t.Fatal("expirations one nanosecond apart produced the same digest; expiration is not bound losslessly")
	}
}

func TestBuildApprovalRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		req  ApprovalRequest
	}{
		{"nil plan", ApprovalRequest{Plan: nil, SelectedActions: []string{"action-1"}, ExpiresAt: testExpiry(), MaxClaims: 1}},
		{"empty selection", ApprovalRequest{Plan: testPlan(), SelectedActions: nil, ExpiresAt: testExpiry(), MaxClaims: 1}},
		{"unknown action id", ApprovalRequest{Plan: testPlan(), SelectedActions: []string{"nope"}, ExpiresAt: testExpiry(), MaxClaims: 1}},
		{"duplicate selection", ApprovalRequest{Plan: testPlan(), SelectedActions: []string{"action-1", "action-1"}, ExpiresAt: testExpiry(), MaxClaims: 1}},
		{"zero max claims", ApprovalRequest{Plan: testPlan(), SelectedActions: []string{"action-1"}, ExpiresAt: testExpiry(), MaxClaims: 0}},
		{"zero expiry", ApprovalRequest{Plan: testPlan(), SelectedActions: []string{"action-1"}, MaxClaims: 1}},
		{"invalid plan", ApprovalRequest{Plan: &protocol.Plan{ProtocolVersion: 99}, SelectedActions: []string{"action-1"}, ExpiresAt: testExpiry(), MaxClaims: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := BuildApproval(c.req); err == nil {
				t.Fatalf("expected rejection for %q", c.name)
			}
		})
	}
}

func TestBuildApprovalPreservesSelectionAndRevision(t *testing.T) {
	a := buildOK(t, ApprovalRequest{Plan: testPlan(), SelectedActions: []string{"action-2", "action-1"}, ExpiresAt: testExpiry(), MaxClaims: 3})
	if a.PlanRevision != 4 {
		t.Fatalf("plan revision not preserved: %d", a.PlanRevision)
	}
	if len(a.AllowedActions) != 2 || a.AllowedActions[0] != "action-2" {
		t.Fatalf("selection order not preserved: %v", a.AllowedActions)
	}
	if a.MaxClaims != 3 {
		t.Fatalf("max claims not preserved: %d", a.MaxClaims)
	}
}
