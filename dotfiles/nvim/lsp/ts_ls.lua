-- LSP configuration for the TypeScript/JavaScript language server.
--
-- Requirements: typescript-language-server must be on your $PATH.
--   npm i -g typescript-language-server typescript

return {
	cmd = { "typescript-language-server", "--stdio" },
	filetypes = { "javascript", "javascriptreact", "typescript", "typescriptreact" },
	root_markers = { "tsconfig.json", "jsconfig.json", "package.json", ".git" },
	init_options = { hostInfo = "neovim" },
}
