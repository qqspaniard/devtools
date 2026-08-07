# Control Room — Phase 0 Foundation

This document records what the Phase 0 slice implements, the load-bearing
decisions it makes, and the choices it explicitly defers. It complements
[`RFC.md`](../RFC.md); the RFC is the product/architecture spec, this is the
record of Phase 0 implementation decisions.

## Scope of this slice

Phase 0 is **protocol and policy only**. It contains:

- A Go module and a minimal, compilable CLI skeleton (`cmd/control-room`).
- Versioned protocol types with strict validation and explicit size/count
  limits (`internal/protocol`).
- A length-prefixed JSON frame codec with pre-allocation size rejection
  (`internal/protocol/frame.go`).
- The session state machine and its authoritative transition matrix
  (`internal/policy/statemachine.go`).
- Deterministic approval canonicalization and SHA-256 digest generation using
  RFC 8785 / JCS semantics (`internal/policy/approval.go`).
- A concurrency-safe, in-memory approval **claim authority**
  (`internal/policy/claim.go`) — policy-domain code, explicitly not durable.
- Versioned JSON Schemas for the public payloads (`schema/v1/`), kept in sync
  with the Go types by a cross-check test.
- Comprehensive tests, including adversarial cases, run under `-race`.

It deliberately does **not** contain: SQLite or any persistence, Unix sockets,
HTTP, the browser UI, TypeScript, the broker process lifecycle, or the CLI
subcommand surface (`open`, `session create`, ...). Those arrive in Phase 1+.

## Decisions made in Phase 0

### Agent transport framing: length-prefixed JSON

The RFC left this open (Open Question 2: newline-delimited vs length-prefixed).
Phase 0 chooses **length-prefixed** framing: a 4-byte big-endian unsigned length
header followed by exactly that many bytes of a single JSON value.

Rationale: the reader validates a frame's declared size against `MaxFrameBytes`
*before allocating any payload buffer*, so a hostile length header cannot force a
large allocation. Newline-delimited JSON cannot bound a frame before scanning it.
The codec guarantees exactly one framed payload per read; the payload parser
additionally rejects trailing/multiple JSON values and unknown object fields.

This is a Phase 0 decision that supersedes the RFC open question for the MVP; it
can be revisited if a transport change is warranted, but the message semantics
are designed to survive a transport swap.

### Approval-relevance: the whole revision is relevant unless explicitly display-only

The RFC's Open Question 5 (which fields are approval-relevant vs display-only)
is resolved with the **safer default**: every field of a published plan revision
is bound into the approval digest **except** fields explicitly modeled and
documented as display-only. In Phase 0 the only display-only field is
`workspace.display_name` (the workspace *identity* is `workspace.id`, which is
bound). Goal, summary, every block, every action and all of its arguments, the
workspace id, revision, expiration, max claims, and the permission envelope are
all bound.

Consequence: changing any of those produces a different digest and invalidates
any prior approval. Adding a new display-only field in a later phase is an
explicit, reviewed decision that must update both the Go canonicalization and
the schema doc.

### Canonicalization: RFC 8785 (JCS) via a pinned dependency

The digest is computed over the RFC 8785 (JSON Canonicalization Scheme)
canonical form of the bound fields, then SHA-256. Number and string
canonicalization are **not hand-rolled**; they are delegated to
[`github.com/gowebpki/jcs`](https://github.com/gowebpki/jcs) `v1.0.1`
(Apache-2.0), a maintained fork of the cyberphone RFC 8785 reference
implementation. It is pinned in `go.mod`/`go.sum`.

Why this library over alternatives: it is the most reputable, widely-used Go JCS
implementation and is cross-platform (macOS-first matters here). Newer
single-maintainer packages were either very low-star/brand-new or Linux-only,
which is unsuitable for a macOS-first tool.

The digest binds, in one canonical document: protocol version, session ID, the
exact canonical plan revision, the canonical selected actions, workspace
identity, the permission envelope (sorted set of risk classes across the
selected actions), expiration (as a canonical UTC RFC3339Nano string), and max
claims. Expiration is bound as a string rather than a numeric timestamp because
RFC 8785 canonicalizes JSON numbers through IEEE-754 float64: a nanosecond
integer above 2^53 would lose precision and fail to bind the exact expiration.
Any future digest field whose value can exceed 2^53 must likewise be encoded as
a string.

Slices that carry semantic order (command `args`, action `targets`, and the
selected-action list) preserve order; `nil` and `[]` are normalized so a decoder
producing either yields the same digest.

### Broker creates the digest; agent references/claims it

`BuildApproval` is the broker-side constructor and the sole creator of
approvals. The agent later presents `(session_id, digest)` to `Claim`. The claim
authority enforces, fail-closed and in order: unknown digest → session/digest
mismatch → supersession → expiration → exhausted claims.

### IDs are opaque, shape-validated, bounded — not entropy-checked

All identifiers (session, workspace, block, action, targets, cwd) are opaque
strings validated for a conservative URL/log-safe character set
(`[A-Za-z0-9._:/-]`) and bounded length (`MaxIDBytes = 256`). This deliberately
accepts scheme-like references such as `workspace://root`. Phase 0 makes **no
claim about the entropy** of caller-supplied IDs — it validates form only.

### Time is injected, not read deep inside policy

The claim authority takes a `now func() time.Time`. Tests pass a controllable
clock; production would pass `time.Now`. Expiration comparison is exclusive
(a claim *at* the expiration instant is expired). No policy code calls
`time.Now` internally.

### The claim authority is policy, not persistence

`ClaimAuthority` is a concurrency-safe **in-memory** registry. It exists so the
single-use / max-claims / replay / supersession rules are executable and
adversarially testable in a phase that has no store yet. It is **not durable**: a
process restart discards all approvals. The RFC's durable, restart-safe claim
guarantee is a Phase 1 responsibility of the SQLite event store; this type is a
faithful model of the *rules*, not a stand-in for the *store*.

### Fail-closed everywhere

Unknown protocol versions, unknown action/risk/block kinds, invalid or duplicate
IDs, out-of-range collections, oversized frames/plans, trailing/multiple JSON
values, unknown JSON fields, missing selection, unknown selected action IDs,
zero/absent expiration, stale (superseded) revisions, and digest mismatches all
produce typed errors rather than silent acceptance or truncation.

## Explicitly deferred choices

These are recorded so a later phase does not have to re-derive that they were
intentionally left open:

- **SQLite driver** (RFC Open Question 3). Not selected. The RFC's initial
  candidate is `modernc.org/sqlite` (pure Go, no CGO), pending a benchmark on
  binary size, startup, cross-compilation, and write behavior. Phase 0 adds no
  persistence at all.
- **SSR library** (RFC Open Question 1): `html/template` vs `templ`. Not
  selected. No HTML is rendered in Phase 0.
- **Browser layer**: bootstrap capability exchange, CSP, cookies, CSRF, SSE — all
  deferred to Phase 2/3. Phase 0 has no HTTP surface.
- **Broker lifecycle**: socket/lock ownership, idle self-stop (RFC Open
  Question 4), PID diagnostics — deferred to Phase 1.
- **CLI subcommand surface**: `open`, `session create`, `plan publish`,
  `decision poll`, etc. — deferred to Phase 1. Phase 0 exposes only `version`,
  `validate-plan`, and `help`.
- **Cancel/fail granularity**: the state machine collapses `cancel` and `fail`
  from `running` into a terminal `completed` state. The distinction between
  cancelled, failed, and cleanly completed runs is intended to live in the event
  history (Phase 1), not in the coarse session state.
- **Retention/quotas** (RFC Open Question 6) and **workspace-root confirmation**
  (Open Question 7): deferred; no state is persisted yet.

## Layout

```
control-room/
├── cmd/control-room/main.go        # minimal CLI skeleton
├── internal/protocol/              # versioned types, limits, validation, frame codec
├── internal/policy/                # state machine, approval digest, claim authority
├── schema/v1/                      # versioned JSON Schemas (plan, approval)
├── docs/phase-0.md                 # this file
├── go.mod / go.sum
└── RFC.md
```

## Verification

From `control-room/`:

```
gofmt -l .        # expect no output
go vet ./...
go test -race ./...
```
