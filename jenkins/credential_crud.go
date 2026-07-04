package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// This file holds the shared, behavior-preserving Create/Read/Update/Delete
// flow for the credential resources (issue #98). The eight credential kinds are
// ~90% identical, differing only in their model struct, the gojenkins credential
// value they build, and their secret fields. Consolidating the flow removes the
// copy-paste that was the direct root cause of several defects (the Azure
// Update() bug, missing Delete guards, divergent 404 handling).
//
// Type-specific behavior is supplied through credentialSpec[M]; the shared flow
// below is written once. Migration is one credential type at a time (see the
// issue), and every migrated type keeps its exact schema, model, and error
// strings so the change is strictly behavior-preserving.

// Error-message templates, kept identical to the pre-refactor per-resource
// strings so diagnostics (and any tests asserting on them) do not change.
const (
	crudCreateErrSummary = "Unable to Create Resource"
	crudReadErrSummary   = "Unable to Refresh Resource"
	crudUpdateErrSummary = "Unable to Update Resource"

	crudCreateErrDetail = "An unexpected error occurred while creating the resource. " +
		"Please report this issue to the provider developers.\n\n" +
		"Error: "
	crudReadErrDetail = "An unexpected error occurred while parsing the resource read response. " +
		"Please report this issue to the provider developers.\n\n" +
		"Error: "
	crudUpdateErrDetail = "An unexpected error occurred while attempting to update the resource. " +
		"Please retry the operation or report this issue to the provider developers.\n\n" +
		"Error: "
)

// credentialSecretField describes one secret-valued attribute of a credential
// (e.g. "secret", "password", "private_key"). A credential may have several —
// Azure's mutually-exclusive client_secret / certificate_id pair is why the
// shared machinery works with a list from the outset. plainValue and woVersion
// return the model's plain value and its write-only rotation-version field.
type credentialSecretField[M any] struct {
	name       string
	plainValue func(*M) types.String
	woVersion  func(*M) types.String
}

// credentialSpec captures everything type-specific about one credential kind so
// the shared flow can be written once. M is the resource's tfsdk model.
type credentialSpec[M any] struct {
	// identity extracts the folder, domain, and credential name (ID) from the model.
	identity func(*M) (folder, domain, name string)
	// setID writes the computed canonical ID into the model.
	setID func(*M, string)
	// secretFields lists the credential's secret attributes (may be empty).
	secretFields []credentialSecretField[M]
	// build returns a pointer to the gojenkins credential struct to Add/Update.
	// secrets holds the resolved secret values to set, keyed by field name; a
	// field absent from the map is left unset — on Update this leaves Jenkins'
	// stored value untouched.
	build func(m *M, secrets map[string]string) interface{}
	// newAPIValue returns a fresh pointer to the gojenkins credential struct to
	// decode a read into; fromAPI copies its readable (non-secret) fields into m.
	newAPIValue func() interface{}
	fromAPI     func(api interface{}, m *M)
}

// credentialCRUD implements the shared credential resource behavior for model M.
// A concrete resource embeds *credentialCRUD[M] (gaining Create/Read/Update/
// Delete, Configure, and ImportState) and supplies Metadata/Schema itself.
type credentialCRUD[M any] struct {
	*resourceHelper
	spec credentialSpec[M]
}

func newCredentialCRUD[M any](spec credentialSpec[M]) *credentialCRUD[M] {
	return &credentialCRUD[M]{resourceHelper: newResourceHelper(), spec: spec}
}

func (c *credentialCRUD[M]) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m M
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}

	folder, domain, name := c.spec.identity(&m)
	cm := c.credentialManagerForFolder(ctx, folder, &resp.Diagnostics)
	if cm == nil {
		return
	}

	secrets := c.resolveSecretsCreate(ctx, &m, req.Config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := cm.Add(ctx, domain, c.spec.build(&m, secrets)); err != nil {
		resp.Diagnostics.AddError(crudCreateErrSummary, crudCreateErrDetail+err.Error())
		return
	}

	c.spec.setID(&m, generateCredentialID(folder, name))
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (c *credentialCRUD[M]) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m M
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}

	folder, domain, name := c.spec.identity(&m)
	cm := c.credentialManager(folder)

	api := c.spec.newAPIValue()
	if err := cm.GetSingle(ctx, domain, name, api); err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(crudReadErrSummary, crudReadErrDetail+err.Error())
		return
	}

	c.spec.setID(&m, generateCredentialID(folder, name))
	c.spec.fromAPI(api, &m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (c *credentialCRUD[M]) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state M
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	folder, domain, name := c.spec.identity(&plan)
	cm := c.credentialManager(folder)

	secrets := c.resolveSecretsUpdate(ctx, &plan, &state, req.Config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := cm.Update(ctx, domain, name, c.spec.build(&plan, secrets)); err != nil {
		resp.Diagnostics.AddError(crudUpdateErrSummary, crudUpdateErrDetail+err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (c *credentialCRUD[M]) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m M
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}

	folder, domain, name := c.spec.identity(&m)
	c.deleteCredential(ctx, folder, domain, name, &resp.Diagnostics)
}

// resolveSecretsCreate resolves each secret field's value for a create: the
// write-only companion (read from config) takes precedence over the plain value.
// Every declared field is included, matching the pre-refactor behavior of always
// setting the secret on the credential struct at create time.
func (c *credentialCRUD[M]) resolveSecretsCreate(ctx context.Context, m *M, config tfsdk.Config, diags *diag.Diagnostics) map[string]string {
	secrets := make(map[string]string, len(c.spec.secretFields))
	for _, f := range c.spec.secretFields {
		val := f.plainValue(m).ValueString()
		if wo := c.readWriteOnly(ctx, config, f.name+"_wo", diags); !wo.IsNull() {
			val = wo.ValueString()
		}
		secrets[f.name] = val
	}
	return secrets
}

// resolveSecretsUpdate resolves each secret field for an update, sending it only
// when it should change: for a write-only secret when its version trigger
// changed, otherwise when the plain value changed. Fields left out keep Jenkins'
// stored value (also correct under lifecycle.ignore_changes).
func (c *credentialCRUD[M]) resolveSecretsUpdate(ctx context.Context, plan, state *M, config tfsdk.Config, diags *diag.Diagnostics) map[string]string {
	secrets := make(map[string]string)
	for _, f := range c.spec.secretFields {
		if wo := c.readWriteOnly(ctx, config, f.name+"_wo", diags); !wo.IsNull() {
			if !f.woVersion(plan).Equal(f.woVersion(state)) {
				secrets[f.name] = wo.ValueString()
			}
		} else if !f.plainValue(plan).Equal(f.plainValue(state)) {
			secrets[f.name] = f.plainValue(plan).ValueString()
		}
	}
	return secrets
}
