package jenkins

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// cascConfig builds a config holding m. tfsdk.Config has no setter, so the
// value is built through a State of the same schema.
func cascConfig(t *testing.T, s rschema.Schema, m configurationAsCodeResourceModel) tfsdk.Config {
	t.Helper()
	st := tfsdk.State{Schema: s}
	if diags := st.Set(context.Background(), m); diags.HasError() {
		t.Fatalf("config: %v", diags)
	}
	return tfsdk.Config{Schema: s, Raw: st.Raw}
}

func TestConfigurationAsCodeCreateSubstitutesSecretsWo(t *testing.T) {
	ctx := context.Background()
	const secret = "wo-secret-value-0001"
	declared := "securityRealm:\n  oic:\n    clientSecret: \"${client_secret}\"\n"

	var posted string
	r := &configurationAsCodeResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockApplyCASC: func(_ context.Context, doc string) error { posted = doc; return nil },
	}}}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)

	// Terraform plans a write-only attribute as null and hands its value
	// over in config only.
	m := cascNullModel("jenkins", declared)
	m.ID = types.StringUnknown()
	m.SecretsWoVersion = types.StringValue("1")
	plan := tfsdk.Plan{Schema: sr.Schema}
	if diags := plan.Set(ctx, m); diags.HasError() {
		t.Fatalf("plan: %v", diags)
	}
	m.SecretsWo = types.MapValueMust(types.StringType, map[string]attr.Value{"client_secret": types.StringValue(secret)})
	config := cascConfig(t, sr.Schema, m)

	resp := resource.CreateResponse{State: tfsdk.State{Schema: sr.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan, Config: config}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diags: %v", resp.Diagnostics)
	}

	if !strings.Contains(posted, secret) || strings.Contains(posted, "${client_secret}") {
		t.Errorf("posted document did not substitute the secret:\n%s", posted)
	}
	if got := cascStateYAML(t, resp.State); got != declared {
		t.Errorf("state yaml = %q, want the declared document %q", got, declared)
	}
	assertCASCStateOmits(t, resp.State, secret)
}

// TestConfigurationAsCodeApplySecretsWoErrors covers the checks apply makes
// with the write-only values in hand: a key yaml never references, which
// ValidateConfig cannot see while yaml is unknown at plan, and a JCasC error
// quoting the substituted value.
func TestConfigurationAsCodeApplySecretsWoErrors(t *testing.T) {
	ctx := context.Background()
	const secret = "wo-secret-value-0002"

	for name, tc := range map[string]struct {
		keys     map[string]string
		applyErr error
		posted   bool
	}{
		"unreferenced key": {
			keys: map[string]string{"client_secret": secret, "typo": "x"},
		},
		"error quoting the value": {
			keys:     map[string]string{"client_secret": secret, "short": "wo-secret"},
			applyErr: errors.New("JCasC apply failed (status 400): Arguments: [" + secret + "]"),
			posted:   true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var posted bool
			r := &configurationAsCodeResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
				mockApplyCASC: func(context.Context, string) error { posted = true; return tc.applyErr },
			}}}
			var sr resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &sr)

			declared := "a: \"${client_secret}\"\n"
			if _, ok := tc.keys["short"]; ok {
				declared += "b: \"${short}\"\n"
			}
			m := cascNullModel("jenkins", declared)
			m.ID = types.StringUnknown()
			m.SecretsWoVersion = types.StringValue("1")
			plan := tfsdk.Plan{Schema: sr.Schema}
			if diags := plan.Set(ctx, m); diags.HasError() {
				t.Fatalf("plan: %v", diags)
			}
			elems := map[string]attr.Value{}
			for k, v := range tc.keys {
				elems[k] = types.StringValue(v)
			}
			m.SecretsWo = types.MapValueMust(types.StringType, elems)

			resp := resource.CreateResponse{State: tfsdk.State{Schema: sr.Schema}}
			r.Create(ctx, resource.CreateRequest{Plan: plan, Config: cascConfig(t, sr.Schema, m)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected an error")
			}
			if posted != tc.posted {
				t.Errorf("posted = %v, want %v", posted, tc.posted)
			}
			for _, d := range resp.Diagnostics {
				// "short" is a prefix of secret: hiding it first would leave
				// the rest of secret in clear.
				if strings.Contains(d.Detail(), "wo-secret") || strings.Contains(d.Detail(), "value-0002") {
					t.Errorf("diagnostic holds a secret value: %s", d.Detail())
				}
			}
		})
	}
}

func TestConfigurationAsCodeValidateConfigUnreferencedSecret(t *testing.T) {
	ctx := context.Background()
	r, s := cascTestResource("")

	for name, tc := range map[string]struct {
		keys    []string
		wantErr bool
	}{
		"all referenced": {keys: []string{"used"}},
		"typo":           {keys: []string{"used", "usde"}, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			m := cascNullModel("jenkins", "a: ${used}\n")
			elems := map[string]attr.Value{}
			for _, k := range tc.keys {
				elems[k] = types.StringValue("v")
			}
			m.SecretsWo = types.MapValueMust(types.StringType, elems)
			m.SecretsWoVersion = types.StringValue("1")
			config := cascConfig(t, s, m)
			var resp resource.ValidateConfigResponse
			r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: config}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("HasError = %v, want %v: %v", resp.Diagnostics.HasError(), tc.wantErr, resp.Diagnostics)
			}
			if tc.wantErr && !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "usde") {
				t.Errorf("diagnostic does not name the unreferenced key: %v", resp.Diagnostics)
			}
		})
	}
}

func TestConfigurationAsCodeSecretErrorBodyOmitted(t *testing.T) {
	for _, tc := range []struct{ name, value, reported, expression string }{
		{"json escaped", "pw\"q\\key\tline\nend", "pw\"q\\key\tline\nend", "${v}"},
		{"decoded", base64.StdEncoding.EncodeToString([]byte("decoded-secret")), "decoded-secret", "${decodeBase64:${v}}"},
		{"short", "a", "a", "${v}"},
		{"fixed text", "jenkins", "jenkins", "${v}"},
		{"html escaped", "<secret>", "&lt;secret&gt;", "${v}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal([]map[string]interface{}{{"line": 1, "message": "jenkins rejected " + tc.reported}})
			srv := cascTestServer(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400); _, _ = w.Write(body) })
			defer srv.Close()
			client, err := newJenkinsClient(&Config{ServerURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			r, s := cascTestResource("")
			r.client = client
			m := cascNullModel("jenkins", "viewsTabBar: '"+tc.expression+"'\n")
			m.SecretsWoVersion = types.StringValue("1")
			plan := tfsdk.Plan{Schema: s}
			if d := plan.Set(context.Background(), m); d.HasError() {
				t.Fatal(d)
			}
			m.SecretsWo = types.MapValueMust(types.StringType, map[string]attr.Value{"v": types.StringValue(tc.value)})
			resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
			r.Create(context.Background(), resource.CreateRequest{Plan: plan, Config: cascConfig(t, s, m)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected error")
			}
			// Identical text for every value: no escaped/decoded message or masking
			// of fixed text can disclose which secret was supplied.
			const want = "JCasC rejected the configuration (HTTP 400). Controller error details are omitted because this request contains write-only secrets."
			if got := resp.Diagnostics.Errors()[0].Detail(); got != want {
				t.Fatalf("diagnostic = %q, want fixed safe text", got)
			}
		})
	}
}

func TestConfigurationAsCodeWriteOnlySchema(t *testing.T) {
	ctx := context.Background()
	r, s := cascTestResource("")
	wo := s.Attributes["secrets_wo"].(rschema.MapAttribute)
	if !wo.IsWriteOnly() || !wo.IsSensitive() || !wo.IsOptional() || !wo.ElementType.Equal(types.StringType) {
		t.Fatal("secrets_wo must be an optional sensitive write-only string map")
	}
	ver := s.Attributes["secrets_wo_version"]
	if !ver.IsOptional() || ver.IsWriteOnly() || ver.IsComputed() {
		t.Fatal("version must be optional and stored in state")
	}
	for _, tc := range []struct {
		name      string
		value     types.Map
		version   types.String
		wantError bool
	}{
		{"neither", types.MapNull(types.StringType), types.StringNull(), false},
		{"both", types.MapValueMust(types.StringType, map[string]attr.Value{"v": types.StringValue("x")}), types.StringValue("1"), false},
		{"map only", types.MapValueMust(types.StringType, map[string]attr.Value{"v": types.StringValue("x")}), types.StringNull(), true},
		{"version only", types.MapNull(types.StringType), types.StringValue("1"), true},
		{"null element", types.MapValueMust(types.StringType, map[string]attr.Value{"v": types.StringNull()}), types.StringValue("1"), true},
		{"unknown element", types.MapValueMust(types.StringType, map[string]attr.Value{"v": types.StringUnknown()}), types.StringValue("1"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := cascNullModel("jenkins", "systemMessage: ${v}")
			m.SecretsWo = tc.value
			m.SecretsWoVersion = tc.version
			config := cascConfig(t, s, m)
			var ds diag.Diagnostics
			for _, v := range r.ConfigValidators(ctx) {
				var resp resource.ValidateConfigResponse
				v.ValidateResource(ctx, resource.ValidateConfigRequest{Config: config}, &resp)
				ds.Append(resp.Diagnostics...)
			}
			for _, v := range wo.Validators {
				var resp validator.MapResponse
				v.ValidateMap(ctx, validator.MapRequest{Config: config, ConfigValue: tc.value, Path: path.Root("secrets_wo"), PathExpression: path.MatchRoot("secrets_wo")}, &resp)
				ds.Append(resp.Diagnostics...)
			}
			if ds.HasError() != tc.wantError {
				t.Fatalf("diagnostics: %v, wantError=%v", ds, tc.wantError)
			}
		})
	}
}

func TestConfigurationAsCodeWarnsRemovingSecrets(t *testing.T) {
	ctx := context.Background()
	r, s := cascTestResource("")
	for _, tc := range []struct {
		name                   string
		yaml                   types.String
		oldVersion, newVersion types.String
		wantWarning            bool
	}{
		{"remaining reference", types.StringValue("systemMessage: ${v}"), types.StringValue("1"), types.StringNull(), true},
		{"unknown yaml", types.StringUnknown(), types.StringValue("1"), types.StringNull(), true},
		{"reference removed", types.StringValue("systemMessage: plain"), types.StringValue("1"), types.StringNull(), false},
		{"escaped reference", types.StringValue("systemMessage: ^${v}"), types.StringValue("1"), types.StringNull(), false},
		{"rotation", types.StringValue("systemMessage: ${v}"), types.StringValue("1"), types.StringValue("2"), false},
		{"controller source", types.StringValue("systemMessage: ${v}"), types.StringNull(), types.StringNull(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := cascNullModel("jenkins", "systemMessage: ${v}")
			before.SecretsWoVersion = tc.oldVersion
			after := cascNullModel("jenkins", "")
			after.YAML = tc.yaml
			after.SecretsWoVersion = tc.newVersion
			st := tfsdk.State{Schema: s}
			if ds := st.Set(ctx, before); ds.HasError() {
				t.Fatal(ds)
			}
			plan := tfsdk.Plan{Schema: s}
			if ds := plan.Set(ctx, after); ds.HasError() {
				t.Fatal(ds)
			}
			resp := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{State: st, Plan: plan}, &resp)
			if resp.Diagnostics.HasError() || (len(resp.Diagnostics.Warnings()) > 0) != tc.wantWarning {
				t.Fatalf("diagnostics: %v", resp.Diagnostics)
			}
		})
	}
}
