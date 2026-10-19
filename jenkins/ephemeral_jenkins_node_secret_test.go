package jenkins

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/echoprovider"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func TestNodeSecretEphemeralResource(t *testing.T) {
	ctx := context.Background()

	schemaOf := func(t *testing.T) ephemeral.SchemaResponse {
		t.Helper()
		var sr ephemeral.SchemaResponse
		newNodeSecretEphemeralResource().Schema(ctx, ephemeral.SchemaRequest{}, &sr)
		if sr.Diagnostics.HasError() {
			t.Fatalf("schema: %v", sr.Diagnostics)
		}
		return sr
	}

	open := func(t *testing.T, client frameworkClient) (ephemeral.OpenResponse, nodeSecretModel) {
		t.Helper()
		e := &nodeSecretEphemeralResource{client: client}
		sr := schemaOf(t)

		cfg := tfsdk.State{Schema: sr.Schema}
		if diags := cfg.Set(ctx, nodeSecretModel{
			Name:   types.StringValue("agent1"),
			Secret: types.StringNull(),
		}); diags.HasError() {
			t.Fatalf("config: %v", diags)
		}
		resp := ephemeral.OpenResponse{Result: tfsdk.EphemeralResultData{Schema: sr.Schema}}
		e.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: sr.Schema, Raw: cfg.Raw}}, &resp)

		var got nodeSecretModel
		if !resp.Diagnostics.HasError() {
			resp.Diagnostics.Append(resp.Result.Get(ctx, &got)...)
		}
		return resp, got
	}

	t.Run("metadata", func(t *testing.T) {
		var resp ephemeral.MetadataResponse
		newNodeSecretEphemeralResource().Metadata(ctx, ephemeral.MetadataRequest{ProviderTypeName: "jenkins"}, &resp)
		if resp.TypeName != "jenkins_node_secret" {
			t.Errorf("TypeName = %q, want jenkins_node_secret", resp.TypeName)
		}
	})

	t.Run("schema", func(t *testing.T) {
		attrs := schemaOf(t).Schema.Attributes
		name, ok := attrs["name"].(schema.StringAttribute)
		if !ok || !name.IsRequired() || len(name.Validators) == 0 {
			t.Errorf("name: %+v, want a required string with validators", attrs["name"])
		}
		secret := attrs["secret"]
		if !secret.IsSensitive() || !secret.IsComputed() {
			t.Errorf("secret: sensitive=%v computed=%v, want both", secret.IsSensitive(), secret.IsComputed())
		}
	})

	t.Run("configure", func(t *testing.T) {
		cases := []struct {
			name       string
			data       any
			wantErr    bool
			wantClient bool
		}{
			{"unconfigured provider", nil, false, false},
			{"adapter", &jenkinsAdapter{}, false, true},
			{"wrong type", "not a client", true, false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				e := &nodeSecretEphemeralResource{}
				var resp ephemeral.ConfigureResponse
				e.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: tc.data}, &resp)
				if resp.Diagnostics.HasError() != tc.wantErr {
					t.Errorf("error = %v, want %v", resp.Diagnostics, tc.wantErr)
				}
				if (e.client != nil) != tc.wantClient {
					t.Errorf("client set = %v, want %v", e.client != nil, tc.wantClient)
				}
			})
		}
	})

	t.Run("open", func(t *testing.T) {
		var gotName string
		resp, got := open(t, &mockJenkinsClient{
			mockGetNodeSecret: func(_ context.Context, name string) (string, error) {
				gotName = name
				return fakeNodeSecret, nil
			},
		})
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diags: %v", resp.Diagnostics)
		}
		if gotName != "agent1" {
			t.Errorf("looked up %q, want agent1", gotName)
		}
		if got.Secret.ValueString() != fakeNodeSecret || got.Name.ValueString() != "agent1" {
			t.Errorf("result = %+v", got)
		}
	})

	t.Run("client error", func(t *testing.T) {
		resp, _ := open(t, &mockJenkinsClient{
			mockGetNodeSecret: func(context.Context, string) (string, error) { return "", fmt.Errorf("boom") },
		})
		if !resp.Diagnostics.HasError() {
			t.Error("expected an error diagnostic")
		}
	})

	t.Run("unconfigured client", func(t *testing.T) {
		resp, _ := open(t, nil)
		if !resp.Diagnostics.HasError() {
			t.Error("expected an error diagnostic, not a panic")
		}
	})
}

func TestJenkinsProvider_EphemeralResources(t *testing.T) {
	p := &JenkinsProvider{}
	for i, fn := range p.EphemeralResources(context.Background()) {
		if fn == nil || fn() == nil {
			t.Errorf("EphemeralResources()[%d] is nil", i)
		}
	}
}

// testAccEchoProviders adds the echo provider, which copies its ephemeral
// `data` argument into the state of an `echo` resource: the only way for a
// test to observe what an ephemeral resource returned.
func testAccEchoProviders() map[string]func() (tfprotov6.ProviderServer, error) {
	providers := map[string]func() (tfprotov6.ProviderServer, error){
		"echo": echoprovider.NewProviderServer(),
	}
	maps.Copy(providers, testAccProviders)
	return providers
}

func TestAccJenkinsNodeSecretEphemeralResource_basic(t *testing.T) {
	name := "tf-acc-node-secret-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	config := fmt.Sprintf(`
resource jenkins_node foo {
  name      = %q
  remote_fs = "/home/jenkins/agent"
}

ephemeral jenkins_node_secret foo {
  name = jenkins_node.foo.id
}

data jenkins_node_secret foo {
  name = jenkins_node.foo.id
}

provider echo {
  data = ephemeral.jenkins_node_secret.foo
}

resource echo foo {}`, name)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccEchoProviders(),
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		CheckDestroy:             testAccCheckJenkinsNodeDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("echo.foo", "data.name", name),
					resource.TestMatchResourceAttr("echo.foo", "data.secret", regexp.MustCompile(`^[0-9a-f]{64}$`)),
					resource.TestCheckResourceAttrPair("echo.foo", "data.secret", "data.jenkins_node_secret.foo", "secret"),
				),
			},
			{
				Config: config,
			},
		},
	})
}

func TestAccJenkinsNodeSecretEphemeralResource_missing(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccEchoProviders(),
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		Steps: []resource.TestStep{
			{
				Config: `
ephemeral jenkins_node_secret foo {
  name = "tf-acc-node-secret-does-not-exist"
}

provider echo {
  data = ephemeral.jenkins_node_secret.foo
}

resource echo foo {}`,
				ExpectError: regexp.MustCompile(`not found or not an\s+inbound \(JNLP\) agent`),
			},
			{
				Config: `
ephemeral jenkins_node_secret foo {
  name = ""
}`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`string length must be at least 1`),
			},
		},
	})
}
