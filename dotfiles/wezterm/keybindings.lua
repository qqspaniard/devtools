-- keybindings.lua
-- WezTerm key configuration.
--
-- Philosophy: tmux is the multiplexer. We deliberately do NOT add WezTerm
-- pane/tab keybindings that would compete with tmux. In particular, Ctrl-Space
-- (tmux's prefix) must pass through to the shell/tmux untouched, and it does
-- so with WezTerm's defaults -- WezTerm does not bind Ctrl-Space, so no special
-- handling is required here.
--
-- The only custom keys we add restore a couple of iTerm2 defaults that WezTerm
-- does not ship out of the box. They are deliberately narrow: single keys that
-- emit a byte sequence, competing with nothing (tmux passes keyboard bytes
-- through transparently; only mouse events are captured under `mouse on`).

local wezterm = require 'wezterm'
local act = wezterm.action

local M = {}

-- Apply key configuration onto an existing config table. Mutates and returns
-- `config`. WezTerm's default key table stays fully active; we only append.
function M.apply(config)
  config.keys = config.keys or {}

  -- Shift+Enter -> insert a newline instead of submitting.
  --
  -- At WezTerm's default (xterm) encoding, Shift+Enter and Enter collapse to the
  -- same byte (CR, \r), so a TUI cannot tell them apart and Shift+Enter submits.
  -- Emitting LF (\n) gives multi-line input in TUIs whose editors treat LF as
  -- "insert newline" and CR as "submit" (e.g. opencode -- verified Ctrl-J, which
  -- also sends \n, inserts a newline there). iTerm2 shipped this behavior by
  -- default; this restores it without enabling global CSI-u/kitty encoding,
  -- which the WezTerm docs warn against and which regresses other apps.
  table.insert(config.keys, {
    key = 'Enter',
    mods = 'SHIFT',
    action = act.SendString '\n',
  })

  return config
end

return M
