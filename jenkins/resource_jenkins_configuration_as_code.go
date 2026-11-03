package jenkins

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type configurationAsCodeResourceModel struct {
	ID               types.String `tfsdk:"id"`
	Section          types.String `tfsdk:"section"`
	YAML             types.String `tfsdk:"yaml"`
	SecretsWo        types.Map    `tfsdk:"secrets_wo"`
	SecretsWoVersion types.String `tfsdk:"secrets_wo_version"`
}

type configurationAsCodeResource struct {
	*resourceHelper
}

var _ resource.ResourceWithConfigure = &configurationAsCodeResource{}
var _ resource.ResourceWithImportState = &configurationAsCodeResource{}
var _ resource.ResourceWithConfigValidators = &configurationAsCodeResource{}
var _ resource.ResourceWithValidateConfig = &configurationAsCodeResource{}
var _ resource.ResourceWithModifyPlan = &configurationAsCodeResource{}

func newConfigurationAsCodeResource() resource.Resource {
	return &configurationAsCodeResource{
		resourceHelper: newResourceHelper(),
	}
}

func (r *configurationAsCodeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_configuration_as_code"
}

func (r *configurationAsCodeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages one top-level section of the controller configuration through the ` + "`configuration-as-code`" + ` (JCasC) plugin.

Each instance owns a single top-level JCasC section (` + "`jenkins`, `security`, `unclassified`, `tool`, ..." + `) named by ` + "`section`" + `, and applies the ` + "`yaml`" + ` subtree for it. See the [design notes](https://github.com/namecheap/terraform-provider-jenkins/blob/main/docs/design/casc.md) for the full model.

Key behaviours:

- **Merge, not replace.** Applying a section merges the declared keys into the running configuration; keys present on the controller but not in ` + "`yaml`" + ` are left untouched. Drift is detected as a *subset*: only keys you declare are compared.
- **Secrets.** Use JCasC ` + "`${VAR}`" + ` interpolation for secret values; they are resolved by the controller at apply time and never stored in state or compared. A ` + "`${name}`" + ` naming a key of the write-only ` + "`secrets_wo`" + ` map is replaced by the provider instead, just before the configuration is sent, so a secret held by Terraform needs no secret source on the controller.
- **Drift.** When a declared key drifts, state keeps only the declared keys: ` + "`${VAR}`" + ` placeholders stay as written and drifted plain values show the controller's value. A value that could carry a resolved secret or unmanaged keys is shown as ` + "`" + cascDriftMarker + "`" + ` instead.
- **Import.** Import records only ` + "`section`" + `; ` + "`yaml`" + ` is not read from the controller, so the first apply after import re-applies the configured document.
- **Delete is best-effort.** JCasC cannot "unset" configuration, so destroying this resource only stops managing the section — the applied values remain on the controller.

Requires the ` + "`configuration-as-code`" + ` plugin and an account with ` + "`Overall/Administer`" + `.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The managed section name (same as `section`).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"section": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The top-level JCasC configuration section this resource manages, e.g. `jenkins`, `security`, `unclassified`, or `tool`. Changing it forces replacement.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					stringvalidator.RegexMatches(reCASCSection, "must be a bare top-level key (letters, digits, '_' or '-')"),
				},
			},
			"yaml": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The YAML subtree for `section` (the content *under* the section key, not including the key itself). May reference secrets with JCasC `${VAR}` interpolation, or `${name}` for a key of `secrets_wo`.",
			},
			"secrets_wo": schema.MapAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Sensitive:   true,
				WriteOnly:   true,
				MarkdownDescription: "Secret values substituted into `yaml` by the provider, keyed by name: every `${name}` expression in a string of `yaml` is replaced by the value of `name` just before the section is sent to the controller. " +
					"Write-only: the values are **never stored in Terraform state or plan**, and `yaml` keeps the `${name}` form, which drift detection compares as a placeholder when it is the whole value. " +
					"Embedded in a longer string (`https://user:${name}@host/`) it never equals what the controller exports, so the section plans a change on every run. " +
					"Only exactly `${name}` is replaced, also nested as in `${decodeBase64:${name}}`; any other `${...}`, and `^${name}` (JCasC's escape for a literal), is left for JCasC. A `${` inside a value is escaped only when a closing `}` follows in the assembled string; an unclosed `${` stays literal. " +
					"A value nested in another expression must not hold `$`, `{`, `}`, `^` or `:-`, and a value must not end with `^` right before another expression: JCasC syntax has no escape for either, so the apply fails. " +
					"Nested transforming lookups can resolve `${...}` again in their output, including controller environment variables, system properties and file lookups. Decode in Terraform instead: `secrets_wo = { x = base64decode(var.b64) }` with a top-level `${x}` in YAML. Controller error text is omitted whenever write-only secrets are sent. " +
					"Every key must be referenced in `yaml`. Requires Terraform >= 1.11 and must be paired with `secrets_wo_version`.",
				Validators: []validator.Map{
					mapvalidator.NoNullValues(),
					mapvalidator.KeysAre(stringvalidator.RegexMatches(reCASCSecretName, "must start with a letter or '_' and hold only letters, digits, '_', '.' or '-'")),
				},
			},
			"secrets_wo_version": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Version identifier for `secrets_wo`. Because write-only values are not stored in state, Terraform cannot detect when they change; change this value (e.g. after rotating a secret) to have Terraform re-apply the section with the new values. Current values are sent on every section apply, including YAML changes; the version triggers an apply when only secret values change. Removing the pair while interpolation remains warns that JCasC may resolve it to an empty value. Required when `secrets_wo` is set.",
			},
		},
	}
}

// ConfigValidators pairs secrets_wo with its version trigger.
func (r *configurationAsCodeResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.RequiredTogether(path.MatchRoot("secrets_wo"), path.MatchRoot("secrets_wo_version")),
	}
}

// ValidateConfig rejects a secrets_wo key that yaml never references, once
// both are known.
func (r *configurationAsCodeResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data configurationAsCodeResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if data.YAML.IsUnknown() || data.YAML.IsNull() || data.SecretsWo.IsUnknown() || data.SecretsWo.IsNull() {
		return
	}
	checkSecretsReferenced(data.YAML.ValueString(), data.SecretsWo.Elements(), &resp.Diagnostics)
}

// checkSecretsReferenced adds an error naming the secrets_wo keys that no
// ${name} expression in yaml refers to.
func checkSecretsReferenced[V any](yaml string, secrets map[string]V, diags *diag.Diagnostics) {
	unused, err := unreferencedSecrets(yaml, secrets)
	if err != nil {
		diags.AddAttributeError(path.Root("yaml"), "Invalid YAML", err.Error())
		return
	}
	if len(unused) > 0 {
		diags.AddAttributeError(path.Root("secrets_wo"), "Unreferenced Secret",
			"No ${name} expression in yaml refers to: "+strings.Join(unused, ", ")+". Their values would never be applied.")
	}
}

func (r *configurationAsCodeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "configurationAsCodeResource.Create")
	var data configurationAsCodeResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !r.apply(ctx, req.Config, &data, &resp.Diagnostics) {
		return
	}

	data.ID = data.Section
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *configurationAsCodeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "configurationAsCodeResource.Read")
	var data configurationAsCodeResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	exported, err := r.client.ExportCASC(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Refresh Resource", "Could not export JCasC configuration.\n\nError: "+err.Error())
		return
	}

	// A null yaml comes from ImportState, which has no declared document to
	// compare against; the next plan applies the configured one.
	if !data.YAML.IsNull() {
		section := data.Section.ValueString()
		inSync, err := cascInSync(data.YAML.ValueString(), exported, section)
		if err != nil {
			resp.Diagnostics.AddError("Unable to Compare Configuration", err.Error())
			return
		}

		if !inSync {
			// The controller no longer matches the declared subtree. Store a
			// view of the drifted declared keys so a diff (and a reconciling
			// apply) is produced without copying resolved ${...} values or
			// unmanaged keys from the export into state. If the section is gone
			// entirely, drop the resource so it is recreated.
			view, found, err := cascDriftView(data.YAML.ValueString(), exported, section)
			if err != nil {
				resp.Diagnostics.AddError("Unable to Read Section", err.Error())
				return
			}
			if !found {
				resp.State.RemoveResource(ctx)
				return
			}
			data.YAML = types.StringValue(view)
		}
	}

	data.ID = data.Section
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *configurationAsCodeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "configurationAsCodeResource.Update")
	var data configurationAsCodeResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !r.apply(ctx, req.Config, &data, &resp.Diagnostics) {
		return
	}

	data.ID = data.Section
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Delete only stops managing the section: JCasC has no operation to remove
// previously-applied configuration, so the values remain on the controller.
func (r *configurationAsCodeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Debug(ctx, "configurationAsCodeResource.Delete (no-op)")
	var data configurationAsCodeResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.AddWarning(
		"Configuration Not Removed",
		"JCasC cannot un-apply configuration, so the '"+data.Section.ValueString()+"' section previously applied by this "+
			"resource remains on the controller. Remove or override it manually if it is no longer wanted.",
	)
}

// ImportState imports by section name after checking the controller has that
// section. yaml is left null: the export holds the values JCasC resolved for
// ${...} placeholders and every unmanaged key, and without a declared document
// there is nothing to restrict it to. The first plan after import therefore
// shows the configured yaml as an in-place update, which re-applies it.
func (r *configurationAsCodeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	section := req.ID
	exported, err := r.client.ExportCASC(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Import Resource", "Could not export JCasC configuration.\n\nError: "+err.Error())
		return
	}
	_, found, err := exportedSection(exported, section)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Import Resource", err.Error())
		return
	}
	if !found {
		resp.Diagnostics.AddError("Section Not Found", "The controller has no JCasC section named "+section+".")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("section"), section)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), section)...)
}

// apply wraps the declared subtree in its section key, substitutes the
// write-only secrets read from config, and POSTs it to JCasC. Write-only values
// are absent from plan and state, so they come from config, where they are
// available on every apply. Returns false (with a diagnostic) on failure.
func (r *configurationAsCodeResource) apply(ctx context.Context, config tfsdk.Config, data *configurationAsCodeResourceModel, diags *diag.Diagnostics) bool {
	var secretsWo types.Map
	diags.Append(config.GetAttribute(ctx, path.Root("secrets_wo"), &secretsWo)...)
	if diags.HasError() {
		return false
	}
	var secrets map[string]string
	if !secretsWo.IsNull() {
		diags.Append(secretsWo.ElementsAs(ctx, &secrets, false)...)
		if diags.HasError() {
			return false
		}
	}
	// ValidateConfig skips this check while yaml or secrets_wo is unknown.
	if len(secrets) > 0 {
		checkSecretsReferenced(data.YAML.ValueString(), secrets, diags)
		if diags.HasError() {
			return false
		}
	}

	doc, err := wrapSection(data.Section.ValueString(), data.YAML.ValueString(), secrets)
	if errors.Is(err, errCASCSecretValue) {
		diags.AddAttributeError(path.Root("secrets_wo"), "Invalid Secret Value", err.Error())
		return false
	}
	if err != nil {
		diags.AddAttributeError(path.Root("yaml"), "Invalid YAML", err.Error())
		return false
	}
	if err := r.client.ApplyCASC(ctx, doc); err != nil {
		detail := "JCasC rejected the configuration for section " + data.Section.ValueString() + ".\n\nError: " + err.Error()
		if len(secrets) > 0 {
			// The controller may quote JSON/HTML-escaped or transformed values.
			// Even masking a short value in fixed text discloses information.
			// Keep only the typed HTTP status; never inspect or echo the body.
			status := ""
			var applyErr *cascApplyError
			if errors.As(err, &applyErr) {
				status = fmt.Sprintf(" (HTTP %d)", applyErr.status)
			}
			detail = "JCasC rejected the configuration" + status + ". Controller error details are omitted because this request contains write-only secrets."
		}
		diags.AddError("Unable to Apply Configuration", detail)
		return false
	}
	return true
}

// ModifyPlan warns when removing the write-only source leaves interpolation
// behind. Secret keys are not stored, so we cannot determine whether an
// expression will resolve from another source or become empty on the controller.
func (r *configurationAsCodeResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var prior, planned configurationAsCodeResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &planned)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if prior.SecretsWoVersion.IsNull() || prior.SecretsWoVersion.IsUnknown() || !planned.SecretsWoVersion.IsNull() {
		return
	}
	if !planned.YAML.IsUnknown() {
		tree, err := parseYAML(planned.YAML.ValueString())
		if err != nil || !hasInterpolation(tree) {
			return
		}
	}
	resp.Diagnostics.AddAttributeWarning(path.Root("secrets_wo_version"), "Write-only Secret Source Removed",
		"The write-only secret source is being removed while YAML still contains interpolation or is not yet known. JCasC will resolve remaining expressions from its own secret sources and may replace missing values with empty strings. Remove the references or configure a controller secret source before applying.")
}
