package jenkins

import (
	"context"
	"encoding/xml"
	"fmt"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
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
// (e.g. "secret", "password", "privatekey", "passphrase"). A credential may
// have several — SSH's privatekey+passphrase and Azure's mutually-exclusive
// client_secret/certificate_id pair are why the shared machinery works with a
// list from the outset.
//
//   - hasWriteOnly: a "<name>_wo" / "<name>_wo_version" companion pair exists
//     (added via addWriteOnlySecret). When false, no _wo attribute is read.
//   - element: the field's XML element in the credential's config.xml, set only
//     for an optional secret that a configuration may leave unmanaged (plain and
//     write-only both unset). Update checks that Jenkins holds no value for such
//     a secret before sending a payload without it (see resolveSecretsUpdate).
//     When empty, an unset value is sent as empty: the secret is required, has a
//     schema default, or is replaced by another attribute (Azure's
//     certificate_id).
//
// plainValue returns the model's plain value.
type credentialSecretField[M any] struct {
	name         string
	hasWriteOnly bool
	element      string
	plainValue   func(*M) types.String
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
	// field absent from the map is left unset. Jenkins stores an unset secret as
	// empty, so on Update a field is absent only when Terraform does not manage it
	// and Jenkins was found to hold no value for it.
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
	pathDomain, _, err := escapeCredentialPath(domain, name)
	if err != nil {
		resp.Diagnostics.AddError(crudCreateErrSummary, crudCreateErrDetail+err.Error())
		return
	}
	cm := c.credentialManagerForFolder(ctx, folder, &resp.Diagnostics)
	if cm == nil {
		return
	}

	secrets := c.resolveSecretsCreate(ctx, &m, req.Config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := cm.Add(ctx, pathDomain, c.spec.build(&m, secrets)); err != nil {
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
	cm := c.credentialManager(folder, &resp.Diagnostics)
	if cm == nil {
		return
	}

	api := c.spec.newAPIValue()
	pathDomain, pathName, err := escapeCredentialPath(domain, name)
	if err == nil {
		err = cm.GetSingle(ctx, pathDomain, pathName, api)
	}
	if err != nil {
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
	var plan M
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	folder, domain, name := c.spec.identity(&plan)
	cm := c.credentialManager(folder, &resp.Diagnostics)
	if cm == nil {
		return
	}

	secrets, unmanaged := c.resolveSecretsUpdate(ctx, &plan, req.Config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	pathDomain, pathName, err := escapeCredentialPath(domain, name)
	if err == nil && len(unmanaged) > 0 {
		checkUnmanagedSecrets(ctx, cm, pathDomain, pathName, unmanaged, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if err == nil {
		err = cm.Update(ctx, pathDomain, pathName, c.spec.build(&plan, secrets))
	}
	if err != nil {
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

// resolvePlainOrWo returns a field's effective value: the write-only companion
// (read from config) when the field has one and it is set, otherwise the plain
// value from the model.
func (c *credentialCRUD[M]) resolvePlainOrWo(ctx context.Context, m *M, config tfsdk.Config, f credentialSecretField[M], diags *diag.Diagnostics) string {
	if f.hasWriteOnly {
		if wo := c.readWriteOnly(ctx, config, f.name+"_wo", diags); !wo.IsNull() {
			return wo.ValueString()
		}
	}
	return f.plainValue(m).ValueString()
}

// resolveSecretsCreate resolves every secret field for a create: the write-only
// companion takes precedence over the plain value. All declared fields are
// included, matching the pre-refactor behavior of setting the secret on the
// credential struct at create time.
func (c *credentialCRUD[M]) resolveSecretsCreate(ctx context.Context, m *M, config tfsdk.Config, diags *diag.Diagnostics) map[string]string {
	secrets := make(map[string]string, len(c.spec.secretFields))
	for _, f := range c.spec.secretFields {
		secrets[f.name] = c.resolvePlainOrWo(ctx, m, config, f, diags)
	}
	return secrets
}

// resolveSecretsUpdate resolves the secret fields for an update. Jenkins'
// config.xml update replaces the whole credential, and a secret element that is
// left out, empty or redacted is stored as empty: there is no way to keep a
// stored secret. So every secret Terraform knows is re-sent on every update —
// the write-only companion from config when set (write-only values are present
// in config on every apply), otherwise the plain value from the plan. A
// "<name>_wo_version" change only forces the update; it does not decide what is
// sent. Under lifecycle.ignore_changes on a plain secret the plan holds the
// value from state, so that value is re-sent.
//
// An optional field (element set) that Terraform does not manage — plain and
// write-only both null — is returned in unmanaged instead of being resolved;
// Update must check that Jenkins holds no value for it before sending.
func (c *credentialCRUD[M]) resolveSecretsUpdate(ctx context.Context, plan *M, config tfsdk.Config, diags *diag.Diagnostics) (map[string]string, []unmanagedSecret) {
	secrets := make(map[string]string, len(c.spec.secretFields))
	var unmanaged []unmanagedSecret
	for _, f := range c.spec.secretFields {
		if f.element != "" && f.plainValue(plan).IsNull() &&
			(!f.hasWriteOnly || c.readWriteOnly(ctx, config, f.name+"_wo", diags).IsNull()) {
			unmanaged = append(unmanaged, unmanagedSecret{attr: f.name, element: f.element, hasWriteOnly: f.hasWriteOnly})
			continue
		}
		secrets[f.name] = c.resolvePlainOrWo(ctx, plan, config, f, diags)
	}
	return secrets, unmanaged
}

// unmanagedSecret is an optional secret attribute that the configuration leaves
// unset, so an update has no value to send for it.
type unmanagedSecret struct {
	attr         string // Terraform attribute name, e.g. "password"
	element      string // XML element in the credential's config.xml
	hasWriteOnly bool   // whether "<attr>_wo" exists
}

// checkUnmanagedSecrets refuses an update that would erase a secret Terraform
// does not manage. It reads the credential's config.xml, where Jenkins writes
// each secret it holds as <element><secret-redacted/></element> and omits one it
// does not hold, and adds an error for every unmanaged secret that is present.
// Jenkins does not reveal whether a stored value is empty, so a present element
// always counts as a value. pathDomain and pathName must already be escaped
// (escapeCredentialPath).
func checkUnmanagedSecrets(ctx context.Context, cm *jenkins.CredentialsManager, pathDomain, pathName string, unmanaged []unmanagedSecret, diags *diag.Diagnostics) {
	var stored credentialXMLElements
	if err := cm.GetSingle(ctx, pathDomain, pathName, &stored); err != nil {
		diags.AddError(crudUpdateErrSummary, crudUpdateErrDetail+"reading the stored credential: "+err.Error())
		return
	}
	for _, u := range unmanaged {
		if !stored.names[u.element] {
			continue
		}
		manage := fmt.Sprintf("`%s`", u.attr)
		if u.hasWriteOnly {
			manage = fmt.Sprintf("`%s`, or `%s_wo` with `%s_wo_version`", u.attr, u.attr, u.attr)
		}
		diags.AddAttributeError(path.Root(u.attr), "Update Would Erase Credential Secret",
			fmt.Sprintf("Jenkins holds a value for %q on this credential, but the configuration does not set it. "+
				"Jenkins cannot update a credential without re-sending every secret it holds: a secret left out of "+
				"the update is stored as empty, so this update would erase %q. Jenkins does not reveal the stored "+
				"value, nor whether it is empty.\n\n"+
				"To update the credential, manage the secret in Terraform by setting %s (set it to \"\" if it "+
				"should be empty), or replace the resource to recreate the credential without it.",
				u.attr, u.attr, manage))
	}
}

// credentialXMLElements records the names of the direct children of a
// credential's config.xml root element.
type credentialXMLElements struct {
	names map[string]bool
}

func (e *credentialXMLElements) UnmarshalXML(d *xml.Decoder, _ xml.StartElement) error {
	e.names = make(map[string]bool)
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			e.names[t.Name.Local] = true
			if err := d.Skip(); err != nil {
				return err
			}
		case xml.EndElement:
			return nil
		}
	}
}
