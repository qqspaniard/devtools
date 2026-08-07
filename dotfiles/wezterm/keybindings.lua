-- keybindings.lua
-- WezTerm key configuration.
--
-- Philosophy: tmux is the multiplexer. We deliberately do NOT add WezTerm
-- pane/tab keybindings that would compete with tmux. In particular, Ctrl-Space
-- (tmux's prefix) must pass through to the shell/tmux untouched, and it does
-- so with WezTerm's defaults -- WezTerm does not bind Ctrl-Space, so no special
-- handling is required here.
--
-- This module is intentionally minimal today. It exists as a stable seam so
-- that machine- or workflow-specific keys can be added later without touching
-- the entry point. Leaving WezTerm's default key table in place preserves
-- familiar behavior (copy/paste, font size, new window, etc.).

local M = {}

-- Apply key configuration onto an existing config table. Mutates and returns
-- `config`. Currently a no-op beyond documenting intent: we keep all WezTerm
-- default keybindings so nothing shadows tmux's Ctrl-Space prefix.
function M.apply(config)
  -- Intentionally left without custom `keys`/`key_tables` so WezTerm's
  -- defaults remain fully active. Add entries here (e.g. `config.keys = {...}`)
  -- only when a concrete conflict or need arises.
  return config
end

return M
