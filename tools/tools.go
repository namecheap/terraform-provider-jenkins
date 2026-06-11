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
// --provider-name jenkins is required because the repo is named terraform-provider-jenkins-v2;
// without it tfplugindocs derives the name "jenkins-v2", which mismatches the
// "jenkins_*" type prefix used in the provider schema.
//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-dir .. --provider-name jenkins
