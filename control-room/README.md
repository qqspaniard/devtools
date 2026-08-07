# Control Room

A local, agent-neutral review broker for the **plan → approve → execute →
review** loop. See [`RFC.md`](RFC.md) for the full product and architecture
specification.

## Status: Phase 0 (foundation)

This tree currently contains only the Phase 0 foundation — the protocol types,
resource limits, session state machine, and approval digest/claim policy — with
comprehensive tests. It does **not** yet contain persistence (SQLite), the agent
socket, HTTP, or the browser GUI. See [`docs/phase-0.md`](docs/phase-0.md) for
the decisions made and the choices explicitly deferred.

## Build & test

Requires Go 1.26+.

```sh
go build ./...
go vet ./...
go test -race ./...
```

## CLI (Phase 0)

```sh
control-room version         # print version and protocol number
control-room validate-plan   # validate a plan revision (JSON) from stdin
control-room help
```

The full subcommand surface (`open`, `session create`, `plan publish`,
`decision poll`, ...) lands with the broker in Phase 1.
