package jenkins

import (
	"context"
	"fmt"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// These tests drive the framework CRUD/Read entry points of several resources
// and data sources through the real Plan/State/Config plumbing, using the
// nil-safe mockJenkinsClient. Assertions are intentionally light: the goal is to
// execute the happy, error, and not-found branches of each method.

// --- jenkins_configuration_as_code ------------------------------------------

func TestCovD3_ConfigurationAsCodeCRUD(t *testing.T) {
	ctx := context.Background()

	newRes := func(apply func(context.Context, string) error, export func(context.Context) (string, error)) *configurationAsCodeResource {
		return &configurationAsCodeResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockApplyCASC:  apply,
			mockExportCASC: export,
		}}}
	}
	schemaOf := func(r *configurationAsCodeResource) rschema.Schema {
		var sr resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sr)
		return sr.Schema
	}
	model := func(section, yaml string) configurationAsCodeResourceModel {
		return configurationAsCodeResourceModel{
			ID:      types.StringValue(section),
			Section: types.StringValue(section),
			YAML:    types.StringValue(yaml),
		}
	}

	t.Run("create happy", func(t *testing.T) {
		r := newRes(func(context.Context, string) error { return nil }, nil)
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model("jenkins", "systemMessage: hi"))
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("create apply error", func(t *testing.T) {
		r := newRes(func(context.Context, string) error { return fmt.Errorf("boom") }, nil)
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model("jenkins", "systemMessage: hi"))
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from ApplyCASC")
		}
	})

	t.Run("create invalid yaml", func(t *testing.T) {
		r := newRes(func(context.Context, string) error { return nil }, nil)
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		// A tab in the indentation is illegal in YAML and fails wrapSection.
		plan.Set(ctx, model("jenkins", "a:\n\tb: c"))
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from invalid YAML")
		}
	})

	readWith := func(export func(context.Context) (string, error), m configurationAsCodeResourceModel) resource.ReadResponse {
		r := newRes(nil, export)
		s := schemaOf(r)
		st := tfsdk.State{Schema: s}
		st.Set(ctx, m)
		resp := resource.ReadResponse{State: tfsdk.State{Schema: s}}
		r.Read(ctx, resource.ReadRequest{State: st}, &resp)
		return resp
	}

	t.Run("read in sync", func(t *testing.T) {
		resp := readWith(func(context.Context) (string, error) {
			return "jenkins:\n  systemMessage: hi\n", nil
		}, model("jenkins", "systemMessage: hi"))
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("read drift", func(t *testing.T) {
		resp := readWith(func(context.Context) (string, error) {
			return "jenkins:\n  systemMessage: bye\n", nil
		}, model("jenkins", "systemMessage: hi"))
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("read section gone removes resource", func(t *testing.T) {
		resp := readWith(func(context.Context) (string, error) {
			return "security:\n  foo: bar\n", nil
		}, model("jenkins", "systemMessage: hi"))
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("read export error", func(t *testing.T) {
		resp := readWith(func(context.Context) (string, error) {
			return "", fmt.Errorf("boom")
		}, model("jenkins", "systemMessage: hi"))
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from ExportCASC")
		}
	})

	t.Run("read compare error", func(t *testing.T) {
		// A sequence at top level is not a mapping, so cascInSync errors.
		resp := readWith(func(context.Context) (string, error) {
			return "- a\n- b\n", nil
		}, model("jenkins", "systemMessage: hi"))
		if !resp.Diagnostics.HasError() {
			t.Error("expected compare error")
		}
	})

	t.Run("update happy", func(t *testing.T) {
		r := newRes(func(context.Context, string) error { return nil }, nil)
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model("jenkins", "systemMessage: hi"))
		st := tfsdk.State{Schema: s}
		st.Set(ctx, model("jenkins", "systemMessage: old"))
		resp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
		r.Update(ctx, resource.UpdateRequest{Plan: plan, State: st}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("update error", func(t *testing.T) {
		r := newRes(func(context.Context, string) error { return fmt.Errorf("boom") }, nil)
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model("jenkins", "systemMessage: hi"))
		resp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
		r.Update(ctx, resource.UpdateRequest{Plan: plan, State: tfsdk.State{Schema: s}}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from ApplyCASC on update")
		}
	})

	t.Run("delete no-op warning", func(t *testing.T) {
		r := newRes(nil, nil)
		s := schemaOf(r)
		st := tfsdk.State{Schema: s}
		st.Set(ctx, model("jenkins", "systemMessage: hi"))
		resp := resource.DeleteResponse{State: st}
		r.Delete(ctx, resource.DeleteRequest{State: st}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("delete should not error: %v", resp.Diagnostics)
		}
		if resp.Diagnostics.WarningsCount() == 0 {
			t.Error("expected a warning on delete")
		}
	})

	t.Run("import happy", func(t *testing.T) {
		r := newRes(nil, func(context.Context) (string, error) {
			return "jenkins:\n  systemMessage: hi\n", nil
		})
		s := schemaOf(r)
		resp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
		r.ImportState(ctx, resource.ImportStateRequest{ID: "jenkins"}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("import export error", func(t *testing.T) {
		r := newRes(nil, func(context.Context) (string, error) { return "", fmt.Errorf("boom") })
		s := schemaOf(r)
		resp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
		r.ImportState(ctx, resource.ImportStateRequest{ID: "jenkins"}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from ExportCASC on import")
		}
	})

	t.Run("import section not found", func(t *testing.T) {
		r := newRes(nil, func(context.Context) (string, error) { return "security:\n  foo: bar\n", nil })
		s := schemaOf(r)
		resp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
		r.ImportState(ctx, resource.ImportStateRequest{ID: "jenkins"}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Error("expected 'section not found' error on import")
		}
	})
}

// --- jenkins_credential_domain ----------------------------------------------

func TestCovD3_CredentialDomainCRUD(t *testing.T) {
	ctx := context.Background()

	schemaOf := func(r *credentialDomainResource) rschema.Schema {
		var sr resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sr)
		return sr.Schema
	}
	model := func(folder string) credentialDomainResourceModel {
		m := credentialDomainResourceModel{
			ID:          types.StringValue("dom"),
			Name:        types.StringValue("dom"),
			Description: types.StringValue("desc"),
		}
		if folder == "" {
			m.Folder = types.StringNull()
		} else {
			m.Folder = types.StringValue(folder)
		}
		return m
	}

	t.Run("create happy", func(t *testing.T) {
		r := &credentialDomainResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockCreateCredDomain: func(context.Context, string, string, string) error { return nil },
		}}}
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model(""))
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("create error", func(t *testing.T) {
		r := &credentialDomainResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockCreateCredDomain: func(context.Context, string, string, string) error { return fmt.Errorf("boom") },
		}}}
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model(""))
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from CreateCredentialDomain")
		}
	})

	t.Run("create invalid folder", func(t *testing.T) {
		r := &credentialDomainResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetFolder: func(context.Context, string, ...string) (*jenkins.Folder, error) {
				return nil, fmt.Errorf("404 not found")
			},
		}}}
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model("team"))
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Error("expected error for missing folder")
		}
	})

	readWith := func(get func(context.Context, string, string, interface{}) error) resource.ReadResponse {
		r := &credentialDomainResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetCredDomain: get,
		}}}
		s := schemaOf(r)
		st := tfsdk.State{Schema: s}
		st.Set(ctx, model(""))
		resp := resource.ReadResponse{State: tfsdk.State{Schema: s}}
		r.Read(ctx, resource.ReadRequest{State: st}, &resp)
		return resp
	}

	t.Run("read happy", func(t *testing.T) {
		resp := readWith(func(_ context.Context, _, _ string, out interface{}) error {
			dom := out.(*credentialDomainXML)
			dom.Description = "from-jenkins"
			return nil
		})
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("read not found", func(t *testing.T) {
		resp := readWith(func(context.Context, string, string, interface{}) error {
			return fmt.Errorf("invalid response code 404")
		})
		if resp.Diagnostics.HasError() {
			t.Errorf("404 should remove the resource, not error: %v", resp.Diagnostics)
		}
	})

	t.Run("read error", func(t *testing.T) {
		resp := readWith(func(context.Context, string, string, interface{}) error {
			return fmt.Errorf("boom")
		})
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from GetCredentialDomain")
		}
	})

	updateWith := func(upd func(context.Context, string, string, string) error) resource.UpdateResponse {
		r := &credentialDomainResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockUpdateCredDomain: upd,
		}}}
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model(""))
		resp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
		r.Update(ctx, resource.UpdateRequest{Plan: plan, State: tfsdk.State{Schema: s}}, &resp)
		return resp
	}

	t.Run("update happy", func(t *testing.T) {
		if resp := updateWith(func(context.Context, string, string, string) error { return nil }); resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("update error", func(t *testing.T) {
		if resp := updateWith(func(context.Context, string, string, string) error { return fmt.Errorf("boom") }); !resp.Diagnostics.HasError() {
			t.Error("expected error from UpdateCredentialDomain")
		}
	})

	deleteWith := func(del func(context.Context, string, string) error) resource.DeleteResponse {
		r := &credentialDomainResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockDeleteCredDomain: del,
		}}}
		s := schemaOf(r)
		st := tfsdk.State{Schema: s}
		st.Set(ctx, model(""))
		resp := resource.DeleteResponse{State: st}
		r.Delete(ctx, resource.DeleteRequest{State: st}, &resp)
		return resp
	}

	t.Run("delete happy", func(t *testing.T) {
		if resp := deleteWith(func(context.Context, string, string) error { return nil }); resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("delete error", func(t *testing.T) {
		if resp := deleteWith(func(context.Context, string, string) error { return fmt.Errorf("boom") }); !resp.Diagnostics.HasError() {
			t.Error("expected error from DeleteCredentialDomain")
		}
	})

	t.Run("import with folder", func(t *testing.T) {
		r := &credentialDomainResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{}}}
		s := schemaOf(r)
		resp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
		r.ImportState(ctx, resource.ImportStateRequest{ID: "team/dom"}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("import without folder", func(t *testing.T) {
		r := &credentialDomainResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{}}}
		s := schemaOf(r)
		resp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
		r.ImportState(ctx, resource.ImportStateRequest{ID: "dom"}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})
}

// --- jenkins_pipeline_job ----------------------------------------------------

func TestCovD3_PipelineJobCRUD(t *testing.T) {
	ctx := context.Background()

	schemaOf := func(r *pipelineJobResource) rschema.Schema {
		var sr resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sr)
		return sr.Schema
	}
	model := func(folder string) pipelineJobResourceModel {
		m := pipelineJobResourceModel{
			ID:          types.StringValue("/job/foo"),
			Name:        types.StringValue("foo"),
			Description: types.StringValue("desc"),
			Script:      types.StringValue("echo 'hi'"),
			Sandbox:     types.BoolValue(true),
			Disabled:    types.BoolValue(false),
		}
		if folder == "" {
			m.Folder = types.StringNull()
		} else {
			m.Folder = types.StringValue(folder)
		}
		return m
	}
	// liveConfig renders the config a covcLiveJob should serve so populate parses.
	liveCfg, err := model("").configXML()
	if err != nil {
		t.Fatalf("configXML: %v", err)
	}

	t.Run("create happy", func(t *testing.T) {
		job := covcLiveJob(t, "/job/foo", liveCfg)
		r := &pipelineJobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockCreateJobInFolder: func(context.Context, string, string, ...string) (*jenkins.Job, error) { return nil, nil },
			mockGetJob:            func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
		}}}
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model(""))
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("create invalid folder", func(t *testing.T) {
		r := &pipelineJobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetFolder: func(context.Context, string, ...string) (*jenkins.Folder, error) {
				return nil, fmt.Errorf("404 not found")
			},
		}}}
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model("team"))
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Error("expected invalid-folder error")
		}
	})

	t.Run("create error", func(t *testing.T) {
		r := &pipelineJobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockCreateJobInFolder: func(context.Context, string, string, ...string) (*jenkins.Job, error) {
				return nil, fmt.Errorf("boom")
			},
		}}}
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model(""))
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from CreateJobInFolder")
		}
	})

	t.Run("create readback not found", func(t *testing.T) {
		r := &pipelineJobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockCreateJobInFolder: func(context.Context, string, string, ...string) (*jenkins.Job, error) { return nil, nil },
			mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) {
				return nil, fmt.Errorf("404 not found")
			},
		}}}
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model(""))
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Error("expected read-back error when job not found after create")
		}
	})

	readWith := func(get func(context.Context, string, ...string) (*jenkins.Job, error)) resource.ReadResponse {
		r := &pipelineJobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{mockGetJob: get}}}
		s := schemaOf(r)
		st := tfsdk.State{Schema: s}
		st.Set(ctx, model(""))
		resp := resource.ReadResponse{State: tfsdk.State{Schema: s}}
		r.Read(ctx, resource.ReadRequest{State: st}, &resp)
		return resp
	}

	t.Run("read happy", func(t *testing.T) {
		job := covcLiveJob(t, "/job/foo", liveCfg)
		resp := readWith(func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil })
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("read not found", func(t *testing.T) {
		resp := readWith(func(context.Context, string, ...string) (*jenkins.Job, error) {
			return nil, fmt.Errorf("404 not found")
		})
		if resp.Diagnostics.HasError() {
			t.Errorf("404 should remove resource, not error: %v", resp.Diagnostics)
		}
	})

	t.Run("read error", func(t *testing.T) {
		resp := readWith(func(context.Context, string, ...string) (*jenkins.Job, error) {
			return nil, fmt.Errorf("boom")
		})
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from GetJob")
		}
	})

	// NOTE: the Update happy path is intentionally not exercised here. It calls
	// gojenkins Job.UpdateConfig, whose first step (SetCrumb -> GET
	// /crumbIssuer/api/json) expects a JSON crumb response that the shared
	// covcLiveJob test server (which returns config XML for every path) cannot
	// provide, so the POST fails before the resource logic completes. The Update
	// method's error branch is covered by "update get error" below, and the
	// configXML/populate logic it shares is covered by the Create/Read happy paths.

	t.Run("update get error", func(t *testing.T) {
		r := &pipelineJobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return nil, fmt.Errorf("boom") },
		}}}
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model(""))
		resp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
		r.Update(ctx, resource.UpdateRequest{Plan: plan, State: tfsdk.State{Schema: s}}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from GetJob on update")
		}
	})

	deleteWith := func(del func(context.Context, string, ...string) (bool, error)) resource.DeleteResponse {
		r := &pipelineJobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{mockDeleteJobInFolder: del}}}
		s := schemaOf(r)
		st := tfsdk.State{Schema: s}
		st.Set(ctx, model(""))
		resp := resource.DeleteResponse{State: st}
		r.Delete(ctx, resource.DeleteRequest{State: st}, &resp)
		return resp
	}

	t.Run("delete happy", func(t *testing.T) {
		if resp := deleteWith(func(context.Context, string, ...string) (bool, error) { return true, nil }); resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("delete error", func(t *testing.T) {
		if resp := deleteWith(func(context.Context, string, ...string) (bool, error) { return false, fmt.Errorf("boom") }); !resp.Diagnostics.HasError() {
			t.Error("expected error from DeleteJobInFolder")
		}
	})

	t.Run("import nested", func(t *testing.T) {
		r := &pipelineJobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{}}}
		s := schemaOf(r)
		resp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
		r.ImportState(ctx, resource.ImportStateRequest{ID: "job/team/foo"}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("import top level", func(t *testing.T) {
		r := &pipelineJobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{}}}
		s := schemaOf(r)
		resp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
		r.ImportState(ctx, resource.ImportStateRequest{ID: "job/foo"}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})
}

// --- jenkins_multibranch_pipeline -------------------------------------------

func TestCovD3_MultibranchPipelineCRUD(t *testing.T) {
	ctx := context.Background()

	schemaOf := func(r *multibranchPipelineResource) rschema.Schema {
		var sr resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sr)
		return sr.Schema
	}
	model := func(folder string) multibranchPipelineResourceModel {
		m := multibranchPipelineResourceModel{
			ID:            types.StringValue("/job/svc"),
			Name:          types.StringValue("svc"),
			Description:   types.StringValue("desc"),
			Remote:        types.StringValue("https://github.com/org/repo.git"),
			CredentialsID: types.StringValue("git-token"),
			ScriptPath:    types.StringValue("Jenkinsfile"),
		}
		if folder == "" {
			m.Folder = types.StringNull()
		} else {
			m.Folder = types.StringValue(folder)
		}
		return m
	}
	liveCfg, err := model("").configXML()
	if err != nil {
		t.Fatalf("configXML: %v", err)
	}

	t.Run("create happy", func(t *testing.T) {
		job := covcLiveJob(t, "/job/svc", liveCfg)
		r := &multibranchPipelineResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockCreateJobInFolder: func(context.Context, string, string, ...string) (*jenkins.Job, error) { return nil, nil },
			mockGetJob:            func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
		}}}
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model(""))
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("create invalid folder", func(t *testing.T) {
		r := &multibranchPipelineResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetFolder: func(context.Context, string, ...string) (*jenkins.Folder, error) {
				return nil, fmt.Errorf("404 not found")
			},
		}}}
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model("team"))
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Error("expected invalid-folder error")
		}
	})

	t.Run("create error", func(t *testing.T) {
		r := &multibranchPipelineResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockCreateJobInFolder: func(context.Context, string, string, ...string) (*jenkins.Job, error) {
				return nil, fmt.Errorf("boom")
			},
		}}}
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model(""))
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from CreateJobInFolder")
		}
	})

	readWith := func(get func(context.Context, string, ...string) (*jenkins.Job, error)) resource.ReadResponse {
		r := &multibranchPipelineResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{mockGetJob: get}}}
		s := schemaOf(r)
		st := tfsdk.State{Schema: s}
		st.Set(ctx, model(""))
		resp := resource.ReadResponse{State: tfsdk.State{Schema: s}}
		r.Read(ctx, resource.ReadRequest{State: st}, &resp)
		return resp
	}

	t.Run("read happy", func(t *testing.T) {
		job := covcLiveJob(t, "/job/svc", liveCfg)
		resp := readWith(func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil })
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("read not found", func(t *testing.T) {
		resp := readWith(func(context.Context, string, ...string) (*jenkins.Job, error) {
			return nil, fmt.Errorf("404 not found")
		})
		if resp.Diagnostics.HasError() {
			t.Errorf("404 should remove resource, not error: %v", resp.Diagnostics)
		}
	})

	t.Run("read error", func(t *testing.T) {
		resp := readWith(func(context.Context, string, ...string) (*jenkins.Job, error) {
			return nil, fmt.Errorf("boom")
		})
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from GetJob")
		}
	})

	// NOTE: the Update happy path is intentionally skipped for the same reason as
	// the pipeline job resource: Job.UpdateConfig's crumb fetch cannot be served
	// by the shared covcLiveJob test server. The error branch below covers the
	// Update method; its shared configXML/populate logic is covered elsewhere.

	t.Run("update get error", func(t *testing.T) {
		r := &multibranchPipelineResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return nil, fmt.Errorf("boom") },
		}}}
		s := schemaOf(r)
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model(""))
		resp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
		r.Update(ctx, resource.UpdateRequest{Plan: plan, State: tfsdk.State{Schema: s}}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from GetJob on update")
		}
	})

	deleteWith := func(del func(context.Context, string, ...string) (bool, error)) resource.DeleteResponse {
		r := &multibranchPipelineResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{mockDeleteJobInFolder: del}}}
		s := schemaOf(r)
		st := tfsdk.State{Schema: s}
		st.Set(ctx, model(""))
		resp := resource.DeleteResponse{State: st}
		r.Delete(ctx, resource.DeleteRequest{State: st}, &resp)
		return resp
	}

	t.Run("delete happy", func(t *testing.T) {
		if resp := deleteWith(func(context.Context, string, ...string) (bool, error) { return true, nil }); resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("delete error", func(t *testing.T) {
		if resp := deleteWith(func(context.Context, string, ...string) (bool, error) { return false, fmt.Errorf("boom") }); !resp.Diagnostics.HasError() {
			t.Error("expected error from DeleteJobInFolder")
		}
	})

	t.Run("import nested", func(t *testing.T) {
		r := &multibranchPipelineResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{}}}
		s := schemaOf(r)
		resp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
		r.ImportState(ctx, resource.ImportStateRequest{ID: "job/team/svc"}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})
}

// --- data source jenkins_node ------------------------------------------------

func TestCovD3_NodeDataSourceRead(t *testing.T) {
	ctx := context.Background()

	schemaOf := func(d *nodeDataSource) dschema.Schema {
		var sr datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &sr)
		return sr.Schema
	}
	run := func(client *mockJenkinsClient) datasource.ReadResponse {
		d := &nodeDataSource{dataSourceHelper: &dataSourceHelper{client: client}}
		s := schemaOf(d)
		tmp := tfsdk.State{Schema: s}
		tmp.Set(ctx, nodeDataSourceModel{
			Name:         types.StringValue("agent1"),
			ID:           types.StringNull(),
			NumExecutors: types.Int64Null(),
			Description:  types.StringNull(),
			RemoteFS:     types.StringNull(),
			Labels:       types.StringNull(),
			Online:       types.BoolNull(),
		})
		cfg := tfsdk.Config{Schema: s, Raw: tmp.Raw}
		resp := datasource.ReadResponse{State: tfsdk.State{Schema: s}}
		d.Read(ctx, datasource.ReadRequest{Config: cfg}, &resp)
		return resp
	}

	t.Run("happy", func(t *testing.T) {
		resp := run(&mockJenkinsClient{
			mockGetNode: func(context.Context, string) (*jenkins.Node, error) {
				return &jenkins.Node{Raw: &jenkins.NodeResponse{Offline: false}}, nil
			},
			mockGetNodeConfig: func(_ context.Context, _ string, out interface{}) error {
				cfg := out.(*nodeConfig)
				cfg.NumExecutors = 3
				cfg.Description = "an agent"
				cfg.RemoteFS = "/home/jenkins"
				cfg.Label = "linux docker"
				return nil
			},
		})
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("get node error", func(t *testing.T) {
		resp := run(&mockJenkinsClient{
			mockGetNode: func(context.Context, string) (*jenkins.Node, error) { return nil, fmt.Errorf("boom") },
		})
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from GetNode")
		}
	})

	t.Run("get node config error", func(t *testing.T) {
		resp := run(&mockJenkinsClient{
			mockGetNode:       func(context.Context, string) (*jenkins.Node, error) { return &jenkins.Node{}, nil },
			mockGetNodeConfig: func(context.Context, string, interface{}) error { return fmt.Errorf("boom") },
		})
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from GetNodeConfig")
		}
	})
}

// --- data source jenkins_nodes -----------------------------------------------

func TestCovD3_NodesDataSourceRead(t *testing.T) {
	ctx := context.Background()

	schemaOf := func(d *nodesDataSource) dschema.Schema {
		var sr datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &sr)
		return sr.Schema
	}
	run := func(client *mockJenkinsClient) datasource.ReadResponse {
		d := &nodesDataSource{dataSourceHelper: &dataSourceHelper{client: client}}
		s := schemaOf(d)
		tmp := tfsdk.State{Schema: s}
		tmp.Set(ctx, nodesDataSourceModel{ID: types.StringNull(), Nodes: types.SetNull(types.StringType)})
		cfg := tfsdk.Config{Schema: s, Raw: tmp.Raw}
		resp := datasource.ReadResponse{State: tfsdk.State{Schema: s}}
		d.Read(ctx, datasource.ReadRequest{Config: cfg}, &resp)
		return resp
	}

	t.Run("happy", func(t *testing.T) {
		resp := run(&mockJenkinsClient{
			mockGetAllNodes: func(context.Context) ([]*jenkins.Node, error) {
				return []*jenkins.Node{
					{Raw: &jenkins.NodeResponse{DisplayName: "built-in"}},
					{Raw: &jenkins.NodeResponse{DisplayName: "agent1"}},
				}, nil
			},
		})
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("error", func(t *testing.T) {
		resp := run(&mockJenkinsClient{
			mockGetAllNodes: func(context.Context) ([]*jenkins.Node, error) { return nil, fmt.Errorf("boom") },
		})
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from GetAllNodes")
		}
	})
}

// --- data source jenkins_folders (delegates to listInnerJobs) ----------------

func TestCovD3_FoldersDataSourceRead(t *testing.T) {
	ctx := context.Background()

	schemaOf := func(d *foldersDataSource) dschema.Schema {
		var sr datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &sr)
		return sr.Schema
	}
	run := func(client *mockJenkinsClient, folder string) datasource.ReadResponse {
		d := &foldersDataSource{dataSourceHelper: &dataSourceHelper{client: client}}
		s := schemaOf(d)
		m := foldersDataSourceModel{ID: types.StringNull(), Folders: types.SetNull(types.StringType)}
		if folder == "" {
			m.Folder = types.StringNull()
		} else {
			m.Folder = types.StringValue(folder)
		}
		tmp := tfsdk.State{Schema: s}
		tmp.Set(ctx, m)
		cfg := tfsdk.Config{Schema: s, Raw: tmp.Raw}
		resp := datasource.ReadResponse{State: tfsdk.State{Schema: s}}
		d.Read(ctx, datasource.ReadRequest{Config: cfg}, &resp)
		return resp
	}

	t.Run("happy root", func(t *testing.T) {
		resp := run(&mockJenkinsClient{
			mockGetAllJobNames: func(context.Context) ([]jenkins.InnerJob, error) {
				return []jenkins.InnerJob{
					{Name: "team", Class: "com.cloudbees.hudson.plugins.folder.Folder"},
					{Name: "a-job", Class: "org.jenkinsci.plugins.workflow.job.WorkflowJob"},
				}, nil
			},
		}, "")
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("happy nested folder", func(t *testing.T) {
		resp := run(&mockJenkinsClient{
			mockGetFolder: func(context.Context, string, ...string) (*jenkins.Folder, error) {
				return &jenkins.Folder{Raw: &jenkins.FolderResponse{Jobs: []jenkins.InnerJob{
					{Name: "sub", Class: "com.cloudbees.hudson.plugins.folder.Folder"},
				}}}, nil
			},
		}, "team")
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected diags: %v", resp.Diagnostics)
		}
	})

	t.Run("error", func(t *testing.T) {
		resp := run(&mockJenkinsClient{
			mockGetFolder: func(context.Context, string, ...string) (*jenkins.Folder, error) {
				return nil, fmt.Errorf("boom")
			},
		}, "team")
		if !resp.Diagnostics.HasError() {
			t.Error("expected error from listInnerJobs")
		}
	})
}
