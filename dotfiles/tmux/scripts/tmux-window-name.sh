#!/usr/bin/env bash
# tmux-window-name.sh <window_id>
#
# The MECHANISM brain. Two inputs, one output:
#   inputs:  #W (the window name, owned by tmux / edited via `<prefix> ,`)
#            @agent_state + @agent_title per pane (optional; written by the
#            opencode plugin)
#   output:  the window's displayed name (#W) + a glyph overlay (@agent_glyphs)
#
# On any update, for one window, this computes:
#   #W (via rename-window)  the name, per the conditional:
#                    manual rename active (automatic-rename off) -> LEAVE #W alone
#                    0 agents -> inferred from context (treehouse repo#N / proc)
#                    1 agent  -> that agent's session title
#                    2+ agents-> "N sessions"
#   @agent_glyphs  a cluster of state glyphs, ONE PER AGENT (capped), built by
#                    asking the theme's render_glyph for each agent pane's state
#
# window-status-format (see tmux.conf) renders "#{@agent_glyphs}#W", so #W is the
# single source of truth for the name whether the brain or you authored it.
#
# Invoked on agent events (via the plugin's run-shell) and on window focus.

set -uo pipefail

TMUX_BIN="${TMUX_BIN:-tmux}"
WIN="${1:?usage: tmux-window-name.sh <window_id>}"

_here="${BASH_SOURCE[0]%/*}"
_theme="${TMUX_AGENT_THEME_DIR:-${_here}/../themes/spaceflight}"
# shellcheck source=../themes/spaceflight/glyphs.sh
. "${_theme}/glyphs.sh"

# Max glyphs to show (one per agent). Beyond this, show first N then a "+".
GLYPH_CAP="${TMUX_AGENT_GLYPH_CAP:-4}"

# Max display width of the window name (#W), in CHARACTERS. Longer names are
# truncated with a single-width ellipsis. Sized so ~4 window tabs fit a 148-col
# bar at 15pt (window-list budget ~106 cols / 4 tabs - per-tab chrome ~= 20).
NAME_MAX="${TMUX_AGENT_NAME_MAX:-20}"

# Staleness TTL for active states (seconds). Generous, because producers stamp
# on transitions, not on a heartbeat -- a long agent turn must not go stale.
# See AGENT-STATE-CONTRACT.md.
STALE_TTL="${TMUX_AGENT_STALE_TTL:-1800}"   # 30 min

# Recognized agent process names, for the liveness staleness check: a pane
# claiming an active state but not running one of these has a dead producer.
AGENT_PROCS="${TMUX_AGENT_PROCS:-opencode claude codex}"

_now="$(date +%s)"

# is_agent_proc <cmd> -> 0 if cmd is a recognized agent process
is_agent_proc() {
  local c="$1" p
  for p in $AGENT_PROCS; do [ "$c" = "$p" ] && return 0; done
  return 1
}

# truncate_name <name> <max> -> name capped to <max> display CHARACTERS, with a
# single-width ellipsis appended when truncated. Counts CHARACTERS, not bytes,
# so multibyte UTF-8 titles cut cleanly.
#
# Implementation: pure bash. Under a UTF-8 locale, ${#s} and ${s:0:n} operate on
# CHARACTERS, not bytes. We pin `local LC_ALL=en_US.UTF-8` so this holds even
# when the caller's locale is C -- which is common for tmux `run-shell`, and
# verified to work here (2026-08-08). We deliberately avoid awk: macOS's default
# /usr/bin/awk (BSD) counts bytes even under a UTF-8 locale and cuts mid-byte.
# Pure bash also avoids spawning a subprocess on every rename.
truncate_name() {
  local s="$1" max="$2" ell
  local LC_ALL=en_US.UTF-8                   # char-aware ${#s} / ${s:0:n}
  [ "$max" -lt 1 ] 2>/dev/null && { printf '%s' "$s"; return 0; }
  if [ "${#s}" -le "$max" ]; then
    printf '%s' "$s"
    return 0
  fi
  ell="$(printf '\u2026')"                   # single-width ellipsis (bash printf \u)
  printf '%s%s' "${s:0:$((max - 1))}" "$ell"
}

# --- gather this window's panes and their agent state ----------------------
# Newline-safe iteration (unquoted `for` over pane ids mangles on some setups).
# Per the contract, @agent_state is "<state>:<epoch>" (epoch optional). We apply
# staleness: active states (working/needs-input) are dropped if their producer
# looks dead (no agent process in the pane) OR the stamp is older than STALE_TTL.
# 'done' never goes stale.
states=()   # per-agent (post-staleness) state, in pane order
title=""    # the single agent's title (only meaningful when exactly 1 agent)
agent_count=0

while IFS= read -r line; do
  [ -z "$line" ] && continue
  # line = "<raw_state>\t<title>\t<pane_cmd>"
  raw="${line%%$'\t'*}"
  rest="${line#*$'\t'}"
  ti="${rest%%$'\t'*}"
  cmd="${rest##*$'\t'}"
  [ -z "$raw" ] && continue           # pane has no agent -> skip

  # split "<state>:<epoch>" (epoch optional)
  st="${raw%%:*}"
  if [ "$st" != "$raw" ]; then
    stamp="${raw##*:}"
  else
    stamp=""                          # no epoch -> never-stale (compat)
  fi
  [ -z "$st" ] && continue

  # staleness for active states only; 'done' persists untouched.
  if [ "$st" = "working" ] || [ "$st" = "needs-input" ]; then
    # (1) liveness: producer dead if no agent process in the pane
    if ! is_agent_proc "$cmd"; then
      continue
    fi
    # (2) TTL: stamp too old
    if [ -n "$stamp" ] && [ "$((_now - stamp))" -gt "$STALE_TTL" ]; then
      continue
    fi
  fi

  states+=("$st")
  title="$ti"                         # remember last; used only if count==1
  agent_count=$((agent_count + 1))
done < <("$TMUX_BIN" list-panes -t "$WIN" -F '#{@agent_state}	#{@agent_title}	#{pane_current_command}' 2>/dev/null)

# --- build the glyph cluster (one per agent, capped) -----------------------
glyphs=""
shown=0
for st in "${states[@]}"; do
  if [ "$shown" -ge "$GLYPH_CAP" ]; then
    glyphs="${glyphs}+"
    break
  fi
  g="$(render_glyph "$st" 0)"         # frame 0 (static in baseline)
  glyphs="${glyphs}${g}"
  shown=$((shown + 1))
done
"$TMUX_BIN" set -w -t "$WIN" @agent_glyphs "$glyphs"

# --- compute the name per the conditional ----------------------------------
# Distinguish a USER manual rename from the brain's own rename:
#   - `<prefix> ,` sets automatic-rename off and does NOT set @agent_brain_named.
#   - the brain sets the name then marks @agent_brain_named=1.
# `rename-window` itself flips automatic-rename off, so we can't use that flag
# alone to detect a manual rename. Instead: if automatic-rename is off AND the
# brain did not author it, the user owns #W -> leave it.
auto="$("$TMUX_BIN" show-options -wqv -t "$WIN" automatic-rename 2>/dev/null)"
brain_named="$("$TMUX_BIN" show-options -wqv -t "$WIN" @agent_brain_named 2>/dev/null)"
if [ "$auto" = "off" ] && [ "$brain_named" != "1" ]; then
  exit 0   # user manually renamed via `<prefix> ,`; #W is theirs
fi

name=""
if [ "$agent_count" -eq 0 ]; then
  # 0 agents: infer from the active pane's context (treehouse repo#N / process)
  read -r cpath ccmd < <("$TMUX_BIN" display-message -p -t "$WIN" '#{pane_current_path}	#{pane_current_command}' 2>/dev/null | tr '\t' ' ')
  name="$("${_here}/tmux-pane-classify.sh" "$cpath" "$ccmd" 2>/dev/null)"
elif [ "$agent_count" -eq 1 ]; then
  # 1 agent: its session title (fall back to something if title empty)
  name="${title:-agent}"
else
  # 2+ agents
  name="${agent_count} sessions"
fi

# Cap the name so long titles can't blow out the window bar (see NAME_MAX).
# Applies to every naming path uniformly.
name="$(truncate_name "$name" "$NAME_MAX")"

# Write the real window name (#W = single source of truth for display), then
# mark it brain-authored so a later invocation knows this off-state is ours,
# not a user rename. A real `<prefix> ,` never sets this sentinel.
if [ -n "$name" ]; then
  "$TMUX_BIN" rename-window -t "$WIN" "$name"
  "$TMUX_BIN" set -w -t "$WIN" @agent_brain_named 1
fi
