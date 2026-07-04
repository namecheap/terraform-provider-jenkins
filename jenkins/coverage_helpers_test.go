package jenkins

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// covcLiveJob starts an httptest server that returns configBody for any request
// and returns a *jenkins.Job wired to a real requester pointing at it, so that
// job.GetConfig (used by the pipeline populate helpers) exercises its happy path
// without a live Jenkins.
func covcLiveJob(t *testing.T, base, configBody string) *jenkins.Job {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(configBody))
	}))
	t.Cleanup(srv.Close)

	client, err := newJenkinsClient(&Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("newJenkinsClient: %v", err)
	}
	return &jenkins.Job{Jenkins: client.Jenkins, Base: base}
}

// --- resource_jenkins_role.go -------------------------------------------------

func TestCovC_RolePopulate(t *testing.T) {
	ctx := context.Background()

	t.Run("global found", func(t *testing.T) {
		r := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetRole: func(_ context.Context, _, _ string, out interface{}) error {
				resp := out.(*roleStrategyRoleResponse)
				resp.PermissionIDs = map[string]bool{"hudson.model.Hudson.Read": true}
				resp.Sids = []struct {
					Type string `json:"type"`
					Sid  string `json:"sid"`
				}{{Type: "USER", Sid: "alice"}}
				return nil
			},
		}}}
		data := &roleResourceModel{Type: types.StringValue("global"), Name: types.StringValue("admins")}
		var diags diag.Diagnostics
		if ok := r.populate(ctx, data, &diags); !ok || diags.HasError() {
			t.Fatalf("populate failed: ok=%v diags=%v", ok, diags)
		}
		if data.ID.ValueString() != "global/admins" {
			t.Errorf("id = %q, want global/admins", data.ID.ValueString())
		}
		if !data.Pattern.IsNull() {
			t.Errorf("expected null pattern for global role, got %v", data.Pattern)
		}
	})

	t.Run("item found keeps pattern", func(t *testing.T) {
		r := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetRole: func(_ context.Context, _, _ string, out interface{}) error {
				resp := out.(*roleStrategyRoleResponse)
				resp.PermissionIDs = map[string]bool{"hudson.model.Item.Build": true}
				resp.Pattern = "team/.*"
				return nil
			},
		}}}
		data := &roleResourceModel{Type: types.StringValue("item"), Name: types.StringValue("dev")}
		var diags diag.Diagnostics
		if ok := r.populate(ctx, data, &diags); !ok || diags.HasError() {
			t.Fatalf("populate failed: ok=%v diags=%v", ok, diags)
		}
		if data.Pattern.ValueString() != "team/.*" {
			t.Errorf("pattern = %q, want team/.*", data.Pattern.ValueString())
		}
	})

	t.Run("empty role reports not found", func(t *testing.T) {
		r := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetRole: func(_ context.Context, _, _ string, _ interface{}) error { return nil },
		}}}
		data := &roleResourceModel{Type: types.StringValue("global"), Name: types.StringValue("ghost")}
		var diags diag.Diagnostics
		if ok := r.populate(ctx, data, &diags); ok {
			t.Error("expected populate to report not found for empty role")
		}
		if diags.HasError() {
			t.Errorf("expected no error diagnostic, got %v", diags)
		}
	})

	t.Run("generic error", func(t *testing.T) {
		r := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetRole: func(_ context.Context, _, _ string, _ interface{}) error { return fmt.Errorf("boom") },
		}}}
		data := &roleResourceModel{Type: types.StringValue("global"), Name: types.StringValue("x")}
		var diags diag.Diagnostics
		if ok := r.populate(ctx, data, &diags); ok {
			t.Error("expected populate to fail on error")
		}
		if !diags.HasError() {
			t.Error("expected error diagnostic on generic error")
		}
	})

	t.Run("invalid role type", func(t *testing.T) {
		r := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{}}}
		data := &roleResourceModel{Type: types.StringValue("nope"), Name: types.StringValue("x")}
		var diags diag.Diagnostics
		if ok := r.populate(ctx, data, &diags); ok {
			t.Error("expected populate to fail on invalid type")
		}
		if !diags.HasError() {
			t.Error("expected error diagnostic on invalid type")
		}
	})
}

func TestCovC_RoleValidateInvalidType(t *testing.T) {
	r := &roleResource{resourceHelper: newResourceHelper()}
	data := &roleResourceModel{Type: types.StringValue("bogus"), Pattern: types.StringNull()}
	var diags diag.Diagnostics
	if _, _, ok := r.validate(data, &diags); ok || !diags.HasError() {
		t.Fatalf("expected validation error for unknown type, ok=%v diags=%v", ok, diags)
	}
}

func TestCovC_RoleAssign(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		var calls int
		r := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockAssignRole: func(_ context.Context, _, _, _ string) error { calls++; return nil },
		}}}
		var diags diag.Diagnostics
		if ok := r.assign(ctx, "globalRoles", "admins", []string{"alice", "bob"}, &diags); !ok || diags.HasError() {
			t.Fatalf("assign failed: ok=%v diags=%v", ok, diags)
		}
		if calls != 2 {
			t.Errorf("AssignRole called %d times, want 2", calls)
		}
	})

	t.Run("error", func(t *testing.T) {
		r := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockAssignRole: func(_ context.Context, _, _, _ string) error { return fmt.Errorf("boom") },
		}}}
		var diags diag.Diagnostics
		if ok := r.assign(ctx, "globalRoles", "admins", []string{"alice"}, &diags); ok {
			t.Error("expected assign to fail")
		}
		if !diags.HasError() {
			t.Error("expected error diagnostic")
		}
	})
}

func TestCovC_SetStrings(t *testing.T) {
	ctx := context.Background()

	t.Run("null", func(t *testing.T) {
		var diags diag.Diagnostics
		if out := setStrings(ctx, types.SetNull(types.StringType), &diags); out != nil {
			t.Errorf("expected nil for null set, got %v", out)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		var diags diag.Diagnostics
		if out := setStrings(ctx, types.SetUnknown(types.StringType), &diags); out != nil {
			t.Errorf("expected nil for unknown set, got %v", out)
		}
	})

	t.Run("populated", func(t *testing.T) {
		var diags diag.Diagnostics
		set, d := types.SetValueFrom(ctx, types.StringType, []string{"a", "b"})
		diags.Append(d...)
		out := setStrings(ctx, set, &diags)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		if len(out) != 2 {
			t.Errorf("got %v, want 2 elements", out)
		}
	})
}

// --- resource_jenkins_user.go (new scenarios only) ---------------------------

func TestCovC_UserPopulate(t *testing.T) {
	ctx := context.Background()

	t.Run("generic error", func(t *testing.T) {
		r := &userResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetUser: func(_ context.Context, _ string, _ interface{}) error { return fmt.Errorf("boom") },
		}}}
		data := &userResourceModel{Username: types.StringValue("alice")}
		var diags diag.Diagnostics
		if ok := r.populate(ctx, data, &diags); ok {
			t.Error("expected populate to fail on generic error")
		}
		if !diags.HasError() {
			t.Error("expected error diagnostic on generic error")
		}
	})

	t.Run("empty id reports not found", func(t *testing.T) {
		r := &userResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetUser: func(_ context.Context, _ string, _ interface{}) error { return nil },
		}}}
		data := &userResourceModel{Username: types.StringValue("ghost")}
		var diags diag.Diagnostics
		if ok := r.populate(ctx, data, &diags); ok {
			t.Error("expected populate to report not found for empty id")
		}
		if diags.HasError() {
			t.Errorf("expected no error diagnostic, got %v", diags)
		}
	})

	t.Run("no email yields null", func(t *testing.T) {
		r := &userResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetUser: func(_ context.Context, _ string, out interface{}) error {
				u := out.(*jenkinsUserResponse)
				u.ID = "bob"
				u.FullName = "Bob"
				return nil
			},
		}}}
		data := &userResourceModel{Username: types.StringValue("bob")}
		var diags diag.Diagnostics
		if ok := r.populate(ctx, data, &diags); !ok || diags.HasError() {
			t.Fatalf("populate failed: ok=%v diags=%v", ok, diags)
		}
		if !data.Email.IsNull() {
			t.Errorf("expected null email, got %v", data.Email)
		}
	})
}

// --- resource_jenkins_plugin.go ----------------------------------------------

func TestCovC_PluginObserve(t *testing.T) {
	ctx := context.Background()

	t.Run("installed unpinned reflects observed version", func(t *testing.T) {
		r := &pluginResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockHasPlugin: func(_ context.Context, _ string) (*jenkins.Plugin, error) {
				return &jenkins.Plugin{Active: true, Version: "5.2.0"}, nil
			},
		}}}
		data := &pluginResourceModel{Name: types.StringValue("git"), Version: types.StringNull()}
		var diags diag.Diagnostics
		r.observe(ctx, data, false, &diags)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		if data.Version.ValueString() != "5.2.0" {
			t.Errorf("version = %q, want 5.2.0", data.Version.ValueString())
		}
		if !data.Active.ValueBool() || data.PendingRestart.ValueBool() {
			t.Errorf("active=%v pending=%v, want active=true pending=false", data.Active, data.PendingRestart)
		}
	})

	t.Run("installed pinned preserves version", func(t *testing.T) {
		r := &pluginResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockHasPlugin: func(_ context.Context, _ string) (*jenkins.Plugin, error) {
				return &jenkins.Plugin{Active: true, Version: "5.2.0"}, nil
			},
		}}}
		data := &pluginResourceModel{Name: types.StringValue("git"), Version: types.StringValue("5.1.0")}
		var diags diag.Diagnostics
		r.observe(ctx, data, true, &diags)
		if data.Version.ValueString() != "5.1.0" {
			t.Errorf("version = %q, want pinned 5.1.0", data.Version.ValueString())
		}
	})

	t.Run("not installed pending restart", func(t *testing.T) {
		r := &pluginResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockHasPlugin: func(_ context.Context, _ string) (*jenkins.Plugin, error) { return nil, nil },
		}}}
		data := &pluginResourceModel{Name: types.StringValue("git"), Version: types.StringNull()}
		var diags diag.Diagnostics
		r.observe(ctx, data, false, &diags)
		if diags.HasError() {
			t.Fatalf("unexpected error diags: %v", diags)
		}
		if !data.PendingRestart.ValueBool() || data.Active.ValueBool() {
			t.Errorf("pending=%v active=%v, want pending=true active=false", data.PendingRestart, data.Active)
		}
		if data.Version.ValueString() != "" {
			t.Errorf("version = %q, want empty", data.Version.ValueString())
		}
		if diags.WarningsCount() == 0 {
			t.Error("expected a pending-restart warning diagnostic")
		}
	})

	t.Run("error", func(t *testing.T) {
		r := &pluginResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockHasPlugin: func(_ context.Context, _ string) (*jenkins.Plugin, error) { return nil, fmt.Errorf("boom") },
		}}}
		data := &pluginResourceModel{Name: types.StringValue("git")}
		var diags diag.Diagnostics
		r.observe(ctx, data, false, &diags)
		if !diags.HasError() {
			t.Error("expected error diagnostic")
		}
	})
}

// --- resource_jenkins_pipeline_job.go ----------------------------------------

func TestCovC_PipelineJobPopulate(t *testing.T) {
	ctx := context.Background()

	t.Run("happy path", func(t *testing.T) {
		model := pipelineJobResourceModel{
			Description: types.StringValue("desc"),
			Script:      types.StringValue("echo 'hi'"),
			Sandbox:     types.BoolValue(true),
			Disabled:    types.BoolValue(false),
		}
		cfg, err := model.configXML()
		if err != nil {
			t.Fatalf("configXML: %v", err)
		}
		job := covcLiveJob(t, "/job/foo", cfg)
		r := &pipelineJobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetJob: func(_ context.Context, _ string, _ ...string) (*jenkins.Job, error) { return job, nil },
		}}}
		data := &pipelineJobResourceModel{Name: types.StringValue("foo")}
		var diags diag.Diagnostics
		if ok := r.populate(ctx, data, &diags); !ok || diags.HasError() {
			t.Fatalf("populate failed: ok=%v diags=%v", ok, diags)
		}
		if data.Script.ValueString() != "echo 'hi'" {
			t.Errorf("script = %q, want echo 'hi'", data.Script.ValueString())
		}
		if data.ID.ValueString() != "/job/foo" {
			t.Errorf("id = %q, want /job/foo", data.ID.ValueString())
		}
	})

	t.Run("not found", func(t *testing.T) {
		r := &pipelineJobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetJob: func(_ context.Context, _ string, _ ...string) (*jenkins.Job, error) {
				return nil, fmt.Errorf("404 not found")
			},
		}}}
		data := &pipelineJobResourceModel{Name: types.StringValue("ghost")}
		var diags diag.Diagnostics
		if ok := r.populate(ctx, data, &diags); ok {
			t.Error("expected populate to report not found")
		}
		if diags.HasError() {
			t.Errorf("expected no error diagnostic on 404, got %v", diags)
		}
	})

	t.Run("generic error", func(t *testing.T) {
		r := &pipelineJobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetJob: func(_ context.Context, _ string, _ ...string) (*jenkins.Job, error) {
				return nil, fmt.Errorf("boom")
			},
		}}}
		data := &pipelineJobResourceModel{Name: types.StringValue("x")}
		var diags diag.Diagnostics
		if ok := r.populate(ctx, data, &diags); ok {
			t.Error("expected populate to fail")
		}
		if !diags.HasError() {
			t.Error("expected error diagnostic on generic error")
		}
	})
}

// --- resource_jenkins_multibranch_pipeline.go --------------------------------

func TestCovC_MultibranchPipelinePopulate(t *testing.T) {
	ctx := context.Background()

	t.Run("happy path", func(t *testing.T) {
		model := multibranchPipelineResourceModel{
			Name:          types.StringValue("svc"),
			Description:   types.StringValue("desc"),
			Remote:        types.StringValue("https://github.com/org/repo.git"),
			CredentialsID: types.StringValue("git-token"),
			ScriptPath:    types.StringValue("Jenkinsfile"),
		}
		cfg, err := model.configXML()
		if err != nil {
			t.Fatalf("configXML: %v", err)
		}
		job := covcLiveJob(t, "/job/svc", cfg)
		r := &multibranchPipelineResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetJob: func(_ context.Context, _ string, _ ...string) (*jenkins.Job, error) { return job, nil },
		}}}
		data := &multibranchPipelineResourceModel{Name: types.StringValue("svc")}
		var diags diag.Diagnostics
		if ok := r.populate(ctx, data, &diags); !ok || diags.HasError() {
			t.Fatalf("populate failed: ok=%v diags=%v", ok, diags)
		}
		if data.Remote.ValueString() != "https://github.com/org/repo.git" {
			t.Errorf("remote = %q", data.Remote.ValueString())
		}
	})

	t.Run("not found", func(t *testing.T) {
		r := &multibranchPipelineResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetJob: func(_ context.Context, _ string, _ ...string) (*jenkins.Job, error) {
				return nil, fmt.Errorf("404 not found")
			},
		}}}
		data := &multibranchPipelineResourceModel{Name: types.StringValue("ghost")}
		var diags diag.Diagnostics
		if ok := r.populate(ctx, data, &diags); ok {
			t.Error("expected populate to report not found")
		}
		if diags.HasError() {
			t.Errorf("expected no error diagnostic on 404, got %v", diags)
		}
	})

	t.Run("generic error", func(t *testing.T) {
		r := &multibranchPipelineResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetJob: func(_ context.Context, _ string, _ ...string) (*jenkins.Job, error) {
				return nil, fmt.Errorf("boom")
			},
		}}}
		data := &multibranchPipelineResourceModel{Name: types.StringValue("x")}
		var diags diag.Diagnostics
		if ok := r.populate(ctx, data, &diags); ok {
			t.Error("expected populate to fail")
		}
		if !diags.HasError() {
			t.Error("expected error diagnostic on generic error")
		}
	})
}

// --- data_source_jenkins_jobs.go (listInnerJobs, shared with folders) --------

func TestCovC_ListInnerJobs(t *testing.T) {
	ctx := context.Background()

	t.Run("root uses GetAllJobNames", func(t *testing.T) {
		client := &mockJenkinsClient{
			mockGetAllJobNames: func(_ context.Context) ([]jenkins.InnerJob, error) {
				return []jenkins.InnerJob{{Name: "a"}, {Name: "b"}}, nil
			},
		}
		got, err := listInnerJobs(ctx, client, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("got %d jobs, want 2", len(got))
		}
	})

	t.Run("folder uses GetFolder", func(t *testing.T) {
		client := &mockJenkinsClient{
			mockGetFolder: func(_ context.Context, _ string, _ ...string) (*jenkins.Folder, error) {
				return &jenkins.Folder{Raw: &jenkins.FolderResponse{Jobs: []jenkins.InnerJob{{Name: "child"}}}}, nil
			},
		}
		got, err := listInnerJobs(ctx, client, "team")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].Name != "child" {
			t.Errorf("got %v, want one job 'child'", got)
		}
	})

	t.Run("folder error", func(t *testing.T) {
		client := &mockJenkinsClient{
			mockGetFolder: func(_ context.Context, _ string, _ ...string) (*jenkins.Folder, error) {
				return nil, fmt.Errorf("boom")
			},
		}
		if _, err := listInnerJobs(ctx, client, "team"); err == nil {
			t.Error("expected error from GetFolder")
		}
	})

	t.Run("folder with nil raw returns nil", func(t *testing.T) {
		client := &mockJenkinsClient{
			mockGetFolder: func(_ context.Context, _ string, _ ...string) (*jenkins.Folder, error) {
				return &jenkins.Folder{}, nil
			},
		}
		got, err := listInnerJobs(ctx, client, "team")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
}

// --- util.go folderExists (helper behind the folder-aware Create paths) -------

func TestCovC_FolderExists(t *testing.T) {
	ctx := context.Background()

	t.Run("empty folder skips lookup", func(t *testing.T) {
		client := &mockJenkinsClient{
			mockGetFolder: func(_ context.Context, _ string, _ ...string) (*jenkins.Folder, error) {
				t.Fatal("GetFolder should not be called for an empty folder")
				return nil, nil
			},
		}
		if err := folderExists(ctx, client, ""); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("existing folder", func(t *testing.T) {
		client := &mockJenkinsClient{
			mockGetFolder: func(_ context.Context, _ string, _ ...string) (*jenkins.Folder, error) {
				return &jenkins.Folder{}, nil
			},
		}
		if err := folderExists(ctx, client, "team/sub"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("missing folder returns error", func(t *testing.T) {
		client := &mockJenkinsClient{
			mockGetFolder: func(_ context.Context, _ string, _ ...string) (*jenkins.Folder, error) {
				return nil, fmt.Errorf("404 not found")
			},
		}
		if err := folderExists(ctx, client, "team"); err == nil {
			t.Error("expected error for missing folder")
		}
	})
}
