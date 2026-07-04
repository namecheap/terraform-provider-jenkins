package jenkins

import (
	"context"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// This file completes CRUD coverage for job and folder happy paths (using a
// live gojenkins Job wired to an httptest server via covcLiveJob, with disabled
// unset so the JSON /api/json IsEnabled poll is skipped), plus provider
// Configure validation branches and a few remaining resource error branches.

const covNJobXML = `<project><description>d</description><disabled>false</disabled></project>`
const covNFolderXML = `<com.cloudbees.hudson.plugins.folder.Folder><description>d</description></com.cloudbees.hudson.plugins.folder.Folder>`

// --- job happy path (disabled unset → no IsEnabled/JSON call) ---

func TestCovN_JobHappy(t *testing.T) {
	ctx := context.Background()
	live := covcLiveJob(t, "/job/j", covNJobXML)
	r := &jobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockCreateJobInFolder: func(_ context.Context, _, _ string, _ ...string) (*jenkins.Job, error) { return live, nil },
		mockGetJob:            func(_ context.Context, _ string, _ ...string) (*jenkins.Job, error) { return live, nil },
		mockDeleteJobInFolder: func(_ context.Context, _ string, _ ...string) (bool, error) { return true, nil },
	}}}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	s := sr.Schema
	model := &jobResourceModel{
		Name: types.StringValue("j"), Folder: types.StringNull(),
		Template: types.StringValue(covNJobXML), Disabled: types.BoolNull(),
	}

	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(ctx, model); d.HasError() {
		t.Fatalf("job plan.Set: %v", d)
	}
	cResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
	if cResp.Diagnostics.HasError() {
		t.Errorf("job Create happy: %v", cResp.Diagnostics)
	}

	st := tfsdk.State{Schema: s}
	st.Set(ctx, model)
	rResp := resource.ReadResponse{State: tfsdk.State{Schema: s}}
	r.Read(ctx, resource.ReadRequest{State: st}, &rResp)
	if rResp.Diagnostics.HasError() {
		t.Errorf("job Read happy: %v", rResp.Diagnostics)
	}
	// Update happy path is not exercised here: job.UpdateConfig issues a CSRF
	// crumb GET that the XML-only covcLiveJob server cannot answer with JSON. The
	// Update error branch is covered in TestCovM_Job.
}

// --- folder happy path (populate parses folder config XML; no IsEnabled) ---

func TestCovN_FolderHappy(t *testing.T) {
	ctx := context.Background()
	live := covcLiveJob(t, "/job/f", covNFolderXML)
	r := &folderResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockCreateJobInFolder: func(_ context.Context, _, _ string, _ ...string) (*jenkins.Job, error) { return nil, nil },
		mockGetJob:            func(_ context.Context, _ string, _ ...string) (*jenkins.Job, error) { return live, nil },
		mockDeleteJobInFolder: func(_ context.Context, _ string, _ ...string) (bool, error) { return true, nil },
	}}}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	s := sr.Schema
	model := &folderResourceModel{
		Name: types.StringValue("f"), Folder: types.StringNull(),
		DisplayName: types.StringValue(""), Description: types.StringValue(""),
		Security: types.SetNull(folderSecurityObjectType), Template: types.StringNull(),
	}

	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(ctx, model); d.HasError() {
		t.Fatalf("folder plan.Set: %v", d)
	}
	cResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
	if cResp.Diagnostics.HasError() {
		t.Errorf("folder Create happy: %v", cResp.Diagnostics)
	}

	st := tfsdk.State{Schema: s}
	st.Set(ctx, model)
	rResp := resource.ReadResponse{State: tfsdk.State{Schema: s}}
	r.Read(ctx, resource.ReadRequest{State: st}, &rResp)
	if rResp.Diagnostics.HasError() {
		t.Errorf("folder Read happy: %v", rResp.Diagnostics)
	}
	// Update happy path is not exercised here (see TestCovN_JobHappy note); the
	// folder Update error branch is covered in TestCovM_Folder.
}

// --- provider Configure validation branches (return before dialing) ---

func TestCovN_Configure(t *testing.T) {
	ctx := context.Background()
	t.Setenv("JENKINS_URL", "")
	t.Setenv("JENKINS_USERNAME", "")
	t.Setenv("JENKINS_PASSWORD", "")
	t.Setenv("JENKINS_CA_CERT", "")
	t.Setenv("JENKINS_REQUEST_TIMEOUT", "")
	t.Setenv("JENKINS_RETRY_MAX", "")
	t.Setenv("JENKINS_RETRY_WAIT_MIN", "")
	t.Setenv("JENKINS_RETRY_WAIT_MAX", "")

	p := &JenkinsProvider{}
	var sr provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &sr)
	s := sr.Schema

	cfgFrom := func(m *JenkinsProviderModel) tfsdk.Config {
		st := tfsdk.State{Schema: s}
		if d := st.Set(ctx, m); d.HasError() {
			t.Fatalf("provider model set: %v", d)
		}
		return tfsdk.Config{Schema: s, Raw: st.Raw}
	}

	cases := map[string]*JenkinsProviderModel{
		"missing server_url": {ServerURL: types.StringValue("")},
		"bad request_timeout": {
			ServerURL: types.StringValue("http://localhost:8080"), RequestTimeout: types.StringValue("nope"),
		},
		"bad retry_wait_min": {
			ServerURL: types.StringValue("http://localhost:8080"), RetryWaitMin: types.StringValue("nope"),
		},
		"bad ca_cert path": {
			ServerURL: types.StringValue("http://localhost:8080"), CACert: types.StringValue("/no/such/cert.pem"),
		},
	}
	for name, m := range cases {
		resp := provider.ConfigureResponse{}
		p.Configure(ctx, provider.ConfigureRequest{Config: cfgFrom(m)}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Errorf("Configure(%s) expected an error diagnostic", name)
		}
	}
}
