package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

type (
	// dataSourceHelper provides assistive snippets of logic to help reduce duplication in
	// each data source definition.
	dataSourceHelper struct {
		client frameworkClient
	}
)

func newDataSourceHelper() *dataSourceHelper {
	return &dataSourceHelper{}
}

// Configure should register the client for the resource.
func (d *dataSourceHelper) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	var diags diag.Diagnostics
	d.client, diags = clientFromProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
}

func (d *dataSourceHelper) schema(s map[string]schema.Attribute) map[string]schema.Attribute {
	if _, ok := s["id"]; !ok {
		s["id"] = schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The full canonical job path, e.g. `/job/job-name`",
		}
	}
	if _, ok := s["name"]; !ok {
		s["name"] = schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "The name of the resource being read.",
		}
	}
	if _, ok := s["folder"]; !ok {
		s["folder"] = schema.StringAttribute{
			MarkdownDescription: "The folder namespace containing this resource.",
			Optional:            true,
		}
	}

	return s
}

func (d *dataSourceHelper) schemaCredential(s map[string]schema.Attribute) map[string]schema.Attribute {
	// Pull in the base schema
	s = d.schema(s)

	// The folder, domain and credential ID are interpolated into
	// credentials-store URLs (issue #250).
	if folder, ok := s["folder"].(schema.StringAttribute); ok {
		folder.Validators = append(folder.Validators, credentialFolderValidator{})
		s["folder"] = folder
	}
	if name, ok := s["name"].(schema.StringAttribute); ok {
		name.Validators = append(name.Validators, preEncodedValidator{})
		s["name"] = name
	}

	// Add credential-specific attributes
	if _, ok := s["description"]; !ok {
		s["description"] = schema.StringAttribute{
			MarkdownDescription: "A human readable description of the credentials being stored.",
			Computed:            true,
		}
	}
	if _, ok := s["domain"]; !ok {
		s["domain"] = schema.StringAttribute{
			MarkdownDescription: "The domain store containing this resource.",
			Optional:            true,
			Validators:          []validator.String{preEncodedValidator{}},
		}
	}
	if _, ok := s["scope"]; !ok {
		s["scope"] = schema.StringAttribute{
			MarkdownDescription: `The visibility of the credentials to Jenkins agents. This will be either "GLOBAL" or "SYSTEM".`,
			Computed:            true,
		}
	}

	return s
}
