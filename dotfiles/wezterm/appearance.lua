-- appearance.lua
-- Visual configuration for WezTerm: color scheme, font, window opacity,
-- and (macOS-only) background blur. Kept separate from wezterm.lua so the
-- entry point stays small and the concerns are easy to find.

local wezterm = require 'wezterm'

local M = {}

-- Apply appearance settings onto an existing config table (built by
-- wezterm.config_builder() in the entry point). Mutates and returns `config`.
function M.apply(config)
  -- Colors come from the unified theming system (dotfiles/themes/). The active
  -- theme+mode is rendered to a plain Lua colors table at
  -- ~/.config/themes/generated/current.wezterm.lua by render.sh, and selected
  -- via switch-theme. We load that table and apply it as config.colors so the
  -- terminal palette stays in lockstep with tmux and nvim.
  --
  -- Robustness: if the generated file is absent or fails to load (fresh
  -- checkout before install.sh runs render.sh, or a bad edit), fall back to
  -- WezTerm's built-in Rose Pine Moon so the terminal never fails to launch.
  --
  -- (Future: macOS light/dark auto-follow could be wired here via
  -- wezterm.gui.get_appearance() poking switch-theme; kept out for now so this
  -- stays simple and deterministic.)
  local home = os.getenv('HOME') or ''
  local theme_file = home .. '/.config/themes/generated/current.wezterm.lua'
  local ok, colors = pcall(dofile, theme_file)
  if ok and type(colors) == 'table' then
    config.colors = colors
  else
    config.color_scheme = 'rose-pine-moon'
    config.colors = {
      selection_fg = '#e0def4',
      selection_bg = '#44415a',
    }
  end

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

  -- Small inner padding: enough that content isn't jammed against the glass
  -- edge (fully chromeless + zero padding felt cramped), without the chunky
  -- default gap. 20px all sides is a comfortable breathing margin.
  config.window_padding = { left = 20, right = 20, top = 20, bottom = 20 }

  -- Center the terminal cell grid within the window. When the usable pixel size
  -- isn't an exact multiple of the cell size, the leftover sub-cell pixels would
  -- otherwise all pile onto the right/bottom edges, making padding look
  -- asymmetric. Centering splits that remainder evenly across both edges.
  -- (Requires a wezterm nightly >= ~2025-02; not in the 20240203 stable.)
  config.window_content_alignment = { horizontal = "Center", vertical = "Center" }

  return config
end

return M
