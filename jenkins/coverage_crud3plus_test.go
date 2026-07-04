package jenkins

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// covKJob returns a *jenkins.Job wired to an httptest server that can satisfy a
// full UpdateConfig round-trip: the CSRF-crumb GET and the /api/json poll return
// JSON, POSTs return an empty JSON object, and a GET of config.xml returns the
// supplied XML. This unlocks the Update happy paths (and job IsEnabled) that a
// plain XML-only server can't.
func covKJob(t *testing.T, base, configXML string) *jenkins.Job {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "crumbIssuer"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"crumb":"abc","crumbRequestField":"Jenkins-Crumb"}`)
		case r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{}`)
		case strings.Contains(r.URL.Path, "config.xml"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, configXML)
		default: // /api/json for Poll / IsEnabled
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"color":"blue"}`)
		}
	}))
	t.Cleanup(srv.Close)
	client, err := newJenkinsClient(&Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("newJenkinsClient: %v", err)
	}
	return &jenkins.Job{Jenkins: client.Jenkins, Base: base, Raw: &jenkins.JobResponse{}}
}

// covKUpdate runs a resource's Update against a plan+prior-state built from model
// and asserts no diagnostics (happy path).
func covKUpdate(ctx context.Context, t *testing.T, label string, r resource.Resource, model any) {
	t.Helper()
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	plan := tfsdk.Plan{Schema: sr.Schema}
	if d := plan.Set(ctx, model); d.HasError() {
		t.Fatalf("%s plan.Set: %v", label, d)
	}
	st := tfsdk.State{Schema: sr.Schema}
	if d := st.Set(ctx, model); d.HasError() {
		t.Fatalf("%s state.Set: %v", label, d)
	}
	resp := resource.UpdateResponse{State: tfsdk.State{Schema: sr.Schema}}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Errorf("%s Update happy: %v", label, resp.Diagnostics)
	}
}

func TestCovK_PipelineUpdate(t *testing.T) {
	ctx := context.Background()
	model := &pipelineJobResourceModel{
		Name: types.StringValue("p"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Script: types.StringValue("pipeline { agent any }"), Sandbox: types.BoolValue(true), Disabled: types.BoolValue(false),
	}
	cfg, err := model.configXML()
	if err != nil {
		t.Fatalf("configXML: %v", err)
	}
	job := covKJob(t, "/job/p", cfg)
	r := &pipelineJobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
	}}}
	covKUpdate(ctx, t, "pipeline", r, model)
}

func TestCovK_MultibranchUpdate(t *testing.T) {
	ctx := context.Background()
	model := &multibranchPipelineResourceModel{
		Name: types.StringValue("m"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Remote: types.StringValue("https://git.example.com/x.git"), CredentialsID: types.StringValue(""),
		ScriptPath: types.StringValue("Jenkinsfile"),
	}
	cfg, err := model.configXML()
	if err != nil {
		t.Fatalf("configXML: %v", err)
	}
	job := covKJob(t, "/job/m", cfg)
	r := &multibranchPipelineResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
	}}}
	covKUpdate(ctx, t, "multibranch", r, model)
}

func TestCovK_FolderUpdate(t *testing.T) {
	ctx := context.Background()
	model := &folderResourceModel{
		Name: types.StringValue("f"), Folder: types.StringNull(),
		DisplayName: types.StringValue(""), Description: types.StringValue("d"),
		Security: types.SetNull(folderSecurityObjectType), Template: types.StringNull(),
	}
	job := covKJob(t, "/job/f", covNFolderXML)
	r := &folderResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
	}}}
	covKUpdate(ctx, t, "folder", r, model)
}

func TestCovK_JobUpdate(t *testing.T) {
	ctx := context.Background()
	job := covKJob(t, "/job/j", covNJobXML)
	newR := func() *jobResource {
		return &jobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
		}}}
	}
	// disabled unset (no IsEnabled)
	covKUpdate(ctx, t, "job/disabled-null", newR(), &jobResourceModel{
		Name: types.StringValue("j"), Folder: types.StringNull(),
		Template: types.StringValue(covNJobXML), Disabled: types.BoolNull(),
	})
	// disabled managed → refreshMeta calls IsEnabled (Poll /api/json)
	covKUpdate(ctx, t, "job/disabled-managed", newR(), &jobResourceModel{
		Name: types.StringValue("j"), Folder: types.StringNull(),
		Template: types.StringValue(covNJobXML), Disabled: types.BoolValue(false),
	})
}
