# Control Room — Usable Loop (stacked on Phase 0)

This slice turns the Phase 0 protocol/policy foundation into a **dogfoodable
vertical demo**: a user can start a local broker, publish a plan from the CLI,
review it in a loopback browser page, approve/reject/request changes, poll the
durable decision, atomically claim the exact approval, restart the broker, and
observe that decisions and claims remain durable and replay-safe.

It is intentionally thin. See [`RFC.md`](../RFC.md) for the full product spec and
[`docs/phase-0.md`](phase-0.md) for the foundation this builds on.

## What this slice adds

| Package | Role |
|---|---|
| `internal/statedir` | Private state dir/path resolution (0700 dir, 0600 files), `--state-dir`/`CONTROL_ROOM_STATE_DIR` override, RFC default. |
| `internal/store` | Embedded SQLite (`modernc.org/sqlite`, pure Go), versioned migrations, transactional state transitions, append-only events, durable single-use claims. |
| `internal/broker` | Long-lived process; private Unix socket; control-secret auth; peer-UID check (darwin + linux); versioned request/response ops. Executes nothing. |
| `internal/web` | Loopback-only SSR review page; bootstrap→cookie; CSP; Origin+CSRF mutations. |
| `cmd/control-room` | `serve`, `session create/get/end`, `plan publish`, `decision poll`, `approval claim`, `open` (plus preserved `version`, `validate-plan`). |
| `internal/e2e` | Full-loop integration test (real broker + web + store). |

## Decisions made in this slice

### SQLite driver: `modernc.org/sqlite` (pinned), pure Go

The RFC's initial candidate is used as-is: no CGO, cross-compiles cleanly,
macOS-first friendly. Pinned in `go.mod`/`go.sum` (`v1.56.0`). WAL, foreign
keys, a 5s busy timeout, and `synchronous=NORMAL` are set as connection `_pragma`
query parameters so **every** pooled connection gets them (a session PRAGMA on
one connection would not apply to others). Startup runs `PRAGMA integrity_check`
and a schema-version check; both fail closed with no destructive reset, and a
database newer than this build's schema is refused.

### Event history is logically append-only

Application code only ever `INSERT`s into `events`. The projections
(`sessions`, `decisions`, `approvals`, `claims`) are what enforce constraints;
`events` is the audit trail. Fail-closed claim rejections append an
`execution.claim_rejected` event in a separate short transaction that runs
**only after the claim transaction has been finalized** (committed or rolled
back), so the audit write never contends for the write lock the claim path held.
The audit is best-effort with respect to its own failure, but it can no longer
block on the very lock it would need.

### Durable single-use claims

A claim is atomic: `Store.Claim` opens one write transaction, makes the entire
decision, and commits OR rolls back **before any audit write**. Inside that
transaction it compare-and-sets `approvals.claims_consumed`
(`WHERE claims_consumed = <observed>`) and inserts a `claims(approval_id,
claim_seq)` row whose primary key is the durable single-use proof. Concurrent
claims are serialized by SQLite; exactly one wins. Because the counter and the
claim row are both persisted, **a restart cannot make a consumed single-use
approval claimable again**. Superseded (newer revision published), expired,
unknown, and session/digest-mismatched claims all fail closed in that order,
matching the Phase 0 policy sentinels. A losing/ rejected claim returns promptly
(the transaction is already released) and its rejection is audited afterward.

### Broker transport & lifecycle

Length-prefixed JSON framing (Phase 0 codec) over a private Unix socket inside
the 0700 state dir. One request per connection. Clean shutdown on SIGINT/SIGTERM
closes the listener, drains in-flight handlers, and removes the socket. Stale
sockets are handled by **probing** the existing socket and rebinding only if no
live broker answers — PID files are never trusted. A socket-path length guard
fails closed (macOS `sockaddr_un` is ~104 bytes) instead of an opaque EINVAL.

For this slice the broker requires an explicit `serve`; there is no auto-spawn.
That keeps lifecycle and ownership trivial and the usable journey predictable.

### Control authentication + peer UID

A random 256-bit control secret is persisted mode 0600 (O_EXCL create) and
compared in constant time. As defense in depth, the broker checks the connecting
peer's UID and refuses a different OS user before reading any request bytes:
`LOCAL_PEERCRED` on darwin and `SO_PEERCRED` on linux. On other platforms this is
a documented no-op; the 0700 dir + 0600 secret remain the primary controls
there.

### Browser security

The web server binds only `127.0.0.1:0` (OS-assigned port). Every request is
Host-validated against the exact `127.0.0.1:PORT` we bound (anti-rebinding). A
one-time, 60s-expiring bootstrap token is exchanged for an `HttpOnly;
SameSite=Strict` cookie, then the request is redirected to the capability-free
`/session/<id>` URL so the token never lingers in history. The CSP is
`default-src 'none'` with `script-src 'self'` (no inline script, no
`unsafe-inline`, no remote assets) plus `frame-ancestors 'none'`,
`X-Frame-Options: DENY`, `nosniff`, `no-referrer`, `no-store`.

Mutations (`POST /api/decide`) require: POST + `application/json` + an exact
same-origin `Origin` header (missing/`null`/foreign rejected) + a constant-time
CSRF token bound to the cookie + a bounded body. A tiny bundled same-origin
`app.js` submits the decision via `fetch()` — chosen precisely because a plain
HTML form POST may omit the `Origin` header, which would weaken the same-origin
check; a same-origin `fetch` always sets `Origin`. The RFC's security
requirement wins over no-JS purity, and the script contains no eval, no dynamic
loading, and no remote calls.

### Plan content is rendered as escaped plaintext

The SSR page uses `html/template`, which context-escapes every interpolated
value, so agent-authored plan content (goal, summary, block content, action
titles) can never inject markup or script. Raw agent HTML is never emitted. The
page shows goal, summary, blocks, actions (with default-all checkboxes),
revision, workspace, and the decision/status plus an event timeline.

### Browser approval uses the Phase 0 digest policy

Approving builds a digest-bound approval via `policy.BuildApproval` over the
loaded plan and the user's selected actions (15-minute TTL, single-use), and
persists it atomically with the decision. The digest binds
revision/actions/workspace/envelope/expiration/max-claims exactly as Phase 0
defines. Stale-revision decisions fail closed (HTTP 409): a browser holding an
older revision cannot decide it once a newer revision is published. The approval
`digest` is surfaced in `decision poll` output so an adapter can claim directly.

The approval's **permission envelope** — the deterministic, sorted set of risk
classes spanned by the selected actions — is now persisted as canonical JSON in
`approvals.permission_envelope` (and exposed on `policy.Approval`). The digest
remains the binding authority; persisting the envelope makes the granted risk
classes queryable for audit (`Store.PermissionEnvelope`) without recomputing
them from the plan.

### Session lifecycle: matrix vs. broker-level operations

The RFC state machine (`policy.Next` / `transitionMatrix`) governs the
review→approve→execute lifecycle and keeps its terminal-state invariants
unchanged. Two real operations do not fit as single matrix edges and are modeled
as **broker-level operations explicitly outside the matrix**, so the store's
behavior and the policy source of truth agree:

- **Administrative end** (`session end`): `policy.CanAdministrativelyEnd` permits
  ending any known, non-terminal session; it lands in `completed` and is
  idempotent for an already-terminal session. It is intentionally not a matrix
  edge (there is no per-state `end` transition).
- **Republish**: `policy.CanRepublish` permits publishing a newer, higher
  revision from any state except `running` and `completed` — including the
  matrix-terminal `rejected`/`expired`, because an agent may address a rejection
  or a lapsed approval with a fresh revision. It lands in
  `policy.RepublishTarget` (`awaiting_approval`), enforces monotonic revision
  numbers, and marks prior approvals superseded. `plan.publish` from `draft`
  still uses the core `Next(draft, publish)` edge; every other legal source uses
  republish. The `plan.published` event records which flow occurred.

These helpers are tested directly, and `policy.Next` explicitly rejects
`republish`/`end` as matrix transitions so the divergence is unambiguous.

## Known limitations (this slice)

- **Same-user isolation is not absolute.** Per the RFC threat model, a fully
  compromised same-user process can read the 0700 state dir, the 0600 secret,
  and the socket. The peer-UID check blocks *other* users, not a malicious
  process running as *you*. We do not claim stronger isolation than this. On
  Unix, `Ensure` additionally refuses to adopt a state directory that is a
  symlink or owned by another uid — defense in depth against redirection/planting,
  not absolute isolation.
- **Peer-UID check covers darwin and linux.** darwin uses `LOCAL_PEERCRED`,
  linux uses `SO_PEERCRED`. Other platforms (e.g. Windows named pipes) are a
  documented hardening follow-up; there the filesystem permissions are the
  control.
- **No auto-spawn / service ownership.** `serve` must be run explicitly.
- **Deliberately excluded:** SSE, semantic annotations, TypeScript, Mermaid,
  rich diff rendering, execution, agent adapters, telemetry, LAN binds, visual
  polish. `cancel`/`fail` granularity still collapses into `completed` (Phase 0
  decision).
- **In-memory browser maps are bounded, not persisted.** Bootstrap tokens and
  browser-session cookies live in memory only (a broker restart invalidates
  them, as the RFC requires). Each map prunes expired entries on issue and is
  size-capped (256), evicting the soonest-expiring entry if the cap is reached.
  This is simple bounded cleanup, not a cache framework.
- **No mutation-idempotency layer in this slice.** Mutations are guarded by
  durable uniqueness constraints only — revision PK (`plan_revisions`), one
  decision per revision (`decisions` PK), `approvals.digest` UNIQUE, and the
  claim PK (`claims`). There is intentionally no idempotency-key table or retry
  de-duplication; nothing in this slice promises it.

## Verification

From `control-room/`:

```sh
gofmt -l .            # expect no output
go vet ./...
go test -race ./...
./scripts/dogfood-smoke.sh
```
