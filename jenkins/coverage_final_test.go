package jenkins

import (
	"context"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Final targeted branches: role invalid-type / not-read-back, canonical-XML
// comparison edge cases, and ssh + write-only credential update paths.

func TestCovG_RoleTypeBranches(t *testing.T) {
	ctx := context.Background()
	s := covMSchema(ctx, &roleResource{resourceHelper: newResourceHelper()})
	perms := types.SetValueMust(types.StringType, []attr.Value{types.StringValue("p")})
	asg := types.SetValueMust(types.StringType, []attr.Value{types.StringValue("u")})

	// Invalid type on Create → validate rejects it.
	bad := &roleResourceModel{Type: types.StringValue("bogus"), Name: types.StringValue("r"), Permissions: perms, Assignments: asg}
	rC := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{}}}
	plan := tfsdk.Plan{Schema: s}
	plan.Set(ctx, bad)
	cResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	rC.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
	if !cResp.Diagnostics.HasError() {
		t.Error("role Create should reject an unknown type")
	}

	// Invalid type on Delete → roleTypeAPI miss.
	st := tfsdk.State{Schema: s}
	st.Set(ctx, bad)
	dResp := resource.DeleteResponse{State: st}
	rC.Delete(ctx, resource.DeleteRequest{State: st}, &dResp)
	if !dResp.Diagnostics.HasError() {
		t.Error("role Delete should reject an unknown type")
	}

	// Created but not read back: AddRole/assign succeed, GetRole returns empty.
	good := &roleResourceModel{Type: types.StringValue("global"), Name: types.StringValue("r"), Permissions: perms, Assignments: asg}
	rNR := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockAddRole:    func(context.Context, string, string, []string, string, bool) error { return nil },
		mockAssignRole: func(context.Context, string, string, string) error { return nil },
		mockGetRole:    func(context.Context, string, string, interface{}) error { return nil }, // empty → not found
	}}}
	plan2 := tfsdk.Plan{Schema: s}
	plan2.Set(ctx, good)
	c2 := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	rNR.Create(ctx, resource.CreateRequest{Plan: plan2}, &c2)
	if !c2.Diagnostics.HasError() {
		t.Error("role Create should error when the role can't be read back")
	}
}

func TestCovG_TemplatesEqualEdges(t *testing.T) {
	// attribute-order + empty-element differences → canonical comparison equal
	if !templatesEqual(`<x a="1" b="2"/>`, `<x b="2" a="1"></x>`) {
		t.Error("attribute order / empty-element form should compare equal")
	}
	// genuinely different child content → not equal
	if templatesEqual(`<x><a>1</a></x>`, `<x><a>2</a></x>`) {
		t.Error("different text content should not compare equal")
	}
	// malformed on one side → falls back, not equal
	if templatesEqual(`<good/>`, `<bad`) {
		t.Error("malformed XML should not compare equal to well-formed")
	}
	// whitespace / declaration only differences → equal
	if !templatesEqual(`<?xml version="1.0"?><x>  <a>1</a> </x>`, `<x><a>1</a></x>`) {
		t.Error("declaration/whitespace differences should compare equal")
	}
}

const covGFolderRichXML = `<com.cloudbees.hudson.plugins.folder.Folder><description>d</description><displayName>Team F</displayName><properties><com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty><inheritanceStrategy class="org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy"/><permission>hudson.model.Item.Read:alice</permission></com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty></properties></com.cloudbees.hudson.plugins.folder.Folder>`

func TestCovG_FolderRichSecurity(t *testing.T) {
	ctx := context.Background()
	job := covKJob(t, "/job/f", covGFolderRichXML)
	r := &folderResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
	}}}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	model := &folderResourceModel{
		Name: types.StringValue("f"), Folder: types.StringNull(),
		DisplayName: types.StringValue("Team F"), Description: types.StringValue("d"),
		Security: types.SetNull(folderSecurityObjectType), Template: types.StringNull(),
	}
	st := tfsdk.State{Schema: sr.Schema}
	st.Set(ctx, model)
	rResp := resource.ReadResponse{State: tfsdk.State{Schema: sr.Schema}}
	r.Read(ctx, resource.ReadRequest{State: st}, &rResp)
	if rResp.Diagnostics.HasError() {
		t.Errorf("folder Read (rich security): %v", rResp.Diagnostics)
	}
}

func TestCovG_FolderWithSecurity(t *testing.T) {
	ctx := context.Background()
	job := covKJob(t, "/job/f", covGFolderRichXML)
	r := &folderResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockCreateJobInFolder: func(context.Context, string, string, ...string) (*jenkins.Job, error) { return nil, nil },
		mockGetJob:            func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
	}}}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	s := sr.Schema

	secObj := types.ObjectValueMust(folderSecurityObjectType.AttrTypes, map[string]attr.Value{
		"inheritance_strategy": types.StringValue("org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy"),
		"permissions":          types.SetValueMust(types.StringType, []attr.Value{types.StringValue("hudson.model.Item.Read:alice")}),
	})
	model := &folderResourceModel{
		Name: types.StringValue("f"), Folder: types.StringNull(),
		DisplayName: types.StringValue("Team F"), Description: types.StringValue("d"),
		Security: types.SetValueMust(folderSecurityObjectType, []attr.Value{secObj}), Template: types.StringNull(),
	}

	// Create: securityFromModel renders the block into the config XML.
	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(ctx, model); d.HasError() {
		t.Fatalf("plan.Set: %v", d)
	}
	cResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
	if cResp.Diagnostics.HasError() {
		t.Errorf("folder Create with security: %v", cResp.Diagnostics)
	}

	// Update: exercises securityFromModel + Render on the update path too.
	st := tfsdk.State{Schema: s}
	st.Set(ctx, model)
	uResp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: st}, &uResp)
	if uResp.Diagnostics.HasError() {
		t.Errorf("folder Update with security: %v", uResp.Diagnostics)
	}
}

func TestCovG_CredentialFolderError(t *testing.T) {
	ctx := context.Background()
	// A non-empty folder triggers folderExists → GetFolder, which the live client
	// answers with 500, exercising the invalid-folder branch.
	client := covCredClientCode(t, 200, 500)
	r, s := covSTResource(client)
	model := covSTModel()
	model.Folder = types.StringValue("team")
	plan := tfsdk.Plan{Schema: s}
	plan.Set(ctx, model)
	cfgState := tfsdk.State{Schema: s}
	cfgState.Set(ctx, model)
	cfg := tfsdk.Config{Schema: s, Raw: cfgState.Raw}
	cResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan, Config: cfg}, &cResp)
	if !cResp.Diagnostics.HasError() {
		t.Error("credential Create should error when the folder does not exist")
	}
}

func TestCovG_JobReadDisabled(t *testing.T) {
	ctx := context.Background()
	job := covKJob(t, "/job/j", covNJobXML)
	r := &jobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
	}}}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	// disabled managed → Read's refreshMeta calls IsEnabled (Poll /api/json).
	model := &jobResourceModel{
		Name: types.StringValue("j"), Folder: types.StringNull(),
		Template: types.StringValue(covNJobXML), Disabled: types.BoolValue(false),
	}
	st := tfsdk.State{Schema: sr.Schema}
	st.Set(ctx, model)
	rResp := resource.ReadResponse{State: tfsdk.State{Schema: sr.Schema}}
	r.Read(ctx, resource.ReadRequest{State: st}, &rResp)
	if rResp.Diagnostics.HasError() {
		t.Errorf("job Read (disabled managed): %v", rResp.Diagnostics)
	}
}

func TestCovG_SSHLive(t *testing.T) {
	ctx := context.Background()
	credXML := `<com.cloudbees.jenkins.plugins.sshcredentials.impl.BasicSSHUserPrivateKey><id>k</id><scope>GLOBAL</scope><description>d</description><username>u</username></com.cloudbees.jenkins.plugins.sshcredentials.impl.BasicSSHUserPrivateKey>`
	client := covCredClient(t, credXML)
	r := &credentialSSHResource{credentialCRUD: newCredentialCRUD(sshCredentialSpec())}
	r.client = client
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	model := &credentialSSHResourceModel{
		Name: types.StringValue("k"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"), Username: types.StringValue("u"),
		PrivateKey: types.StringValue("KEY"), PrivateKeyWo: types.StringNull(), PrivateKeyWoVersion: types.StringNull(),
		Passphrase: types.StringValue("pp"),
	}
	covLCredCRUD(ctx, t, "ssh", r, sr.Schema, model)
}

func TestCovG_SecretTextWriteOnlyUpdate(t *testing.T) {
	ctx := context.Background()
	credXML := `<org.jenkinsci.plugins.plaincredentials.impl.StringCredentialsImpl><id>tok</id><scope>GLOBAL</scope><description>d</description></org.jenkinsci.plugins.plaincredentials.impl.StringCredentialsImpl>`
	client := covCredClient(t, credXML)
	r, s := covSTResource(client)

	// Write-only secret with a bumped version between state and plan → re-sent.
	state := &credentialSecretTextResourceModel{
		Name: types.StringValue("tok"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
		Secret: types.StringNull(), SecretWo: types.StringNull(), SecretWoVersion: types.StringValue("1"),
	}
	plan := &credentialSecretTextResourceModel{
		Name: types.StringValue("tok"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
		Secret: types.StringNull(), SecretWo: types.StringNull(), SecretWoVersion: types.StringValue("2"),
	}
	planV := tfsdk.Plan{Schema: s}
	planV.Set(ctx, plan)
	stV := tfsdk.State{Schema: s}
	stV.Set(ctx, state)
	// config carries the write-only value (present only during apply)
	cfgModel := &credentialSecretTextResourceModel{
		Name: types.StringValue("tok"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
		Secret: types.StringNull(), SecretWo: types.StringValue("rotated"), SecretWoVersion: types.StringValue("2"),
	}
	cfgState := tfsdk.State{Schema: s}
	cfgState.Set(ctx, cfgModel)
	cfg := tfsdk.Config{Schema: s, Raw: cfgState.Raw}

	uResp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(ctx, resource.UpdateRequest{Plan: planV, State: stV, Config: cfg}, &uResp)
	if uResp.Diagnostics.HasError() {
		t.Errorf("secret_text write-only Update: %v", uResp.Diagnostics)
	}
}
