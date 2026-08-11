-- appearance.lua
-- Visual configuration for WezTerm: color scheme, font, window opacity,
-- and (macOS-only) background blur. Kept separate from wezterm.lua so the
-- entry point stays small and the concerns are easy to find.

local wezterm = require 'wezterm'

local M = {}

-- Apply appearance settings onto an existing config table (built by
-- wezterm.config_builder() in the entry point). Mutates and returns `config`.
function M.apply(config)
  -- Color scheme: use WezTerm's built-in Rose Pine Moon. No external theme
  -- files are required, which keeps this config self-contained and portable.
  config.color_scheme = 'rose-pine-moon'

  -- The built-in rose-pine-moon scheme (verified against WezTerm
  -- 20240203-110809-5046fc22) ships with selection_bg == background
  -- (#232136), which makes selected text effectively invisible. Override
  -- ONLY the selection colors with the canonical Rose Pine Moon "highlight"
  -- overlay tone so selections are legible. Everything else is left to the
  -- built-in scheme. If a future WezTerm fixes the built-in selection color,
  -- this block can simply be removed.
  config.colors = {
    selection_fg = '#e0def4', -- rose-pine-moon text (unchanged, kept explicit)
    selection_bg = '#44415a', -- rose-pine-moon "highlight med" overlay
  }

  -- Font. The plan standardizes on Hack Nerd Font at size 15. WezTerm will
  -- fall back to a system monospace font (and its own bundled Nerd Font
  -- symbols) if Hack Nerd Font is not installed; install.sh warns when the
  -- font is missing.
  config.font = wezterm.font('Hack Nerd Font')
  config.font_size = 15.0

  -- Slight transparency. 0.9 keeps text readable while letting the desktop
  -- show through subtly.
  config.window_background_opacity = 0.9

  -- Background blur is only meaningful/reliable on macOS, where it is exposed
  -- as macos_window_background_blur. Detect the platform via target_triple so
  -- this stays inert (and warning-free) on Linux/Windows/WSL.
  if wezterm.target_triple:find('darwin') ~= nil then
    config.macos_window_background_blur = 30
  end

  -- Let tmux be the sole visible multiplexer/status UI. Disabling WezTerm's
  -- tab bar avoids a second, redundant status row when running tmux.
  config.enable_tab_bar = false

  -- Chromeless window: no title bar, no OS border -- just the terminal surface,
  -- edge to edge. "RESIZE" keeps the drag-to-resize handles (grab any edge/
  -- corner), it only removes the title bar + traffic-light buttons. To MOVE a
  -- chromeless window, hold Ctrl+Cmd and drag its body IF the macOS setting
  -- `defaults write -g NSWindowShouldDragOnGesture -bool true` is enabled
  -- (opt-in, not set by this config); otherwise resize via edges and manage
  -- window placement via Mission Control / a window manager.
  config.window_decorations = "RESIZE"

  -- Zero inner padding so content runs edge to edge (no chrome gap around it).
  config.window_padding = { left = 0, right = 0, top = 0, bottom = 0 }

  return config
end

return M
