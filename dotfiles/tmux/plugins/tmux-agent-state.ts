/**
 * tmux-agent-state — opencode-side half of the tmux agent-status feature.
 *
 * NOTE ON LOCATION: this file lives in devtools/dotfiles/tmux/plugins/ (with the
 * rest of the tmux agent-status feature: tmux.conf, scripts/, themes/) and is
 * SYMLINKED into ~/.config/opencode/plugins/ by dotfiles/install.sh. It is an
 * opencode plugin but belongs to the tmux feature, so it is co-located with it.
 * Editing the state names here means editing tmux-window-name.sh + glyphs.sh too.
 *
 * IMPORTANT (see orchestrator.ts in this plugins dir): opencode invokes EVERY
 * named export in a plugin file as a plugin factory. So this file exports
 * EXACTLY ONE thing (the plugin). All helpers are non-exported consts captured
 * in the factory closure. A stray helper export crashes plugin load -> TUI crash.
 *
 * What it does, keyed on $TMUX_PANE (the pane this opencode session runs in):
 *   session.status busy   -> tmux set -p @agent_state working
 *   permission.asked      -> tmux set -p @agent_state needs-input
 *   session.status idle   -> tmux set -p @agent_state done
 *   session title change  -> tmux set -p @agent_title "<title>"
 *   session.deleted       -> tmux set -p @agent_state ''   (clears the glyph)
 * After each write it pokes tmux-window-name.sh to recompute the window rollup.
 *
 * No-ops cleanly when not inside tmux ($TMUX_PANE unset).
 */
import type { Plugin } from "@opencode-ai/plugin"

export const TmuxAgentState: Plugin = async ({ $ }) => {
  const pane = process.env.TMUX_PANE
  if (!pane) return {} // not inside tmux; do nothing

  // The brain script lives with the tmux feature, symlinked to ~/.config/tmux
  // by install.sh. Env override wins (tmux does not export its @options to this
  // separate process, so we can't read @agent_script_dir here).
  const scriptDir =
    process.env.TMUX_AGENT_SCRIPT_DIR ||
    `${process.env.HOME}/.config/tmux/scripts`

  const setPaneOpt = async (name: string, value: string) => {
    try {
      await $`tmux set-option -p -t ${pane} ${name} ${value}`.quiet()
    } catch {
      /* tmux gone or pane closed; ignore */
    }
  }
  const pokeRollup = async () => {
    try {
      const win = (
        await $`tmux display-message -p -t ${pane} ${"#{window_id}"}`
          .quiet()
          .text()
      ).trim()
      if (win) await $`${scriptDir}/tmux-window-name.sh ${win}`.quiet()
    } catch {
      /* ignore */
    }
  }

  const setState = async (state: string) => {
    // Per the self-report contract (see AGENT-STATE-CONTRACT.md), active states
    // are stamped "<state>:<epoch>" so the consumer can detect staleness if this
    // process dies mid-turn without clearing. Empty clears the pane.
    const value = state ? `${state}:${Math.floor(Date.now() / 1000)}` : ""
    await setPaneOpt("@agent_state", value)
    await pokeRollup()
  }

  const setTitle = async (title: string) => {
    if (!title) return
    await setPaneOpt("@agent_title", title)
    await pokeRollup()
  }

  return {
    event: async ({ event }) => {
      const e = event as any
      switch (e?.type) {
        case "session.status": {
          const t = e.properties?.status?.type // idle | busy | retry
          if (t === "busy") await setState("working")
          else if (t === "idle") await setState("done")
          break
        }
        case "permission.asked":
          await setState("needs-input")
          break
        case "session.updated":
        case "session.created": {
          const title = e.properties?.info?.title
          if (title) await setTitle(title)
          break
        }
        case "session.deleted":
          await setState("")
          break
      }
    },
  }
}
