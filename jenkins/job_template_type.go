package jenkins

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// jobTemplateType is a string type whose values compare equal when their XML is
// semantically equivalent (see templatesEqual). This lets the framework treat a
// job template that Jenkins has reformatted on read (attribute order, whitespace,
// plugin version annotations, the XML declaration) as unchanged, replacing the
// SDKv2 templateDiff DiffSuppressFunc.
type jobTemplateType struct {
	basetypes.StringType
}

var _ basetypes.StringTypable = jobTemplateType{}

func (t jobTemplateType) Equal(o attr.Type) bool {
	other, ok := o.(jobTemplateType)
	if !ok {
		return false
	}
	return t.StringType.Equal(other.StringType)
}

func (t jobTemplateType) String() string {
	return "jenkins.jobTemplateType"
}

func (t jobTemplateType) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return jobTemplateValue{StringValue: in}, nil
}

func (t jobTemplateType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	attrValue, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	sv, ok := attrValue.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type %T", attrValue)
	}
	return jobTemplateValue{StringValue: sv}, nil
}

func (t jobTemplateType) ValueType(_ context.Context) attr.Value {
	return jobTemplateValue{}
}

// jobTemplateValue is the value counterpart of jobTemplateType.
type jobTemplateValue struct {
	basetypes.StringValue
}

var _ basetypes.StringValuableWithSemanticEquals = jobTemplateValue{}

func newJobTemplateValue(s string) jobTemplateValue {
	return jobTemplateValue{StringValue: basetypes.NewStringValue(s)}
}

func (v jobTemplateValue) Type(_ context.Context) attr.Type {
	return jobTemplateType{}
}

func (v jobTemplateValue) Equal(o attr.Value) bool {
	other, ok := o.(jobTemplateValue)
	if !ok {
		return false
	}
	return v.StringValue.Equal(other.StringValue)
}

// StringSemanticEquals reports whether two templates are equivalent XML, so that
// Jenkins reformatting the stored config.xml does not surface as drift. Null or
// unknown values fall back to strict equality.
func (v jobTemplateValue) StringSemanticEquals(_ context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	newVal, ok := newValuable.(jobTemplateValue)
	if !ok {
		return false, diags
	}
	if v.IsNull() || v.IsUnknown() || newVal.IsNull() || newVal.IsUnknown() {
		return v.StringValue.Equal(newVal.StringValue), diags
	}
	return templatesEqual(v.ValueString(), newVal.ValueString()), diags
}

// jobDisabledTemplateModifier suppresses a template diff that is caused only by
// the <disabled> element when the "disabled" attribute manages the job's enabled
// state (the enable/disable API rewrites that element out of band). It mirrors
// the SDKv2 GetOk("disabled") behavior in templateDiff. When "disabled" is unset
// the template's <disabled> value diffs normally.
type jobDisabledTemplateModifier struct{}

func jobDisabledTemplatePlanModifier() planmodifier.String {
	return jobDisabledTemplateModifier{}
}

func (m jobDisabledTemplateModifier) Description(_ context.Context) string {
	return "Ignores the <disabled> element in the template when the disabled attribute is managed."
}

func (m jobDisabledTemplateModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m jobDisabledTemplateModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	// Only relevant on update, where a prior state and a known plan both exist.
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}

	var disabled types.Bool
	if diags := req.Plan.GetAttribute(ctx, path.Root("disabled"), &disabled); diags.HasError() {
		return
	}
	if disabled.IsNull() || disabled.IsUnknown() {
		// disabled is unmanaged: leave the template's <disabled> value to diff.
		return
	}

	stateStripped := reDisabledElement.ReplaceAllString(req.StateValue.ValueString(), "")
	planStripped := reDisabledElement.ReplaceAllString(req.PlanValue.ValueString(), "")
	if templatesEqual(stateStripped, planStripped) {
		resp.PlanValue = req.StateValue
	}
}

// jobXMLValidator checks a job template at plan time. Non-well-formed XML (which
// today fails only at apply, as an opaque Jenkins 500) is reported as an error
// with a line number where available; a well-formed document with no root element
// yields a warning. It is the framework counterpart of the former SDKv2
// validateJobXML.
type jobXMLValidator struct{}

func jobXMLValidatorAttr() validator.String {
	return jobXMLValidator{}
}

func (v jobXMLValidator) Description(_ context.Context) string {
	return "Validates that the template is well-formed XML with a root element."
}

func (v jobXMLValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v jobXMLValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	if strings.TrimSpace(s) == "" {
		return
	}

	// Strip the XML declaration before parsing: Jenkins uses
	// `<?xml version='1.1'?>`, which Go's encoding/xml rejects outright. Removing
	// it (the declaration carries no configuration) leaves the newline in place,
	// so reported line numbers stay accurate.
	dec := xml.NewDecoder(strings.NewReader(reXMLDecl.ReplaceAllString(s, "")))
	depth, rootSeen := 0, false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			detail := err.Error()
			if se, ok := err.(*xml.SyntaxError); ok {
				detail = fmt.Sprintf("line %d: %s", se.Line, se.Msg)
			}
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid job configuration XML", detail)
			return
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
			rootSeen = true
		case xml.EndElement:
			depth--
		}
	}

	if depth != 0 {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid job configuration XML", "unexpected EOF: the document contains an unclosed element")
		return
	}
	if !rootSeen {
		resp.Diagnostics.AddAttributeWarning(
			req.Path,
			"Job configuration has no root XML element",
			"The template is well-formed but contains no XML element; Jenkins expects a job configuration document such as <project> or <flow-definition>.",
		)
	}
}
