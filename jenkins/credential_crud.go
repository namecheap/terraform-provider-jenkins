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
//     for an optional secret that Jenkins may hold without Terraform (username
//     password, Vault secret_id, SSH passphrase). When neither the configuration
//     nor state has a value for it, Update first checks that Jenkins holds none
//     (see resolveSecretUpdate). When empty, a secret the configuration leaves
//     unset is sent as empty: the secret is required, has a schema default, or
//     is replaced by another attribute (Azure's certificate_id).
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
	// empty, so on Update a field is absent only when Terraform has never managed
	// it and Jenkins was found to hold no value for it.
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

	secrets := make(map[string]string, len(c.spec.secretFields))
	var unmanaged []unmanagedSecret
	for _, f := range c.spec.secretFields {
		value, unset := resolveSecretUpdate(ctx, req, f.name, f.hasWriteOnly, f.element != "", &resp.Diagnostics)
		if unset {
			unmanaged = append(unmanaged, unmanagedSecret{attr: f.name, element: f.element, hasWriteOnly: f.hasWriteOnly})
			continue
		}
		secrets[f.name] = value
	}
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

// attributeGetter reads one attribute; tfsdk.Plan, tfsdk.State and tfsdk.Config
// implement it.
type attributeGetter interface {
	GetAttribute(ctx context.Context, p path.Path, target interface{}) diag.Diagnostics
}

func getStringAttribute(ctx context.Context, src attributeGetter, attr string, diags *diag.Diagnostics) types.String {
	var v types.String
	diags.Append(src.GetAttribute(ctx, path.Root(attr), &v)...)
	return v
}

// resolveSecretUpdate resolves one secret attribute for an update. Jenkins'
// config.xml update replaces the whole credential, and a secret element that is
// left out, empty or redacted is stored as empty: there is no way to keep a
// stored secret. So an update sends every secret Terraform knows: the
// write-only "<attr>_wo" from config when set (write-only values are present in
// config on every apply; "<attr>_wo_version" only forces the update), otherwise
// the plain value from the plan. Under lifecycle.ignore_changes the plan holds
// the value from state, so that value is re-sent.
//
// When the plan has no value but the configuration sets one, ignore_changes is
// keeping a null value from state, as after an import (Jenkins never returns
// secrets). Terraform has nothing to send, so an error is added rather than the
// secret erased.
//
// When the configuration sets neither attribute, the secret is sent as empty,
// except an optional one (optional true) that Jenkins may hold without
// Terraform. If state has a value or a write-only version for it, Terraform set
// it and the configuration now drops it, so it is cleared as the plan shows.
// Otherwise unmanaged is true: Terraform has never managed the secret, and the
// caller must check that Jenkins holds no value for it (checkUnmanagedSecrets)
// before sending an update without it.
func resolveSecretUpdate(ctx context.Context, req resource.UpdateRequest, attr string, hasWriteOnly, optional bool, diags *diag.Diagnostics) (value string, unmanaged bool) {
	if hasWriteOnly {
		if wo := getStringAttribute(ctx, req.Config, attr+"_wo", diags); !wo.IsNull() {
			return wo.ValueString(), false
		}
	}
	if v := getStringAttribute(ctx, req.Plan, attr, diags); !v.IsNull() {
		return v.ValueString(), false
	}
	if !getStringAttribute(ctx, req.Config, attr, diags).IsNull() {
		manage := ""
		if hasWriteOnly {
			manage = fmt.Sprintf(", or manage the secret with `%s_wo` and `%s_wo_version` instead of %q", attr, attr, attr)
		}
		diags.AddAttributeError(path.Root(attr), "Credential Secret Not Known to Terraform",
			fmt.Sprintf("The configuration sets %q, but Terraform has no value for it: lifecycle.ignore_changes keeps "+
				"the value from state, and state holds none, as after an import (Jenkins never returns secrets). "+
				"Jenkins cannot update a credential without re-sending every secret it holds: a secret left out of "+
				"the update is stored as empty, so this update would erase %q.\n\n"+
				"To update the credential, remove %q from ignore_changes for one apply so that the configured value "+
				"is sent%s.",
				attr, attr, attr, manage))
		return "", false
	}
	if !optional {
		return "", false
	}
	if !getStringAttribute(ctx, req.State, attr, diags).IsNull() ||
		(hasWriteOnly && !getStringAttribute(ctx, req.State, attr+"_wo_version", diags).IsNull()) {
		return "", false
	}
	return "", true
}

// unmanagedSecret is an optional secret attribute that Terraform has never
// managed (neither the configuration nor state has a value for it), so an
// update has no value to send for it.
type unmanagedSecret struct {
	attr         string // Terraform attribute name, e.g. "password"
	element      string // XML element in the credential's config.xml
	hasWriteOnly bool   // whether "<attr>_wo" exists
}

// checkUnmanagedSecrets refuses an update that would erase a secret Terraform
// has never managed. It reads the credential's config.xml, where Jenkins writes
// each secret it holds as <element><secret-redacted/></element> and omits one it
// does not hold, and adds an error for every unmanaged secret that is present.
// Jenkins writes an empty secret the same way (a username or Vault AppRole
// credential created without its secret holds an empty one) and does not reveal
// the value, so a present element always counts as a value. pathDomain and
// pathName must already be escaped (escapeCredentialPath).
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
			manage = fmt.Sprintf("`%s`, or `%s_wo` with `%s_wo_version`,", u.attr, u.attr, u.attr)
		}
		diags.AddAttributeError(path.Root(u.attr), "Update Would Erase Credential Secret",
			fmt.Sprintf("Jenkins reports a stored value for %q on this credential, but neither the configuration nor "+
				"the Terraform state has one. Jenkins cannot update a credential without re-sending every secret it "+
				"holds: a secret left out of the update is stored as empty, so this update would erase %q. Jenkins "+
				"does not reveal the stored value, and reports one even when it is empty (a username or Vault AppRole "+
				"credential created without its secret holds an empty one), so Terraform cannot tell whether "+
				"anything would be lost.\n\n"+
				"To update the credential, set %s in the configuration (set it to \"\" to store an empty value, "+
				"which erases any value set within Jenkins), or replace the resource.",
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
