-- wezterm.lua -- entry point for WezTerm configuration.
--
-- This file is modular: it requires sibling modules (appearance, keybindings)
-- that live next to it. To make those `require` calls resolve regardless of
-- how the config was installed, we prepend WezTerm's own config directory to
-- Lua's package.path before requiring the modules.
--
-- install.sh symlinks the ENTIRE wezterm/ directory to ~/.config/wezterm, so
-- wezterm.config_dir points at a directory that contains all three modules and
-- `require 'appearance'` resolves naturally. Prepending it to package.path is
-- belt-and-suspenders: it also makes the modules resolve if this file is
-- evaluated directly (e.g. `wezterm --config-file .../wezterm.lua`) or if only
-- individual files were linked.

local wezterm = require 'wezterm'

-- wezterm.config_dir is provided by WezTerm and points at the directory of the
-- active config. Use it to guarantee sibling modules are on the search path.
local dir = wezterm.config_dir
if dir ~= nil and dir ~= '' then
  package.path = dir .. '/?.lua;' .. package.path
end

local config = wezterm.config_builder()

-- Compose the configuration from focused modules.
config = require('appearance').apply(config)
config = require('keybindings').apply(config)

return config
