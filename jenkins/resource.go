package jenkins

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type (
	// resourceHelper provides assistive snippets of logic to help reduce duplication in
	// each resource definition.
	resourceHelper struct {
		client frameworkClient
	}
)

func newResourceHelper() *resourceHelper {
	return &resourceHelper{}
}

// credentialManager returns a CredentialsManager scoped to the given (unformatted)
// folder. Shared by the credential resources' CRUD methods.
func (r *resourceHelper) credentialManager(folder string) *jenkins.CredentialsManager {
	cm := r.client.Credentials()
	cm.Folder = formatFolderName(folder)
	return cm
}

// credentialManagerForFolder is like credentialManager but also validates that the
// folder exists. On failure it appends an "Invalid Folder" diagnostic and returns nil,
// so callers should `if cm == nil { return }`.
func (r *resourceHelper) credentialManagerForFolder(ctx context.Context, folder string, diags *diag.Diagnostics) *jenkins.CredentialsManager {
	cm := r.credentialManager(folder)
	if err := folderExists(ctx, r.client, cm.Folder); err != nil {
		diags.AddError(
			"Invalid Folder",
			fmt.Sprintf("An invalid folder name %q was specified. ", cm.Folder)+
				"Please report this issue to the provider developers.\n\n"+
				"Error: "+err.Error(),
		)
		return nil
	}
	tflog.Debug(ctx, "validated credential folder", map[string]interface{}{"folder": cm.Folder})
	return cm
}

// deleteCredential deletes the named credential from the given folder/domain,
// appending an "Unable to Delete Resource" diagnostic on failure. Shared by every
// credential resource's Delete method.
func (r *resourceHelper) deleteCredential(ctx context.Context, folder, domain, name string, diags *diag.Diagnostics) {
	cm := r.credentialManager(folder)
	tflog.Debug(ctx, "deleting credential", map[string]interface{}{"folder": cm.Folder, "domain": domain, "name": name})
	if err := cm.Delete(ctx, domain, name); err != nil {
		diags.AddError(
			"Unable to Delete Resource",
			"An unexpected error occurred while deleting the resource. "+
				"Please report this issue to the provider developers.\n\n"+
				"Error: "+err.Error(),
		)
	}
}

// Configure should register the client for the resource.
func (r *resourceHelper) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*jenkinsAdapter)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *jenkinsAdapter, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	r.client = client
}

// ImportState is called when performing import operations of existing resources.
func (r *resourceHelper) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	splitID := strings.Split(req.ID, "/")
	if len(splitID) < 2 {
		resp.Diagnostics.AddError(
			"Unexpected Import Identifier",
			fmt.Sprintf("Expected import identifier with format: \"[<folder>/]<domain>/<name>\". Got: %q", req.ID),
		)
		return
	}

	name := splitID[len(splitID)-1]
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)

	domain := splitID[len(splitID)-2]
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("domain"), domain)...)

	folder := strings.Trim(strings.Join(splitID[0:len(splitID)-2], "/"), "/")
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("folder"), folder)...)

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), generateCredentialID(folder, name))...)
}

// addWriteOnlySecret adds the write-only companion pair for a secret attribute
// named `name` to the given attribute map: `<name>_wo` (write-only, never stored
// in state) and `<name>_wo_version` (a rotation trigger). Terraform cannot detect
// changes to a write-only value — it is absent from state — so bumping the
// version is how a user signals that the secret should be re-sent to Jenkins.
// Requires Terraform >= 1.11.
func addWriteOnlySecret(attrs map[string]schema.Attribute, name, desc string) map[string]schema.Attribute {
	attrs[name+"_wo"] = schema.StringAttribute{
		MarkdownDescription: desc + " Write-only: the value is used only during apply and is **never stored in Terraform state or plan**. " +
			"Requires Terraform >= 1.11, conflicts with `" + name + "`, and must be paired with `" + name + "_wo_version`.",
		Optional:  true,
		Sensitive: true,
		WriteOnly: true,
	}
	attrs[name+"_wo_version"] = schema.StringAttribute{
		MarkdownDescription: "Version identifier for `" + name + "_wo`. Because a write-only value is not stored in state, Terraform cannot detect when it changes; " +
			"change this value (e.g. after rotating the secret) to have Terraform re-send `" + name + "_wo` to Jenkins. Required when `" + name + "_wo` is set.",
		Optional: true,
	}
	return attrs
}

// writeOnlySecretConfigValidators returns the resource-level validators for a
// required secret: exactly one of the plain or write-only attribute must be set,
// and the write-only attribute is always paired with its version trigger.
func writeOnlySecretConfigValidators(name string) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(path.MatchRoot(name), path.MatchRoot(name+"_wo")),
		resourcevalidator.RequiredTogether(path.MatchRoot(name+"_wo"), path.MatchRoot(name+"_wo_version")),
	}
}

// optionalWriteOnlySecretConfigValidators returns the resource-level validators for
// an optional secret that may be omitted entirely (leaving it unmanaged): at most
// one of the plain or write-only attribute may be set, and the write-only attribute
// is always paired with its version trigger.
func optionalWriteOnlySecretConfigValidators(name string) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.Conflicting(path.MatchRoot(name), path.MatchRoot(name+"_wo")),
		resourcevalidator.RequiredTogether(path.MatchRoot(name+"_wo"), path.MatchRoot(name+"_wo_version")),
	}
}

// readWriteOnly fetches a write-only string attribute from the config. Write-only
// values are absent from plan and state, so they must be read from the config
// during Create/Update; they are available there on every apply.
func (r *resourceHelper) readWriteOnly(ctx context.Context, config tfsdk.Config, attr string, diags *diag.Diagnostics) types.String {
	var v types.String
	diags.Append(config.GetAttribute(ctx, path.Root(attr), &v)...)
	return v
}

func (r *resourceHelper) schema(s map[string]schema.Attribute) map[string]schema.Attribute {
	if _, ok := s["id"]; !ok {
		s["id"] = schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The full canonical job path, e.g. `/job/job-name`",
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			},
		}
	}
	if _, ok := s["name"]; !ok {
		s["name"] = schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "The name of the resource being created. This maps to the ID property within Jenkins, and cannot be changed once set.",
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(),
			},
			Validators: []validator.String{
				stringvalidator.RegexMatches(
					regexp.MustCompile(`^[^/]*$`),
					"must not include path characters. Please use the 'folder' property if specifying a job within a subfolder",
				),
			},
		}
	}
	if _, ok := s["folder"]; !ok {
		s["folder"] = schema.StringAttribute{
			MarkdownDescription: "The folder namespace to store the resource in. If not set will default to global Jenkins.",
			Optional:            true,
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(),
			},
		}
	}

	return s
}
func (r *resourceHelper) schemaCredential(s map[string]schema.Attribute) map[string]schema.Attribute {
	// Pull in the base schema
	s = r.schema(s)

	// Add credential-specific attributes
	if _, ok := s["description"]; !ok {
		s["description"] = schema.StringAttribute{
			MarkdownDescription: "A human readable description of the credentials being stored.",
			Optional:            true,
			Computed:            true,
			Default:             stringdefault.StaticString("Managed by Terraform"),
		}
	}
	if _, ok := s["domain"]; !ok {
		s["domain"] = schema.StringAttribute{
			MarkdownDescription: "The domain store to place the credentials into. If not set will default to the global credentials store.",
			Optional:            true,
			Computed:            true,
			Default:             stringdefault.StaticString(defaultCredentialDomain),
			PlanModifiers: []planmodifier.String{
				// In-place updates should be possible, but gojenkins does not support move operations
				stringplanmodifier.RequiresReplace(),
			},
		}
	}
	if _, ok := s["scope"]; !ok {
		s["scope"] = schema.StringAttribute{
			MarkdownDescription: `The visibility of the credentials to Jenkins agents. This must be set to either "GLOBAL" or "SYSTEM". If not set will default to "GLOBAL".`,
			Optional:            true,
			Computed:            true,
			Default:             stringdefault.StaticString("GLOBAL"),
			Validators: []validator.String{
				stringvalidator.OneOf(supportedCredentialScopes...),
			},
		}
	}

	return s
}
