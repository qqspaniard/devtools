-- LSP configuration for the Terraform language server.
--
-- Note: terraform-ls is a Terraform *semantic* server and only understands
-- real Terraform files (.tf / .tfvars). It deliberately does NOT handle
-- generic HCL (Packer, Nomad, .terraform.lock.hcl, ...), so we do not attach
-- it to the `hcl` filetype -- doing so only produces spurious diagnostics.
--
-- Requirements: terraform-ls must be on your $PATH.
--   brew install terraform-ls

return {
	cmd = { "terraform-ls", "serve" },
	filetypes = { "terraform", "terraform-vars" },
	root_markers = { ".terraform", ".git" },
}
