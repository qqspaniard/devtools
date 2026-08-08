-- LSP configuration for the YAML language server.
--
-- Requirements: yaml-language-server must be on your $PATH.
--   npm i -g yaml-language-server

return {
	cmd = { "yaml-language-server", "--stdio" },
	filetypes = { "yaml", "yaml.docker-compose" },
	root_markers = { ".git" },
	settings = {
		redhat = { telemetry = { enabled = false } },
		yaml = { keyOrdering = false },
	},
}
