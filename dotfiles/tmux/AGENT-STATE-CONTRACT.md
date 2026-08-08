# The Agent Self-Report Contract

This is the interface between **producers** (agents reporting their state) and
**consumers** (the tmux status bar rendering it). It is deliberately small,
explicit, and aligned with the emerging ecosystem convention so that any agent
or script -- not just this repo's opencode plugin -- can participate.

## The contract

A producer reports state **per tmux pane**, by setting two pane-scoped tmux
user options on the pane it runs in (identified by `$TMUX_PANE`):

| Option | Value | Meaning |
|--------|-------|---------|
| `@agent_state` | `<state>:<epoch>` | the agent's current state + when it was stamped |
| `@agent_title` | free text | the agent's session title (used for naming) |

- **`<state>`** is one of: **`working`**, **`needs-input`**, **`done`**.
  Empty / unset `@agent_state` means "no agent in this pane".
- **`<epoch>`** is Unix seconds (`date +%s`) at the moment of the stamp. It is
  used for staleness (see below). A value with no `:<epoch>` is still accepted
  (treated as never-stale) for forward/backward compatibility.

To report, a producer runs:

```sh
tmux set-option -p -t "$TMUX_PANE" @agent_state "working:$(date +%s)"
tmux set-option -p -t "$TMUX_PANE" @agent_title "fix the status bar"
```

To clear (agent gone):

```sh
tmux set-option -p -t "$TMUX_PANE" @agent_state ""
```

That's the whole contract. Anything that can run `tmux set-option` can be a
producer -- a coding agent's plugin/hook, a wrapper script, a Makefile, etc.

## State vocabulary

`working` / `needs-input` / `done` is the **tmux-side consensus spelling**,
matching accessd/tmux-agent-indicator and tmux-ide's "self-report contract"
(`tmux set-option -p @agent_state "working:<epoch>"`). Other projects use
synonyms (ACP v2 `running`/`requires_action`/`idle`; gentle-agent-state
`working`/`blocked`/`idle`); we use the tmux-native spelling deliberately so a
script written for accessd/tmux-ide interoperates here unchanged.

| Our state | Meaning | ACP v2 | gentle |
|-----------|---------|--------|--------|
| `working` | actively processing | `running` | `working` |
| `needs-input` | blocked on the user (permission/question) | `requires_action` | `blocked` |
| `done` | finished a turn, waiting to be seen | `idle` | `idle` |

## Staleness (crash safety)

Producers report on **transitions**, not on a heartbeat. If a producer dies
mid-`working` without clearing its state (e.g. the process is killed), the pane
option would otherwise be stuck `working` forever. The consumer guards against
this with two independent checks, in priority order:

1. **Process liveness (primary).** If a pane claims `working`/`needs-input` but
   no known agent process is running in it (`#{pane_current_command}` is not a
   recognized agent), the state is stale -> ignored. This is precise and
   time-independent: it catches a crash immediately.
2. **TTL (backup).** A `working`/`needs-input` stamp older than **30 minutes**
   is treated as stale. The generous TTL accommodates long agent turns
   (opencode fires `session.status` only on change, so it does not re-stamp
   during a long turn -- a short TTL would wrongly clear a legitimately busy
   agent).

**`done` never goes stale.** It is the terminal "come look at me" state and
persists until you focus the window (which transitions it) or the agent starts
a new turn. Staleness applies only to the active states.

## Consumers

The consumer in this repo is `scripts/tmux-window-name.sh` (the "brain"), which
rolls per-pane state up to a per-window name + glyph cluster. See that script
and `tmux.conf`'s "Agent status" section. The producer is
`plugins/tmux-agent-state.ts` (an opencode plugin). Either side can be replaced
independently as long as it honors this contract.

## Ecosystem references

- accessd/tmux-agent-indicator -- same `@agent_state` pane-option approach.
- tmux-ide -- documents "the self-report contract",
  `tmux set-option -p @agent_state "working:<epoch>"` + staleness fallback
  (their TTL is 10 min; we use 30 for opencode's transition-only events).
- ACP v2 `state_update` -- the agent<->client protocol analog
  (`running`/`idle`/`requires_action`).
