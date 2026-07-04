package jenkins

import (
	"context"
	"testing"

	gojenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/path"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// buildDisabledPlan constructs a minimal tfsdk.Plan carrying only a "disabled"
// bool attribute so templatePlanModifier.PlanModifyString can read it via
// req.Plan.GetAttribute. A nil disabled pointer yields a null value (unmanaged).
func buildDisabledPlan(t *testing.T, ctx context.Context, disabled *bool) tfsdk.Plan {
	t.Helper()
	ps := rschema.Schema{Attributes: map[string]rschema.Attribute{
		"disabled": rschema.BoolAttribute{Optional: true},
	}}
	objType := ps.Type().TerraformType(ctx).(tftypes.Object)
	var dv interface{}
	if disabled != nil {
		dv = *disabled
	}
	raw := tftypes.NewValue(objType, map[string]tftypes.Value{
		"disabled": tftypes.NewValue(tftypes.Bool, dv),
	})
	return tfsdk.Plan{Schema: ps, Raw: raw}
}

func TestCovA_TemplatePlanModifier(t *testing.T) {
	ctx := context.Background()
	m := templatePlanModifier{}

	if m.Description(ctx) == "" {
		t.Error("templatePlanModifier.Description is empty")
	}
	if m.MarkdownDescription(ctx) == "" {
		t.Error("templatePlanModifier.MarkdownDescription is empty")
	}

	yes := true

	t.Run("config null returns early", func(t *testing.T) {
		req := planmodifier.StringRequest{
			Path:        path.Root("template"),
			ConfigValue: types.StringNull(),
			StateValue:  types.StringValue("<project/>"),
			Plan:        buildDisabledPlan(t, ctx, nil),
		}
		resp := &planmodifier.StringResponse{PlanValue: req.ConfigValue}
		m.PlanModifyString(ctx, req, resp)
		if !resp.PlanValue.IsNull() {
			t.Errorf("expected PlanValue to remain null, got %v", resp.PlanValue)
		}
	})

	t.Run("state null (create) keeps config", func(t *testing.T) {
		cfg := types.StringValue("<project><description>x</description></project>")
		req := planmodifier.StringRequest{
			Path:        path.Root("template"),
			ConfigValue: cfg,
			StateValue:  types.StringNull(),
			Plan:        buildDisabledPlan(t, ctx, nil),
		}
		resp := &planmodifier.StringResponse{PlanValue: cfg}
		m.PlanModifyString(ctx, req, resp)
		if !resp.PlanValue.Equal(cfg) {
			t.Errorf("expected PlanValue to stay config, got %v", resp.PlanValue)
		}
	})

	t.Run("semantically equal (whitespace) suppresses diff", func(t *testing.T) {
		cfg := types.StringValue("<project>  <description>x</description></project>")
		state := types.StringValue("<project><description>x</description></project>")
		req := planmodifier.StringRequest{
			Path:        path.Root("template"),
			ConfigValue: cfg,
			StateValue:  state,
			Plan:        buildDisabledPlan(t, ctx, nil),
		}
		resp := &planmodifier.StringResponse{PlanValue: cfg}
		m.PlanModifyString(ctx, req, resp)
		if !resp.PlanValue.Equal(state) {
			t.Errorf("expected PlanValue to become prior state (suppressed), got %v", resp.PlanValue)
		}
	})

	t.Run("disabled managed true suppresses disabled-only change", func(t *testing.T) {
		cfg := types.StringValue("<project><disabled>false</disabled><description>x</description></project>")
		state := types.StringValue("<project><disabled>true</disabled><description>x</description></project>")
		req := planmodifier.StringRequest{
			Path:        path.Root("template"),
			ConfigValue: cfg,
			StateValue:  state,
			Plan:        buildDisabledPlan(t, ctx, &yes),
		}
		resp := &planmodifier.StringResponse{PlanValue: cfg}
		m.PlanModifyString(ctx, req, resp)
		if !resp.PlanValue.Equal(state) {
			t.Errorf("expected disabled-only change to be suppressed, got %v", resp.PlanValue)
		}
	})

	t.Run("genuine change keeps config", func(t *testing.T) {
		cfg := types.StringValue("<project><description>a</description></project>")
		state := types.StringValue("<project><description>b</description></project>")
		req := planmodifier.StringRequest{
			Path:        path.Root("template"),
			ConfigValue: cfg,
			StateValue:  state,
			Plan:        buildDisabledPlan(t, ctx, nil),
		}
		resp := &planmodifier.StringResponse{PlanValue: cfg}
		m.PlanModifyString(ctx, req, resp)
		if !resp.PlanValue.Equal(cfg) {
			t.Errorf("expected genuine change to keep config, got %v", resp.PlanValue)
		}
	})
}

func TestCovA_FolderPlanModifier(t *testing.T) {
	ctx := context.Background()
	m := folderPlanModifier{}

	if m.Description(ctx) == "" {
		t.Error("folderPlanModifier.Description is empty")
	}
	if m.MarkdownDescription(ctx) == "" {
		t.Error("folderPlanModifier.MarkdownDescription is empty")
	}

	t.Run("unknown config returns early", func(t *testing.T) {
		req := planmodifier.StringRequest{
			ConfigValue: types.StringUnknown(),
			StateValue:  types.StringNull(),
		}
		resp := &planmodifier.StringResponse{PlanValue: req.ConfigValue}
		m.PlanModifyString(ctx, req, resp)
		if resp.RequiresReplace {
			t.Error("unknown config must not force replacement")
		}
	})

	t.Run("both unset keeps prior state", func(t *testing.T) {
		state := types.StringNull()
		req := planmodifier.StringRequest{
			ConfigValue: types.StringNull(),
			StateValue:  state,
		}
		resp := &planmodifier.StringResponse{PlanValue: types.StringValue("sentinel")}
		m.PlanModifyString(ctx, req, resp)
		if !resp.PlanValue.Equal(state) {
			t.Errorf("expected PlanValue to become prior state, got %v", resp.PlanValue)
		}
		if resp.RequiresReplace {
			t.Error("both-unset must not force replacement")
		}
	})

	t.Run("empty string config vs empty state keeps state", func(t *testing.T) {
		state := types.StringValue("")
		req := planmodifier.StringRequest{
			ConfigValue: types.StringValue(""),
			StateValue:  state,
		}
		resp := &planmodifier.StringResponse{PlanValue: types.StringValue("sentinel")}
		m.PlanModifyString(ctx, req, resp)
		if !resp.PlanValue.Equal(state) {
			t.Errorf("expected PlanValue to become prior state, got %v", resp.PlanValue)
		}
	})

	t.Run("real folder change forces replacement", func(t *testing.T) {
		req := planmodifier.StringRequest{
			ConfigValue: types.StringValue("/job/b"),
			StateValue:  types.StringValue("/job/a"),
		}
		resp := &planmodifier.StringResponse{PlanValue: req.ConfigValue}
		m.PlanModifyString(ctx, req, resp)
		if !resp.RequiresReplace {
			t.Error("expected a real folder change to force replacement")
		}
	})

	t.Run("unchanged folder does not force replacement", func(t *testing.T) {
		req := planmodifier.StringRequest{
			ConfigValue: types.StringValue("/job/a"),
			StateValue:  types.StringValue("/job/a"),
		}
		resp := &planmodifier.StringResponse{PlanValue: req.ConfigValue}
		m.PlanModifyString(ctx, req, resp)
		if resp.RequiresReplace {
			t.Error("unchanged folder must not force replacement")
		}
	})
}

func TestCovA_StateFolderString(t *testing.T) {
	if got := stateFolderString(types.StringNull()); got != "" {
		t.Errorf("null => %q, want empty", got)
	}
	if got := stateFolderString(types.StringUnknown()); got != "" {
		t.Errorf("unknown => %q, want empty", got)
	}
	if got := stateFolderString(types.StringValue("/job/x")); got != "/job/x" {
		t.Errorf("value => %q, want /job/x", got)
	}
}

func TestCovA_ValidatorDescriptions(t *testing.T) {
	ctx := context.Background()
	for _, v := range []validator.String{folderNameValidator{}, jobXMLValidator{}} {
		if v.Description(ctx) == "" {
			t.Errorf("%T.Description is empty", v)
		}
		if v.MarkdownDescription(ctx) == "" {
			t.Errorf("%T.MarkdownDescription is empty", v)
		}
	}
}

func TestCovA_FolderNameValidatorBranches(t *testing.T) {
	ctx := context.Background()
	run := func(cv types.String) *validator.StringResponse {
		resp := &validator.StringResponse{}
		folderNameValidator{}.ValidateString(ctx, validator.StringRequest{Path: path.Root("folder"), ConfigValue: cv}, resp)
		return resp
	}
	if run(types.StringNull()).Diagnostics.HasError() {
		t.Error("null folder should be valid")
	}
	if run(types.StringUnknown()).Diagnostics.HasError() {
		t.Error("unknown folder should be valid")
	}
	if run(types.StringValue("parent/child")).Diagnostics.HasError() {
		t.Error("slash-separated folder should be valid")
	}
	if !run(types.StringValue(`bad\path`)).Diagnostics.HasError() {
		t.Error("backslash folder should be invalid")
	}
}

func TestCovA_JobXMLValidatorBranches(t *testing.T) {
	ctx := context.Background()
	run := func(cv types.String) *validator.StringResponse {
		resp := &validator.StringResponse{}
		jobXMLValidator{}.ValidateString(ctx, validator.StringRequest{Path: path.Root("template"), ConfigValue: cv}, resp)
		return resp
	}

	// null and unknown short-circuit.
	if run(types.StringNull()).Diagnostics.HasError() {
		t.Error("null template should be valid")
	}
	if run(types.StringUnknown()).Diagnostics.HasError() {
		t.Error("unknown template should be valid")
	}
	// empty / whitespace-only left to schema required-ness handling.
	if run(types.StringValue("   ")).Diagnostics.HasError() {
		t.Error("whitespace-only template should be valid")
	}
	// well-formed job XML.
	if run(types.StringValue("<project><description>x</description></project>")).Diagnostics.HasError() {
		t.Error("well-formed XML should be valid")
	}
	// malformed via an undefined XML entity.
	if !run(types.StringValue("<project>&undefined;</project>")).Diagnostics.HasError() {
		t.Error("undefined entity should be an error")
	}
	// well-formed but no root element yields a warning, not an error.
	noRoot := run(types.StringValue("just text"))
	if noRoot.Diagnostics.HasError() {
		t.Error("text-only template should not error")
	}
	if len(noRoot.Diagnostics.Warnings()) == 0 {
		t.Error("text-only template should warn about missing root element")
	}
}

// exerciseCredSpec drives every closure of a credential resource spec and
// returns the credential value built with all secret fields populated.
func exerciseCredSpec[M any](t *testing.T, s credentialSpec[M], m *M) interface{} {
	t.Helper()

	folder, domain, name := s.identity(m)
	_, _, _ = folder, domain, name

	s.setID(m, "folder/name")

	if empty := s.build(m, map[string]string{}); empty == nil {
		t.Fatal("build with no secrets returned nil")
	}

	secrets := map[string]string{}
	for _, f := range s.secretFields {
		_ = f.plainValue(m)
		if f.woVersion != nil {
			_ = f.woVersion(m)
		}
		secrets[f.name] = "sekret-" + f.name
	}

	built := s.build(m, secrets)
	if built == nil {
		t.Fatal("build with secrets returned nil")
	}

	if s.newAPIValue() == nil {
		t.Fatal("newAPIValue returned nil")
	}
	s.fromAPI(s.newAPIValue(), m)

	return built
}

func TestCovA_CredentialResourceSpecs(t *testing.T) {
	t.Run("secret_text", func(t *testing.T) {
		m := &credentialSecretTextResourceModel{
			Name: types.StringValue("cred"), Folder: types.StringValue("f"),
			Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
			Description: types.StringValue("d"), Secret: types.StringValue("plain"),
			SecretWoVersion: types.StringValue("1"),
		}
		built := exerciseCredSpec(t, secretTextCredentialSpec(), m)
		if m.ID.ValueString() != "folder/name" {
			t.Errorf("setID: got %q", m.ID.ValueString())
		}
		if got := built.(*gojenkins.StringCredentials).Secret; got != "sekret-secret" {
			t.Errorf("secret: got %q", got)
		}
	})

	t.Run("ssh", func(t *testing.T) {
		m := &credentialSSHResourceModel{
			Name: types.StringValue("cred"), Folder: types.StringValue("f"),
			Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
			Description: types.StringValue("d"), Username: types.StringValue("u"),
			PrivateKey: types.StringValue("pk"), PrivateKeyWoVersion: types.StringValue("1"),
			Passphrase: types.StringValue("pp"),
		}
		built := exerciseCredSpec(t, sshCredentialSpec(), m)
		cred := built.(*gojenkins.SSHCredentials)
		pk, ok := cred.PrivateKeySource.(*gojenkins.PrivateKey)
		if !ok || pk.Value != "sekret-privatekey" {
			t.Errorf("privatekey: got %+v", cred.PrivateKeySource)
		}
		if cred.Passphrase != "sekret-passphrase" {
			t.Errorf("passphrase: got %q", cred.Passphrase)
		}
	})

	t.Run("username", func(t *testing.T) {
		m := &credentialUsernameResourceModel{
			Name: types.StringValue("cred"), Folder: types.StringValue("f"),
			Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
			Description: types.StringValue("d"), Username: types.StringValue("u"),
			Password: types.StringValue("plain"), PasswordWoVersion: types.StringValue("1"),
		}
		built := exerciseCredSpec(t, usernameCredentialSpec(), m)
		cred := built.(*gojenkins.UsernameCredentials)
		if cred.Username != "u" || cred.Password != "sekret-password" {
			t.Errorf("username/password: got %q/%q", cred.Username, cred.Password)
		}
	})

	t.Run("secret_file", func(t *testing.T) {
		m := &credentialSecretFileResourceModel{
			Name: types.StringValue("cred"), Folder: types.StringValue("f"),
			Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
			Description: types.StringValue("d"), Filename: types.StringValue("file.txt"),
			SecretBytes: types.StringValue("plain"), SecretBytesWoVersion: types.StringValue("1"),
		}
		built := exerciseCredSpec(t, secretFileCredentialSpec(), m)
		cred := built.(*gojenkins.FileCredentials)
		if cred.Filename != "file.txt" || cred.SecretBytes != "sekret-secretbytes" {
			t.Errorf("filename/secretbytes: got %q/%q", cred.Filename, cred.SecretBytes)
		}
	})

	t.Run("aws", func(t *testing.T) {
		m := &credentialAwsResourceModel{
			Name: types.StringValue("cred"), Folder: types.StringValue("f"),
			Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
			Description: types.StringValue("d"), AccessKey: types.StringValue("ak"),
			SecretKey: types.StringValue("plain"), SecretKeyWoVersion: types.StringValue("1"),
			IamRoleArn: types.StringValue("arn"), IamMfaSerialNumber: types.StringValue("mfa"),
		}
		built := exerciseCredSpec(t, awsCredentialSpec(), m)
		cred := built.(*credentialAws)
		if cred.AccessKey != "ak" || cred.SecretKey != "sekret-secret_key" {
			t.Errorf("access/secret key: got %q/%q", cred.AccessKey, cred.SecretKey)
		}
	})

	t.Run("vault_approle", func(t *testing.T) {
		m := &credentialVaultAppRoleResourceModel{
			Name: types.StringValue("cred"), Folder: types.StringValue("f"),
			Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
			Description: types.StringValue("d"), Namespace: types.StringValue("ns"),
			Path: types.StringValue("approle"), RoleID: types.StringValue("rid"),
			SecretID: types.StringValue("plain"), SecretIDWoVersion: types.StringValue("1"),
		}
		built := exerciseCredSpec(t, vaultAppRoleCredentialSpec(), m)
		cred := built.(*VaultAppRoleCredentials)
		if cred.RoleID != "rid" || cred.SecretID != "sekret-secret_id" {
			t.Errorf("role/secret id: got %q/%q", cred.RoleID, cred.SecretID)
		}
	})

	t.Run("github_app", func(t *testing.T) {
		m := &credentialGitHubAppResourceModel{
			Name: types.StringValue("cred"), Folder: types.StringValue("f"),
			Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
			Description: types.StringValue("d"), AppID: types.StringValue("123"),
			PrivateKey: types.StringValue("plain"), PrivateKeyWoVersion: types.StringValue("1"),
		}
		built := exerciseCredSpec(t, gitHubAppCredentialSpec(), m)
		cred := built.(*GitHubAppCredentials)
		if cred.AppID != "123" || cred.PrivateKey != "sekret-private_key" {
			t.Errorf("app id/private key: got %q/%q", cred.AppID, cred.PrivateKey)
		}
	})

	t.Run("azure_service_principal", func(t *testing.T) {
		m := &credentialAzureServicePrincipalResourceModel{
			Name: types.StringValue("cred"), Folder: types.StringValue("f"),
			Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
			Description: types.StringValue("d"), SubscriptionId: types.StringValue("sub"),
			ClientId: types.StringValue("cid"), ClientSecret: types.StringValue("plain"),
			ClientSecretWoVersion: types.StringValue("1"), Tenant: types.StringValue("t"),
		}
		built := exerciseCredSpec(t, azureServicePrincipalCredentialSpec(), m)
		cred := built.(*AzureServicePrincipalCredentials)
		if cred.Data.ClientId != "cid" || cred.Data.ClientSecret != "sekret-client_secret" {
			t.Errorf("client id/secret: got %q/%q", cred.Data.ClientId, cred.Data.ClientSecret)
		}
	})
}

// exerciseCredReader drives every closure of a credential data-source reader.
func exerciseCredReader[M any](t *testing.T, r credentialDataSourceReader[M], m *M) {
	t.Helper()
	_ = r.folder(m)
	_ = r.name(m)
	_ = r.domain(m)
	r.setDomain(m, "_")
	r.setID(m, "id")
	if r.newAPIValue() == nil {
		t.Fatal("newAPIValue returned nil")
	}
	r.fromAPI(r.newAPIValue(), m)
}

func TestCovA_CredentialDataSourceReaders(t *testing.T) {
	t.Run("secret_text", func(t *testing.T) {
		m := &credentialSecretTextDataSourceModel{
			Name: types.StringValue("c"), Folder: types.StringValue("f"), Domain: types.StringNull(),
		}
		exerciseCredReader(t, secretTextCredentialDataSourceReader(), m)
		if m.ID.ValueString() != "id" || m.Domain.ValueString() != "_" {
			t.Errorf("setID/setDomain: %q/%q", m.ID.ValueString(), m.Domain.ValueString())
		}
	})
	t.Run("ssh", func(t *testing.T) {
		m := &credentialSSHDataSourceModel{
			Name: types.StringValue("c"), Folder: types.StringValue("f"), Domain: types.StringNull(),
		}
		exerciseCredReader(t, sshCredentialDataSourceReader(), m)
		if m.ID.ValueString() != "id" || m.Domain.ValueString() != "_" {
			t.Errorf("setID/setDomain: %q/%q", m.ID.ValueString(), m.Domain.ValueString())
		}
	})
	t.Run("username", func(t *testing.T) {
		m := &credentialUsernameDataSourceModel{
			Name: types.StringValue("c"), Folder: types.StringValue("f"), Domain: types.StringNull(),
		}
		exerciseCredReader(t, usernameCredentialDataSourceReader(), m)
		if m.ID.ValueString() != "id" || m.Domain.ValueString() != "_" {
			t.Errorf("setID/setDomain: %q/%q", m.ID.ValueString(), m.Domain.ValueString())
		}
	})
	t.Run("secret_file", func(t *testing.T) {
		m := &credentialSecretFileDataSourceModel{
			Name: types.StringValue("c"), Folder: types.StringValue("f"), Domain: types.StringNull(),
		}
		exerciseCredReader(t, secretFileCredentialDataSourceReader(), m)
		if m.ID.ValueString() != "id" || m.Domain.ValueString() != "_" {
			t.Errorf("setID/setDomain: %q/%q", m.ID.ValueString(), m.Domain.ValueString())
		}
	})
	t.Run("aws", func(t *testing.T) {
		m := &credentialAwsDataSourceModel{
			Name: types.StringValue("c"), Folder: types.StringValue("f"), Domain: types.StringNull(),
		}
		exerciseCredReader(t, awsCredentialDataSourceReader(), m)
		if m.ID.ValueString() != "id" || m.Domain.ValueString() != "_" {
			t.Errorf("setID/setDomain: %q/%q", m.ID.ValueString(), m.Domain.ValueString())
		}
	})
	t.Run("vault_approle", func(t *testing.T) {
		m := &credentialVaultAppRoleDataSourceModel{
			Name: types.StringValue("c"), Folder: types.StringValue("f"), Domain: types.StringNull(),
		}
		exerciseCredReader(t, vaultAppRoleCredentialDataSourceReader(), m)
		if m.ID.ValueString() != "id" || m.Domain.ValueString() != "_" {
			t.Errorf("setID/setDomain: %q/%q", m.ID.ValueString(), m.Domain.ValueString())
		}
	})
	t.Run("azure_service_principal", func(t *testing.T) {
		m := &credentialAzureServicePrincipalDataSourceModel{
			Name: types.StringValue("c"), Folder: types.StringValue("f"), Domain: types.StringNull(),
		}
		exerciseCredReader(t, azureServicePrincipalCredentialDataSourceReader(), m)
		if m.ID.ValueString() != "id" || m.Domain.ValueString() != "_" {
			t.Errorf("setID/setDomain: %q/%q", m.ID.ValueString(), m.Domain.ValueString())
		}
	})
	t.Run("certificate", func(t *testing.T) {
		m := &credentialCertificateDataSourceModel{
			Name: types.StringValue("c"), Folder: types.StringValue("f"), Domain: types.StringNull(),
		}
		exerciseCredReader(t, certificateCredentialDataSourceReader(), m)
		if m.ID.ValueString() != "id" || m.Domain.ValueString() != "_" {
			t.Errorf("setID/setDomain: %q/%q", m.ID.ValueString(), m.Domain.ValueString())
		}
	})
}
