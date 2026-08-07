# RFC: Control Room — A Secure Local Agent Review Broker

- **Status:** Draft
- **Date:** 2026-08-07
- **Authors:** David and Jhin
- **Target:** macOS first; portable protocol and architecture

## Summary

Control Room is a local, agent-neutral review surface for the **plan → approve → execute → review** loop.

An agent publishes a structured plan. Control Room presents the plan, proposed actions, diffs, questions, and progress in a browser GUI. The user can annotate it, request changes, approve exact actions, and review their results. The agent remains responsible for execution under its existing permissions; the GUI does not run commands.

Control Room ships as one Go binary containing:

- a local broker;
- an embedded SQLite database;
- server-rendered HTML;
- compiled TypeScript/CSS assets embedded with `go:embed`;
- a Unix-socket agent interface;
- a loopback-only browser interface.

There is no SQLite server or external database process. SQLite is linked into the binary and writes one private local database file.

## Motivation

Agent workflows generally expose plans and execution progress through linear chat. This is adequate for small changes but degrades for:

- plans containing multiple actions and dependencies;
- proposed code or configuration diffs;
- targeted feedback on one section or action;
- approval of some actions but not others;
- long-running execution with progress and partial failures;
- distinguishing what was reviewed from what was ultimately executed.

[Lavish-Axi](https://github.com/kunchenguid/lavish-axi) demonstrates a compelling local browser feedback loop: an agent publishes an artifact, a human reviews it visually, feedback is queued, and the agent polls for it. Control Room retains that interaction model while adopting a stricter trust boundary suitable for approving agent actions.

The Lavish-Axi source was reviewed at commit `542819086b799d907e7eddf0a1fadd2eb60c3dfe`. Its strongest reusable ideas include durable feedback, explicit agent presence, revision-aware review, semantic annotations, and a browser conversation surface. Control Room intentionally does not reuse its unauthenticated localhost API, arbitrary HTML execution model, file-path-based session identity, hosted sharing, telemetry, or runtime package installation.

## Goals

1. Make agent plans easier to understand and review than chat-only output.
2. Bind approval to the exact plan revision, actions, arguments, workspace, and permission envelope reviewed by the user.
3. Keep the GUI outside the execution boundary: it grants capabilities but never invokes tools directly.
4. Ship a single, auditable executable with no runtime package installation or external service.
5. Support multiple agent clients through a small, versioned protocol.
6. Recover cleanly after broker, browser, or agent interruption without losing decisions.
7. Be local-only and make no outbound network requests by default.
8. Keep the first implementation small enough to reason about and adversarially test.

## Non-goals

The initial release will not:

- execute shell commands or agent tools;
- replace an agent's permission or sandbox system;
- render arbitrary agent-authored HTML or JavaScript;
- expose the broker over LAN, Tailscale, or the public internet;
- provide hosted sharing or collaboration;
- install editor plugins or session hooks automatically;
- collect telemetry;
- manage secrets;
- support multiple operating-system users sharing one broker;
- implement a general workflow engine.

## Product model

The core interaction is:

1. The agent creates a session and publishes a structured plan revision.
2. Control Room opens or refreshes the local review GUI.
3. The user annotates blocks or actions, answers questions, and either requests revision or approves selected actions.
4. The agent polls or streams decisions over the local agent protocol.
5. The agent claims a valid approval, executes through its normal tool system, and reports progress and results.
6. The user reviews the result and completes the session or requests another revision.

### Session state machine

```text
draft
  └── publish ──▶ awaiting_approval
                       ├── reject ───────▶ rejected
                       ├── request edit ─▶ draft
                       └── approve ──────▶ approved
                                              ├── expire ─▶ expired
                                              └── claim ──▶ running
                                                               ├── cancel
                                                               ├── fail
                                                               └── review
                                                                    ├── complete
                                                                    └── revise ─▶ draft
```

The broker validates every transition. Frontend state is a projection, not authority.

## User experience

### Review page

The initial GUI should contain:

- session goal and summary;
- plan sections with stable semantic IDs;
- proposed action cards;
- code and configuration diffs;
- questions requiring user choices;
- risk and permission summaries;
- annotations and conversation history;
- agent presence: `waiting`, `listening`, `working`, or `disconnected`;
- event timeline and execution progress;
- approve, reject, request changes, and end-session controls.

### Structured content

The broker renders trusted components from typed data:

- Markdown with raw HTML disabled;
- action cards generated from schemas;
- diffs generated or validated by the broker;
- Mermaid rendered by pinned, bundled code;
- questions generated from JSON Schema;
- plaintext or ANSI-parsed command output.

Every annotatable block and action has a stable ID supplied in the plan protocol. Annotations target those IDs rather than CSS selectors or rendered DOM structure.

Arbitrary HTML is deliberately excluded from the MVP. If added later, it requires a separate threat model and sandbox process/origin.

## Architecture

```text
┌──────────────────────┐
│ Agent adapter / CLI  │
│ OpenCode first       │
└──────────┬───────────┘
           │ Unix domain socket
           │ typed, authenticated protocol
           ▼
┌─────────────────────────────────────────┐
│ Control Room broker                     │
│                                         │
│  protocol     policy / approval verifier│
│      │                  │               │
│      └──── event store ─┘               │
│                  │                      │
│          session projections            │
└──────────┬──────────────────────────────┘
           │ random-port loopback HTTP
           │ one-time browser bootstrap
           ▼
┌─────────────────────────────────────────┐
│ Review GUI                              │
│ Go SSR + hydrated TypeScript islands    │
│                                         │
│ Can decide. Cannot execute.             │
└─────────────────────────────────────────┘
```

### Single-binary packaging

The build pipeline is:

```text
TypeScript / CSS ── esbuild ──▶ static assets
                                      │
Go templates + migrations + assets ───┤ go:embed
                                      ▼
                              control-room binary
```

The binary contains the broker, schema migrations, HTML templates, JavaScript, and CSS. Users install or copy one executable.

The frontend does not use TypeScript SSR inside Go. Embedding a JavaScript runtime such as V8, QuickJS, or Goja would add complexity without improving the security boundary. Initial HTML is rendered using Go `html/template` or `templ`; TypeScript hydrates interactive islands.

### Suggested repository layout

```text
control-room/
├── cmd/control-room/
├── internal/
│   ├── broker/       # process lifecycle and coordination
│   ├── protocol/     # versioned agent messages
│   ├── store/        # SQLite, migrations, projections
│   ├── policy/       # approval and transition validation
│   └── web/          # HTTP handlers and templates
├── web/
│   ├── src/          # TypeScript and CSS
│   └── dist/         # generated embedded assets
├── migrations/
└── RFC.md
```

## Process lifecycle

The same executable supports short CLI operations and the long-lived broker:

```text
control-room open
control-room session create
control-room plan publish
control-room decision poll
control-room event append
control-room session end
control-room stop
```

Each invocation:

1. attempts to connect to the private Unix socket;
2. reuses the broker when available;
3. otherwise acquires an exclusive broker lock and starts it;
4. performs the requested operation;
5. leaves the broker running while browser or agent clients remain connected;
6. optionally self-stops after a configurable idle period.

The socket and lock establish ownership. PID files are diagnostic only and are never trusted as authorization.

### Local files

On macOS and Linux:

```text
~/.local/state/control-room/
├── broker.sock
├── broker.lock
├── control-room.db
└── logs/             # optional, bounded and redacted
```

The parent directory is mode `0700`; the database, lock metadata, and logs are mode `0600`. The socket is reachable only through the private parent directory. Platform-native paths may replace this default where appropriate.

Windows support will use a named pipe and an equivalent private application-data directory.

## Persistence

SQLite runs **inside the Go process**. It is not a separately hosted server.

SQLite is preferred over JSON or a custom append-only log because Control Room needs:

- atomic transitions between plan, approval, and execution states;
- durable recovery after interruption;
- schema migrations;
- indexed event timelines;
- uniqueness and replay constraints;
- bounded cleanup and retention;
- concurrent browser and agent reads.

Recommended configuration:

- a pure-Go SQLite implementation if binary size and performance are acceptable;
- WAL mode;
- foreign keys enabled;
- a short busy timeout;
- explicit transactions for state transitions;
- embedded, versioned migrations;
- startup integrity and schema-version checks;
- database and parent-directory permission verification.

The choice of SQLite implementation is deferred until a small benchmark compares binary size, startup time, cross-compilation, and write behavior. `modernc.org/sqlite` is the initial candidate because it avoids CGO.

### Data model

Initial tables:

```text
sessions
plan_revisions
plan_blocks
actions
annotations
approval_requests
approvals
execution_claims
events
```

`events` is the authoritative audit history. Other tables are transactional projections used for efficient rendering and constraint enforcement.

Representative events:

```text
session.created
plan.published
plan.revised
annotation.created
approval.requested
approval.granted
approval.denied
approval.expired
execution.claimed
action.started
action.progressed
action.completed
action.failed
session.ended
```

Each event records:

- event ID;
- session and plan revision;
- actor (`user`, `agent`, `system`);
- timestamp;
- event type;
- canonical payload hash;
- causal parent event;
- redacted payload.

Secrets, full process environments, and unbounded command output must never be persisted.

## Agent protocol

The agent communicates with the broker through a Unix domain socket. Browsers cannot access this transport, removing DNS rebinding, CSRF, and browser-origin concerns from the agent control channel.

The protocol is:

- length-delimited or newline-delimited JSON for the MVP;
- strictly schema validated;
- explicitly versioned;
- bounded by message and field sizes;
- idempotent for state-changing requests;
- authenticated by OS permissions plus a broker-generated control secret;
- designed so a future transport can preserve the same message semantics.

The control secret is stored mode `0600` or in the OS keychain. On supported systems, peer UID checks should provide an additional layer.

### Example plan

```json
{
  "protocol_version": 1,
  "session_id": "random-256-bit-id",
  "revision": 4,
  "goal": "Add approval-bound agent execution",
  "summary": "Introduce a trusted local feedback broker",
  "workspace": {
    "id": "broker-issued-workspace-id",
    "display_name": "devtools"
  },
  "blocks": [
    {
      "id": "architecture",
      "kind": "markdown",
      "content": "The broker separates review from execution."
    }
  ],
  "actions": [
    {
      "id": "action-1",
      "kind": "write_patch",
      "title": "Add event-store schema",
      "targets": ["workspace://migrations"],
      "risk": "workspace_write"
    },
    {
      "id": "action-2",
      "kind": "run_command",
      "title": "Run the test suite",
      "program": "go",
      "args": ["test", "./..."],
      "cwd": "workspace://root",
      "risk": "local_process"
    }
  ]
}
```

The browser never nominates arbitrary filesystem paths. The broker registers a workspace root through the agent interface and exposes opaque workspace references to the GUI.

## Approval model

Approval is a durable, broker-enforced capability—not a frontend flag or conversational convention.

An approval binds to:

- session ID;
- exact plan revision;
- canonical action definitions;
- selected action IDs;
- workspace identity;
- permission envelope;
- expiration;
- maximum claim count.

Conceptually:

```text
approval_digest = SHA-256(
  protocol version
  + session ID
  + canonical plan revision
  + canonical selected actions
  + workspace identity
  + permission envelope
  + expiration
  + maximum claims
)
```

Changing an action, argument, workspace, permission, or relevant plan field produces a different digest and invalidates the approval.

Example approval:

```json
{
  "session_id": "random-256-bit-id",
  "plan_revision": 4,
  "digest": "sha256:...",
  "allowed_action_ids": ["action-1", "action-2"],
  "expires_at": "2026-08-07T18:00:00Z",
  "max_claims": 1
}
```

The agent must atomically claim an approval before execution. Claims are single-use by default. Replayed, expired, superseded, or mismatched claims fail closed and create audit events.

### Initial permission classes

| Permission class | Default policy |
|---|---|
| Read registered workspace files | Allowed when included in the published plan |
| Run tests or static analysis | Approval required in the MVP |
| Modify workspace files | Approval required |
| Create a local commit | Approval required or later user-configurable |
| Network access | Denied unless explicitly approved |
| Read secrets or credentials | Denied |
| Push, merge, publish, or external comment | Separate explicit approval |
| Read infrastructure state | Separate scoped approval |
| Modify infrastructure or production | Separate explicit approval, never inherited |
| Destructive filesystem operations | Separate explicit approval |

Control Room expresses and verifies these capabilities but does not enforce an agent's operating-system sandbox. The executing agent remains responsible for honoring them. A later runner may enforce them independently, but that is outside the MVP.

## Browser security

The browser interface is a narrow presentation and decision surface.

Requirements:

1. Bind only to `127.0.0.1` on an OS-assigned random port.
2. Do not support wildcard, LAN, or custom external binds.
3. Strictly validate `Host` against the exact loopback host and active port.
4. Open the browser with a random, one-time bootstrap capability.
5. Exchange the bootstrap capability for an `HttpOnly`, `SameSite=Strict` session cookie.
6. Remove the bootstrap capability from the visible URL immediately.
7. Require an exact same-origin `Origin` header and CSRF token on every mutation.
8. Accept state-changing requests only as `POST`, `PUT`, or `DELETE` with `application/json`.
9. Reject missing, malformed, foreign, and `null` origins on browser mutations.
10. Generate random session IDs and per-session browser capabilities.
11. Set a restrictive Content Security Policy and prohibit remote scripts, styles, frames, and connections.
12. Add request-size, connection, rate, and per-session storage limits.
13. Never expose generic filesystem or process APIs to the browser.

Browser authentication is defense in depth even on loopback. Binding to localhost is routing, not authorization.

### Proposed CSP

The precise policy will evolve with the frontend, but should begin from:

```text
default-src 'none';
script-src 'self';
style-src 'self';
img-src 'self' data:;
font-src 'self';
connect-src 'self';
frame-src 'none';
object-src 'none';
base-uri 'none';
form-action 'self';
frame-ancestors 'none'
```

Inline script and style should be avoided. If required, use per-response nonces rather than `'unsafe-inline'`.

## Execution boundary

The GUI never executes commands.

After approval:

1. the agent receives the approval metadata;
2. the agent atomically claims it;
3. the agent executes using its existing tool and permission system;
4. the agent reports structured progress and results;
5. the broker rejects events that do not correspond to the claimed revision and action IDs.

This separation limits the broker's authority and prevents a browser compromise from becoming direct shell execution.

The MVP cannot guarantee that a malicious or compromised agent obeys an approval. It can make deviations visible and avoid granting additional execution authority. Strong enforcement requires a separately designed sandboxed runner.

## API shape

The exact schemas are deferred, but the protocol should expose these operations:

### Agent-side

- `session.create`
- `session.get`
- `session.end`
- `plan.publish`
- `decision.poll`
- `approval.claim`
- `action.start`
- `action.progress`
- `action.complete`
- `action.fail`

### Browser-side

- `GET /session/:id`
- `GET /api/session/:id`
- `GET /api/session/:id/events`
- `POST /api/session/:id/annotations`
- `POST /api/session/:id/request-changes`
- `POST /api/session/:id/approve`
- `POST /api/session/:id/reject`
- `POST /api/session/:id/end`

The frontend uses Server-Sent Events for plan revisions, presence, decisions, and progress. SSE is preferred over WebSockets initially because communication is predominantly server-to-browser and the simpler protocol reduces attack and lifecycle surface.

## Resource governance

The broker must define explicit bounds for:

- message size;
- plan and block size;
- number of actions per revision;
- sessions retained;
- annotations and events per session;
- concurrent SSE and agent connections;
- command-output bytes retained per action;
- database size and retention period;
- idle broker lifetime.

Limits should fail with typed errors and must not silently truncate approval-relevant data. Display-only progress output may be truncated with an explicit marker and retained hash.

## Privacy and network policy

Default policy:

- no telemetry;
- no update checks;
- no hosted sharing;
- no remote assets;
- no outbound network requests;
- no persistence of secrets or full environments;
- bounded, redacted local logs;
- user-visible session deletion;
- documented state paths and retention behavior.

The Go module and frontend dependencies still create a build-time supply-chain surface. Release artifacts should be reproducible where practical, checksummed, signed, and generated through a pinned build environment.

## Failure and recovery

Required behaviors:

- Agent poll interruption does not lose user decisions.
- Browser refresh restores the current projection from SQLite.
- Broker restart invalidates ephemeral browser bootstrap tokens but preserves sessions and decisions.
- A claimed approval remains claimed after restart and cannot be replayed.
- An action running during disconnect becomes `unknown` until the agent reconciles it; it is never silently marked failed or complete.
- Corrupt or incompatible databases fail closed with actionable diagnostics and no automatic destructive reset.
- A stale plan revision cannot receive a new approval or claim.
- Duplicate idempotency keys return the original result.

## Threat model

### Assets

- plan contents and repository metadata;
- user decisions and approval capabilities;
- action definitions and execution results;
- local file paths shown intentionally in review;
- integrity of the event history;
- availability of the local broker.

### Attackers considered

1. A malicious website open in the user's browser.
2. Malicious or prompt-injected agent output.
3. A compromised agent process running as the same OS user.
4. Another unprivileged OS user.
5. Malformed or oversized protocol clients.
6. Accidental process interruption, partial writes, and stale clients.

### Explicit limitation

A fully compromised same-user process can read user-owned files, inspect processes, and potentially access Control Room's local state. Unix permissions and capabilities prevent browser and cross-user attacks; they do not establish a hard boundary against arbitrary malware already running as the same user.

Control Room still minimizes the capability it introduces: the browser cannot reach the agent socket, and the broker does not possess a command-execution primitive.

## Adversarial test plan

Before the approval flow is considered usable, tests must cover:

- foreign website requests to every browser endpoint;
- DNS-rebinding-style hostile `Host` values;
- missing, malformed, foreign, and `null` origins;
- forged and replayed CSRF tokens;
- stolen, expired, and reused browser bootstrap capabilities;
- guessed or substituted session IDs;
- plan mutation after approval;
- selected-action and permission-envelope mutation;
- approval replay and concurrent claims;
- stale plan and stale browser decisions;
- symlink and workspace-path traversal;
- malformed, recursive, and oversized JSON;
- event, SSE, and connection exhaustion;
- broker restart during approval and execution;
- database corruption and migration failure;
- malicious Markdown, Mermaid, diff, ANSI, and URL content;
- secret-shaped data in logs and action output.

## Delivery plan

### Phase 0 — Protocol and threat model

- finalize actors, assets, and boundaries;
- define protocol schemas and size limits;
- define the state machine and policy matrix;
- specify canonicalization and approval digests;
- write security and recovery tests before implementation.

### Phase 1 — Headless broker

- Go binary and lifecycle;
- Unix socket and control authentication;
- embedded SQLite and migrations;
- event store and projections;
- session creation, plan publishing, polling, and ending;
- CLI-only end-to-end loop.

### Phase 2 — Read-only GUI

- secure browser bootstrap;
- Go SSR shell;
- embedded TypeScript/CSS;
- plan, action, diff, presence, and event views;
- SSE updates;
- no browser mutations yet.

### Phase 3 — Feedback and approval

- semantic annotations;
- request changes, reject, and approve;
- digest-bound approvals;
- expiration and single-use claims;
- stale-revision UX;
- progress and result review.

### Phase 4 — OpenCode adapter

- publish structured plans;
- poll for feedback and decisions;
- claim approvals;
- report action lifecycle events;
- preserve an agent-neutral broker protocol.

### Phase 5 — Hardening and distribution

- adversarial browser and protocol tests;
- quotas and retention;
- signed release artifacts and checksums;
- macOS installation path;
- Linux verification;
- assess Windows named-pipe support.

An execution sandbox or privileged runner requires a separate RFC.

## Alternatives considered

### Mutable JSON state

Rejected. It begins simply but requires custom atomic-write recovery, locking, migrations, indexing, replay constraints, and compaction. Those are database features wearing a fake moustache.

### JSONL event log

Attractive for append-only history, but still requires indexes, projections, compaction, transaction semantics, and corruption recovery. SQLite can retain an append-only logical event model without reimplementing storage machinery.

### bbolt or another embedded key-value store

Viable, but relational constraints and event/projection queries become application code. It offers little advantage for this data model.

### In-memory state

Rejected because approvals and claims must survive broker and browser restarts.

### TypeScript SSR embedded in Go

Rejected for the MVP. It requires embedding a JavaScript runtime and expands the binary and runtime surface. Go SSR plus hydrated TypeScript provides the desired product model without that machinery.

### Electron or Tauri

Deferred. Both can package a polished window but do not solve the underlying protocol or trust boundary. A secure local browser GUI is sufficient initially and easier to inspect. A desktop wrapper can be added later without changing the broker protocol.

### Browser extension and native messaging

Security-strong but operationally heavier. Native messaging would remove the browser HTTP listener, but requires extension distribution and host registration. It remains an option if the loopback browser interface proves difficult to secure or product requirements favor an extension UI.

### Fork Lavish-Axi

Rejected. Control Room shares interaction ideas but differs at foundational boundaries: structured data instead of arbitrary HTML, authenticated capabilities instead of path-derived sessions, Unix-socket agent control, no external sharing, and approval integrity as a core domain model. A clean implementation will be smaller and easier to audit than removing unrelated functionality from a fork.

## Open questions

1. Use `html/template` directly or adopt `templ` for typed components?
2. Does the first agent protocol use newline-delimited JSON or length-prefixed frames?
3. Which pure-Go SQLite driver best balances binary size, reliability, and cross-compilation?
4. Should the broker self-stop after an idle timeout or remain a user-level service?
5. Which plan fields are approval-relevant versus display-only?
6. What is the initial retention period and maximum database size?
7. Should registered workspace roots require a one-time browser confirmation?
8. How should the OpenCode adapter map its existing permission prompts to Control Room without creating duplicate approval fatigue?

## Decision requested

Approve the MVP boundary:

> Build a local, authenticated, agent-neutral plan review broker with structured plans, semantic annotations, revision-bound approvals, durable progress events, and no built-in execution.

On approval, Phase 0 will turn the protocol and approval rules in this RFC into versioned schemas and executable adversarial tests before product implementation begins.
