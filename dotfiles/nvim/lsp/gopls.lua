-- LSP configuration for gopls (Go).
--
-- Requirements: gopls must be on your $PATH.
--   go install golang.org/x/tools/gopls@latest

return {
	cmd = { "gopls" },
	filetypes = { "go", "gomod", "gowork", "gotmpl" },
	root_markers = { "go.work", "go.mod", ".git" },
}
