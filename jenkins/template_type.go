package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// templatePlanModifier is the plan-time equivalent of the SDKv2 templateDiff
// DiffSuppressFunc for the jenkins_job "template" attribute.
//
// Terraform core requires the planned value of a configured attribute to equal
// its configuration (it may not be unknown or some other known value), and the
// framework runs string semantic equality only on read/create/update — never
// during plan. So this modifier suppresses a phantom diff the only way it can:
// when the prior state (Jenkins' re-serialized config, stored on refresh) is
// semantically equal to the configured template, it keeps the prior state
// value, leaving the plan empty. Otherwise it leaves the planned value as the
// configuration, so a genuine change is shown.
//
// This is also what makes upgrading SDKv2 state seamless: the SDKv2 provider
// stored Jenkins' re-serialized config in "template", and this modifier keeps
// that value when it is semantically equal to the practitioner's configuration.
//
// When the managed "disabled" attribute is true, the top-level <disabled>
// element is ignored in the comparison, because the enable/disable API rewrites
// it out of band — matching the SDKv2 d.GetOk("disabled") gate.
type templatePlanModifier struct{}

var _ planmodifier.String = templatePlanModifier{}

func (m templatePlanModifier) Description(context.Context) string {
	return "Suppresses semantically-equivalent Jenkins config.xml template diffs."
}

func (m templatePlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m templatePlanModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	// On create (no prior state) leave the planned value as the configuration.
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() {
		return
	}

	var disabled types.Bool
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("disabled"), &disabled)...)
	if resp.Diagnostics.HasError() {
		return
	}
	strip := !disabled.IsNull() && !disabled.IsUnknown() && disabled.ValueBool()

	cfg := stripDisabledElement(req.ConfigValue.ValueString(), strip)
	state := stripDisabledElement(req.StateValue.ValueString(), strip)
	if templatesEqual(state, cfg) {
		// Semantically equal: keep the prior state value so no diff is shown.
		resp.PlanValue = req.StateValue
	}
	// Otherwise leave the planned value as the configuration (a real change).
}

// stripDisabledElement removes the top-level <disabled> element from a Jenkins
// config.xml template when strip is true, leaving the rest of the document
// untouched so it remains valid XML.
func stripDisabledElement(s string, strip bool) string {
	if strip {
		return reDisabledElement.ReplaceAllString(s, "")
	}
	return s
}

// folderPlanModifier governs the optional "folder" attribute shared by
// jenkins_job and jenkins_folder. It does two things:
//
//   - Treats the SDKv2 empty-string ("") and the framework null as the same
//     "unset folder". The SDKv2 provider always wrote folder = "" for a
//     top-level resource; the framework leaves it null. Without this, upgrading
//     SDKv2 state shows a spurious diff and, because folder is part of the
//     resource identity, a forced replacement.
//   - Forces replacement only when the effective (non-empty) folder actually
//     changes, since folder cannot be updated in place.
//
// Because a plan modifier may only set a planned value that differs from the
// configuration on a Computed attribute, the folder attribute is declared
// Optional+Computed on both resources.
type folderPlanModifier struct{}

var _ planmodifier.String = folderPlanModifier{}

func (m folderPlanModifier) Description(context.Context) string {
	return "Treats an empty-string folder as unset and forces replacement only on a real folder change."
}

func (m folderPlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m folderPlanModifier) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	// A reference to a not-yet-created folder is unknown at plan time; leave it
	// as known-after-apply.
	if req.ConfigValue.IsUnknown() {
		return
	}

	configEmpty := req.ConfigValue.IsNull() || req.ConfigValue.ValueString() == ""
	stateEmpty := req.StateValue.IsNull() || req.StateValue.IsUnknown() || req.StateValue.ValueString() == ""

	// Both unset (null or ""): keep the prior state value so no diff or
	// replacement is produced when upgrading SDKv2 state.
	if configEmpty && stateEmpty {
		resp.PlanValue = req.StateValue
		return
	}

	// A genuine change to the folder identity forces replacement.
	if req.ConfigValue.ValueString() != stateFolderString(req.StateValue) {
		resp.RequiresReplace = true
	}
}

// stateFolderString returns the folder string held in state, treating null and
// unknown as the empty string.
func stateFolderString(v types.String) string {
	if v.IsNull() || v.IsUnknown() {
		return ""
	}
	return v.ValueString()
}
