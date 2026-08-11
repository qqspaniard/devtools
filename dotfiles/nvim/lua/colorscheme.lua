-- colorscheme.lua -- base colors, driven by the theming system.
--
-- termguicolors=true so nvim consumes the same 24-bit palette as wezterm/tmux
-- (dotfiles/themes/). render.sh generates native Neovim colorschemes into
-- ~/.config/nvim/colors/<name>-<mode>.lua (one per palette+mode); each sets
-- vim.g.colors_name and the base highlight groups (statusline.lua, required
-- after this module in init.lua, reads PmenuSel/Directory/Visual, which the
-- generated scheme defines).
--
-- Switch themes by changing the name below (e.g. 'nebula-light',
-- 'rosepine-dark', or a locally-generated one). The committed default is
-- nebula-dark. The colorscheme call is guarded with pcall so a missing scheme
-- (fresh checkout before install.sh runs render.sh) never breaks startup: on
-- failure we keep truecolor on and leave nvim's defaults in place. Run
-- `sh dotfiles/themes/render.sh --all` to generate the colorschemes.
vim.o.termguicolors = true

pcall(vim.cmd.colorscheme, "nebula-dark")
