-- LSP configuration for the Bash language server.
--
-- Requirements: bash-language-server must be on your $PATH.
--   npm i -g bash-language-server

return {
	cmd = { "bash-language-server", "start" },
	filetypes = { "sh", "bash" },
	root_markers = { ".git" },
}
