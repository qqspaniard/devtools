-- LSP configuration for rust-analyzer.
--
-- Requirements: rust-analyzer must be on your $PATH.
--   brew install rust-analyzer   (or: rustup component add rust-analyzer)

return {
	cmd = { "rust-analyzer" },
	filetypes = { "rust" },
	root_markers = { "Cargo.toml", "Cargo.lock", ".git" },
}
