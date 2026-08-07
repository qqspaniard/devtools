# Control Room — Dogfood the usable loop

This walks the full **publish → review → approve → poll → claim → restart →
replay-safe** journey against a local broker, using a temporary state directory
so it never touches your real `~/.local/state/control-room`.

Everything is local-only: a private Unix socket for the agent/CLI and a
loopback-only (`127.0.0.1`) browser page. The broker never executes anything.

## Build

```sh
cd control-room
go build -o /tmp/control-room ./cmd/control-room
```

## 1. Start the broker

In one terminal, start the long-lived broker against a scratch state dir:

```sh
export CR_STATE=/tmp/control-room-dogfood
/tmp/control-room serve --state-dir "$CR_STATE"
```

It prints (on stderr) the control socket, the loopback review base URL, and the
state directory, then blocks:

```
control-room broker listening
  socket: /tmp/control-room-dogfood/broker.sock
  review base: http://127.0.0.1:PORT
  state dir: /tmp/control-room-dogfood
```

Leave it running. Use a second terminal for the steps below (re-export
`CR_STATE` there too). All CLI commands take `--state-dir "$CR_STATE"`.

## 2. Create a session

```sh
/tmp/control-room session create --state-dir "$CR_STATE" \
  --workspace-id ws-devtools --workspace-name devtools
```

The JSON on stdout includes the session `id`. Save it:

```sh
export SID=$(/tmp/control-room session create --state-dir "$CR_STATE" \
  --workspace-id ws-devtools --workspace-name devtools \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
echo "$SID"
```

## 3. Publish a plan

The repo ships `examples/sample-plan.json`. Point it at your session and publish
(the CLI reads a `--file` or stdin):

```sh
python3 -c "import json;p=json.load(open('examples/sample-plan.json'));p['session_id']='$SID';print(json.dumps(p))" \
  | /tmp/control-room plan publish --state-dir "$CR_STATE"
```

The session moves to `awaiting_approval` at `current_revision: 1`.

## 4. Open the review page

```sh
/tmp/control-room open --state-dir "$CR_STATE" --session "$SID"
```

On macOS this launches your browser at a one-time bootstrap URL, which
immediately exchanges the capability for an `HttpOnly; SameSite=Strict` cookie
and redirects to the capability-free `/session/<id>` page. Use `--no-browser` to
just print the URL (for headless/CI use).

In the page you can:

- read the goal, summary, blocks, workspace, and revision;
- uncheck any action you do not approve (all are checked by default);
- **Approve selected**, **Reject**, or **Request changes** (the latter two
  require a bounded reason).

Decisions are submitted via a same-origin `fetch` that always carries an
`Origin` header and the page's CSRF token; the server rejects any missing,
foreign, or `null` origin, and any bad CSRF token.

## 5. Poll for the decision

```sh
/tmp/control-room decision poll --state-dir "$CR_STATE" --session "$SID"
```

Before you decide it prints `{"decided": false}`. After you approve it returns
the durable decision including the approval `digest`:

```json
{
  "decided": true,
  "decision": {
    "kind": "approve",
    "approval_id": "apr-...",
    "digest": "sha256:...",
    ...
  }
}
```

## 6. Claim the approval

An agent atomically claims the exact approval by its digest:

```sh
export DIGEST=$(/tmp/control-room decision poll --state-dir "$CR_STATE" --session "$SID" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["decision"]["digest"])')

/tmp/control-room approval claim --state-dir "$CR_STATE" --session "$SID" --digest "$DIGEST"
```

The first claim wins (`"claim_seq": 1`). A second claim of the same single-use
approval fails closed:

```
control-room: broker error [claims_exhausted]: policy: approval claims exhausted
```

## 7. Prove durability and replay-safety across a restart

Stop the broker (Ctrl-C in terminal 1) and start it again with the **same**
`--state-dir`. Then:

```sh
# The decision survived the restart:
/tmp/control-room decision poll --state-dir "$CR_STATE" --session "$SID"

# The already-claimed approval is still not reusable:
/tmp/control-room approval claim --state-dir "$CR_STATE" --session "$SID" --digest "$DIGEST"
# -> broker error [claims_exhausted]
```

## Notes on scope and limits

This is a deliberately thin vertical slice. It does **not** include SSE,
semantic annotations, TypeScript, Mermaid, rich diff rendering, execution, agent
adapters, telemetry, LAN binds, or visual polish. The broker requires an
explicit `serve` (no auto-spawn). See `RFC.md` for the full design and
`docs/usable-loop.md` for the decisions and known limitations of this slice.

## Automated smoke test

The whole journey (using an HTTP client instead of a real browser) runs as a Go
test:

```sh
go test ./internal/e2e/...
```

and a shell smoke script drives the built binary:

```sh
./scripts/dogfood-smoke.sh
```

State directories created by the smoke script live under `/tmp` and are removed
on completion.
