// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//go:build generate

package tools

import (
	// document generation
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
)

// Format Terraform code for use in documentation.
//go:generate terraform fmt -recursive ../examples/
// Generate documentation.
// --provider-name jenkins is explicit even though tfplugindocs would auto-derive
// "jenkins" from the binary name (terraform-provider-jenkins). Keeping it prevents
// any future rename from silently changing page titles in the generated docs.
//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-dir .. --provider-name jenkins
