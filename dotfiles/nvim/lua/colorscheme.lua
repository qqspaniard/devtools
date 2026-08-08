-- colorscheme.lua -- base colors, driven by the unified theming system.
--
-- termguicolors=true so nvim consumes the same 24-bit palette as wezterm/tmux
-- (dotfiles/themes/). The active theme+mode is rendered to a Lua module at
-- ~/.config/themes/generated/current.nvim.lua by render.sh; it returns a
-- function that sets the base highlight groups. statusline.lua (required after
-- this module in init.lua) reads PmenuSel/Directory/Visual, which the generated
-- scheme defines.
--
-- Fallback: if the generated file is missing or errors, keep truecolor on and
-- leave nvim's defaults in place so startup never breaks.
vim.o.termguicolors = true

local home = os.getenv("HOME") or ""
local theme_file = home .. "/.config/themes/generated/current.nvim.lua"
local ok, apply = pcall(dofile, theme_file)
if ok and type(apply) == "function" then
	pcall(apply)
end
