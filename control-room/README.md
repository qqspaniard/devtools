# Control Room

A local, agent-neutral review broker for the **plan → approve → execute →
review** loop. See [`RFC.md`](RFC.md) for the full product and architecture
specification.

## Status: usable vertical slice (stacked on Phase 0)

This tree contains the Phase 0 foundation **plus** a dogfoodable vertical slice:
a local broker (private Unix socket + control-secret auth), an embedded SQLite
store with durable, replay-safe approvals/claims, and a loopback-only browser
review page. A user can publish a plan, review and approve it in the browser,
poll the durable decision, and atomically claim the exact approval — surviving a
broker restart. See [`docs/usable-loop.md`](docs/usable-loop.md) for decisions
and known limitations, [`docs/phase-0.md`](docs/phase-0.md) for the foundation,
and [`DOGFOOD.md`](DOGFOOD.md) for exact run commands.

It is intentionally thin: no SSE, semantic annotations, TypeScript, Mermaid,
rich diffs, execution, agent adapters, telemetry, or LAN binds.

## Build & test

Requires Go 1.26+.

```sh
go build ./...
go vet ./...
go test -race ./...
./scripts/dogfood-smoke.sh   # end-to-end smoke against the built binary
```

## CLI

```sh
control-room version                       # print version and protocol number
control-room validate-plan                 # validate a plan (JSON) from stdin
control-room serve --state-dir DIR         # run the local broker (blocks)

control-room session create --workspace-id ID [--workspace-name NAME]
control-room session get    --session ID
control-room session end    --session ID
control-room plan publish   [--file plan.json]     # reads stdin if no --file
control-room decision poll  --session ID
control-room approval claim --session ID --digest sha256:...
control-room open           --session ID [--no-browser]
```

Machine-readable JSON is written to stdout for adapter-consumable operations;
human diagnostics go to stderr.
