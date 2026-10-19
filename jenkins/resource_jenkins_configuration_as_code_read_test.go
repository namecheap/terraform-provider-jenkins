package jenkins

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflogtest"
)

// The values JCasC resolves ${LAB_SECRET} to in the export: plaintext for a
// String-typed field, ciphertext for a Secret-typed one. Neither may reach
// state (docs/design/casc.md Q3, #249).
const (
	cascTestResolvedSecret = "dummy-lab-secret-0001"
	cascTestCiphertext     = "{AQAAABAAAAAgZHVtbXktbGFiLWNpcGhlcnRleHQ=}"
)

const cascTestDeclared = `location:
  adminAddress: "${LAB_SECRET}"
  url: "http://jenkins:8080/"
mailer:
  smtpHost: "smtp.example.invalid"
  authentication:
    username: "mailer"
    password: "${LAB_SECRET}"
`

// cascTestExport is a full export whose unclassified section resolved both
// placeholders and carries keys the resource does not manage.
func cascTestExport(url string) string {
	return `jenkins:
  systemMessage: hi
unclassified:
  location:
    adminAddress: "` + cascTestResolvedSecret + `"
    url: "` + url + `"
  mailer:
    smtpHost: smtp.example.invalid
    charset: UTF-8
    useSsl: false
    authentication:
      username: mailer
      password: "` + cascTestCiphertext + `"
  unmanagedPlugin:
    apiToken: unmanaged-token-value
`
}

func cascTestResource(export string) (*configurationAsCodeResource, rschema.Schema) {
	r := &configurationAsCodeResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockExportCASC: func(context.Context) (string, error) { return export, nil },
	}}}
	var sr resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sr)
	return r, sr.Schema
}

func TestConfigurationAsCodeReadDriftView(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name     string
		declared string
		export   string
		// want is the expected state yaml, compared semantically. Ignored
		// when inSync or removed is set.
		want           string
		inSync         bool
		removed        bool
		mustNotContain []string
	}{
		{
			name:     "in sync export keeps the declared document",
			declared: cascTestDeclared,
			export:   cascTestExport("http://jenkins:8080/"),
			inSync:   true,
		},
		{
			name:     "drift on an unrelated key keeps placeholders and drops unmanaged keys",
			declared: cascTestDeclared,
			export:   cascTestExport("http://changed.example:8080/"),
			want: `location:
  adminAddress: "${LAB_SECRET}"
  url: "http://changed.example:8080/"
mailer:
  smtpHost: "smtp.example.invalid"
  authentication:
    username: "mailer"
    password: "${LAB_SECRET}"
`,
			mustNotContain: []string{cascTestResolvedSecret, cascTestCiphertext, "charset", "useSsl", "unmanagedPlugin", "unmanaged-token-value"},
		},
		{
			name: "sequence element change copies only the declared keys",
			declared: `globalLibraries:
  libraries:
    - name: shared-pipeline
      defaultVersion: main
`,
			export: `unclassified:
  globalLibraries:
    libraries:
      - name: shared-pipeline
        defaultVersion: release
        implicit: false
        retriever:
          modernSCM:
            scm:
              git:
                credentialsId: shared-pipeline-token
  location:
    adminAddress: "` + cascTestResolvedSecret + `"
`,
			want: `globalLibraries:
  libraries:
    - name: shared-pipeline
      defaultVersion: release
`,
			mustNotContain: []string{cascTestResolvedSecret, "implicit", "retriever", "shared-pipeline-token"},
		},
		{
			// Positional copying would put the resolved token under b's
			// declared plain value once the controller reorders the list.
			name: "reordered sequence holding a placeholder is not copied",
			declared: `items:
  - name: a
    token: "${A_TOKEN}"
  - name: b
    token: not-secret
`,
			export: `unclassified:
  items:
    - name: b
      token: not-secret
    - name: a
      token: resolved-a-token
`,
			mustNotContain: []string{"resolved-a-token"},
		},
		{
			// A literal declared for a Secret-typed field never matches the
			// exported ciphertext, so the view takes the ciphertext as a
			// drifted plain scalar. The literal is already in the
			// configuration and state, and the export never holds the
			// plaintext; ${...} keeps both out.
			name: "literal for a Secret field takes the exported ciphertext",
			declared: `location:
  adminAddress: "${LAB_SECRET}"
mailer:
  authentication:
    password: "dummy-literal-password"
`,
			export: cascTestExport("http://jenkins:8080/"),
			want: `location:
  adminAddress: "${LAB_SECRET}"
mailer:
  authentication:
    password: "` + cascTestCiphertext + `"
`,
			mustNotContain: []string{cascTestResolvedSecret, "url", "username", "smtpHost", "unmanagedPlugin", "unmanaged-token-value"},
		},
		{
			name:     "missing section removes the resource",
			declared: cascTestDeclared,
			export:   "jenkins:\n  systemMessage: hi\n",
			removed:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, s := cascTestResource(tc.export)
			st := tfsdk.State{Schema: s}
			st.Set(ctx, configurationAsCodeResourceModel{
				ID:      types.StringValue("unclassified"),
				Section: types.StringValue("unclassified"),
				YAML:    types.StringValue(tc.declared),
			})
			resp := resource.ReadResponse{State: st}
			r.Read(ctx, resource.ReadRequest{State: st}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diags: %v", resp.Diagnostics)
			}

			if tc.removed {
				if !resp.State.Raw.IsNull() {
					t.Fatalf("expected the resource to be removed, state = %s", resp.State.Raw)
				}
				return
			}

			assertCASCStateOmits(t, resp.State, tc.mustNotContain...)
			got := cascStateYAML(t, resp.State)
			if tc.inSync {
				if got != tc.declared {
					t.Errorf("in-sync Read rewrote yaml:\n got: %q\nwant: %q", got, tc.declared)
				}
				return
			}

			gotDoc, declaredDoc := mustParseYAML(t, got), mustParseYAML(t, tc.declared)
			if reflect.DeepEqual(gotDoc, declaredDoc) {
				t.Errorf("drift must leave yaml different from the declared document so a plan diff appears; got:\n%s", got)
			}
			if tc.want != "" {
				if wantDoc := mustParseYAML(t, tc.want); !reflect.DeepEqual(gotDoc, wantDoc) {
					t.Errorf("drift view mismatch:\n got: %#v\nwant: %#v", gotDoc, wantDoc)
				}
			}
		})
	}
}

// TestConfigurationAsCodeReadDriftViewStableAcrossRefreshes feeds the state a
// drifted Read stored back into Read, as the next plan does. The second
// refresh compares the view, not the configuration, against the export: a
// value hidden by the first refresh must stay hidden, and the view must not
// change.
func TestConfigurationAsCodeReadDriftViewStableAcrossRefreshes(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name           string
		declared       string
		export         string
		mustNotContain []string
	}{
		{
			name:     "embedded placeholder",
			declared: "location:\n  adminAddress: \"ops <${LAB_SECRET}@example.invalid>\"\n",
			export: `unclassified:
  location:
    adminAddress: "ops <` + cascTestResolvedSecret + `@example.invalid>"
    url: "http://jenkins:8080/"
`,
			mustNotContain: []string{cascTestResolvedSecret, "http://jenkins:8080/"},
		},
		{
			name: "multi-line string with an embedded placeholder",
			declared: `scriptApproval:
  script: |
    echo start
    curl -u admin:${LAB_SECRET} http://x/
`,
			export: `unclassified:
  scriptApproval:
    script: "echo start\ncurl -u admin:` + cascTestResolvedSecret + ` http://x/\n"
`,
			mustNotContain: []string{cascTestResolvedSecret, "echo start"},
		},
		{
			name: "reordered sequence holding a placeholder",
			declared: `items:
  - name: a
    token: "${A_TOKEN}"
  - name: b
    token: not-secret
`,
			export: `unclassified:
  items:
    - name: b
      token: not-secret
    - name: a
      token: resolved-a-token
`,
			mustNotContain: []string{"resolved-a-token"},
		},
		{
			name:     "scalar replaced by a mapping holding ciphertext",
			declared: "mailer:\n  authentication: none\n",
			export: `unclassified:
  mailer:
    authentication:
      username: mailer
      password: "` + cascTestCiphertext + `"
`,
			mustNotContain: []string{cascTestCiphertext, "username"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, s := cascTestResource(tc.export)
			st := tfsdk.State{Schema: s}
			st.Set(ctx, configurationAsCodeResourceModel{
				ID:      types.StringValue("unclassified"),
				Section: types.StringValue("unclassified"),
				YAML:    types.StringValue(tc.declared),
			})

			var views []string
			for refresh := 1; refresh <= 2; refresh++ {
				resp := resource.ReadResponse{State: st}
				r.Read(ctx, resource.ReadRequest{State: st}, &resp)
				if resp.Diagnostics.HasError() {
					t.Fatalf("refresh %d: unexpected diags: %v", refresh, resp.Diagnostics)
				}
				if resp.State.Raw.IsNull() {
					t.Fatalf("refresh %d removed the resource", refresh)
				}
				assertCASCStateOmits(t, resp.State, tc.mustNotContain...)
				views = append(views, cascStateYAML(t, resp.State))
				st = resp.State
			}

			if reflect.DeepEqual(mustParseYAML(t, views[0]), mustParseYAML(t, tc.declared)) {
				t.Errorf("drift must leave yaml different from the declared document; got:\n%s", views[0])
			}
			if !reflect.DeepEqual(mustParseYAML(t, views[1]), mustParseYAML(t, views[0])) {
				t.Errorf("second refresh changed the view:\n first: %s\nsecond: %s", views[0], views[1])
			}
		})
	}
}

func TestConfigurationAsCodeImportStateOmitsExport(t *testing.T) {
	ctx := context.Background()
	r, s := cascTestResource(cascTestExport("http://jenkins:8080/"))

	imp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: covMNull(ctx, s)}}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "unclassified"}, &imp)
	if imp.Diagnostics.HasError() {
		t.Fatalf("unexpected import diags: %v", imp.Diagnostics)
	}
	assertCASCStateOmits(t, imp.State, cascTestResolvedSecret, cascTestCiphertext, "unmanagedPlugin")

	var section, id, yaml types.String
	imp.State.GetAttribute(ctx, path.Root("section"), &section)
	imp.State.GetAttribute(ctx, path.Root("id"), &id)
	imp.State.GetAttribute(ctx, path.Root("yaml"), &yaml)
	if section.ValueString() != "unclassified" || id.ValueString() != "unclassified" {
		t.Errorf("section/id = %q/%q, want unclassified", section.ValueString(), id.ValueString())
	}
	if !yaml.IsNull() {
		t.Errorf("import must not store the exported section; yaml = %q", yaml.ValueString())
	}

	// The Read that follows an import must not fill yaml from the export
	// either; the first plan then shows the configured yaml being applied.
	read := resource.ReadResponse{State: imp.State}
	r.Read(ctx, resource.ReadRequest{State: imp.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("unexpected read diags: %v", read.Diagnostics)
	}
	assertCASCStateOmits(t, read.State, cascTestResolvedSecret, cascTestCiphertext, "unmanagedPlugin")
	read.State.GetAttribute(ctx, path.Root("yaml"), &yaml)
	if !yaml.IsNull() {
		t.Errorf("Read after import must keep yaml null; yaml = %q", yaml.ValueString())
	}
}

// TestConfigurationAsCodeExportParseErrorOmitsExport feeds Read and ImportState
// an export that cannot be parsed. The diagnostic must say what failed without
// echoing the export, which holds resolved secrets and unmanaged keys, and
// nothing from it may be logged.
func TestConfigurationAsCodeExportParseErrorOmitsExport(t *testing.T) {
	leaks := []string{cascTestResolvedSecret, cascTestCiphertext, "unmanaged-token-value"}
	cases := []struct {
		name   string
		export string
	}{
		{
			name:   "unterminated quoted value",
			export: "unclassified:\n  location:\n    adminAddress: \"" + cascTestResolvedSecret + "\n  unmanagedPlugin:\n    apiToken: unmanaged-token-value\n",
		},
		{
			name:   "tab indentation",
			export: "unclassified:\n\tadminAddress: " + cascTestResolvedSecret + "\n",
		},
		{
			name:   "misaligned key",
			export: "unclassified:\n  location:\n    adminAddress: " + cascTestResolvedSecret + "\n   apiToken: unmanaged-token-value\n",
		},
		{
			name:   "unknown escape in a quoted value",
			export: "unclassified:\n  mailer:\n    password: \"" + cascTestCiphertext + "\\q\"\n",
		},
		{
			name:   "control character",
			export: "unclassified:\n  location:\n    adminAddress: " + cascTestResolvedSecret + "\x01\n",
		},
		{
			name:   "HTML page instead of YAML",
			export: "<html><body>" + cascTestResolvedSecret + " unmanaged-token-value</body></html>\n",
		},
		{
			name:   "top-level sequence",
			export: "- " + cascTestResolvedSecret + "\n- unmanaged-token-value\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			ctx := tflogtest.RootLogger(context.Background(), &logs)
			r, s := cascTestResource(tc.export)

			st := tfsdk.State{Schema: s}
			st.Set(ctx, configurationAsCodeResourceModel{
				ID:      types.StringValue("unclassified"),
				Section: types.StringValue("unclassified"),
				YAML:    types.StringValue(cascTestDeclared),
			})
			read := resource.ReadResponse{State: st}
			r.Read(ctx, resource.ReadRequest{State: st}, &read)
			if !read.Diagnostics.HasError() {
				t.Fatalf("Read accepted a malformed export; state = %s", read.State.Raw)
			}
			assertCASCDiagsOmit(t, "Read", read.Diagnostics, leaks...)
			assertCASCStateOmits(t, read.State, leaks...)

			imp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: covMNull(ctx, s)}}
			r.ImportState(ctx, resource.ImportStateRequest{ID: "unclassified"}, &imp)
			if !imp.Diagnostics.HasError() {
				t.Fatalf("ImportState accepted a malformed export; state = %s", imp.State.Raw)
			}
			assertCASCDiagsOmit(t, "ImportState", imp.Diagnostics, leaks...)
			assertCASCStateOmits(t, imp.State, leaks...)

			// Read stops at cascInSync, so check cascDriftView's error too.
			if _, _, err := cascDriftView(cascTestDeclared, tc.export, "unclassified"); err == nil {
				t.Error("cascDriftView accepted a malformed export")
			} else {
				assertCASCOmits(t, "cascDriftView error", err.Error(), leaks...)
			}

			assertCASCOmits(t, "logs", logs.String(), leaks...)
		})
	}
}

// TestConfigurationAsCodeLogsOmitResolvedValues drives Create, a drifted Read,
// a Read of the stored view, Update, ImportState and the Read after it through
// the real client against an export holding resolved secrets and unmanaged
// keys, with the provider logger capturing everything. None of the exported
// values may be logged.
func TestConfigurationAsCodeLogsOmitResolvedValues(t *testing.T) {
	var logs bytes.Buffer
	ctx := tflogtest.RootLogger(context.Background(), &logs)

	export := cascTestExport("http://changed.example:8080/")
	var applied []string
	srv := cascTestServer(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/configuration-as-code/export":
			w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
			_, _ = io.WriteString(w, export)
		case "/configuration-as-code/configure":
			b, _ := io.ReadAll(r.Body)
			applied = append(applied, string(b))
			_, _ = io.WriteString(w, "Configuration successfully applied.")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer srv.Close()
	c, err := newJenkinsClient(&Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("newJenkinsClient error = %v", err)
	}
	r := &configurationAsCodeResource{resourceHelper: &resourceHelper{client: c}}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	s := sr.Schema

	model := configurationAsCodeResourceModel{
		Section: types.StringValue("unclassified"),
		YAML:    types.StringValue(cascTestDeclared),
	}
	plan := tfsdk.Plan{Schema: s}
	plan.Set(ctx, model)

	create := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &create)
	if create.Diagnostics.HasError() {
		t.Fatalf("Create: %v", create.Diagnostics)
	}

	st := create.State
	for refresh := 1; refresh <= 2; refresh++ {
		read := resource.ReadResponse{State: st}
		r.Read(ctx, resource.ReadRequest{State: st}, &read)
		if read.Diagnostics.HasError() {
			t.Fatalf("Read %d: %v", refresh, read.Diagnostics)
		}
		st = read.State
	}

	update := resource.UpdateResponse{State: st}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: st}, &update)
	if update.Diagnostics.HasError() {
		t.Fatalf("Update: %v", update.Diagnostics)
	}

	imp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: covMNull(ctx, s)}}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "unclassified"}, &imp)
	if imp.Diagnostics.HasError() {
		t.Fatalf("ImportState: %v", imp.Diagnostics)
	}
	read := resource.ReadResponse{State: imp.State}
	r.Read(ctx, resource.ReadRequest{State: imp.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("Read after import: %v", read.Diagnostics)
	}

	if len(applied) != 2 {
		t.Fatalf("configure called %d times, want 2 (Create, Update)", len(applied))
	}
	for i, doc := range applied {
		assertCASCOmits(t, fmt.Sprintf("applied document %d", i+1), doc, cascTestResolvedSecret, cascTestCiphertext)
	}

	// The capture works: Read's debug line is there.
	got := logs.String()
	if !strings.Contains(got, "configurationAsCodeResource.Read") {
		t.Fatalf("provider logs were not captured:\n%s", got)
	}
	assertCASCOmits(t, "logs", got, cascTestResolvedSecret, cascTestCiphertext, "unmanagedPlugin", "unmanaged-token-value", "charset")
}

// assertCASCDiagsOmit fails if any diagnostic's summary or detail contains one
// of values.
func assertCASCDiagsOmit(t *testing.T, label string, diags diag.Diagnostics, values ...string) {
	t.Helper()
	for _, d := range diags {
		assertCASCOmits(t, label+" diagnostic", d.Summary()+"\n"+d.Detail(), values...)
	}
}

// assertCASCOmits fails if text contains any of values.
func assertCASCOmits(t *testing.T, label, text string, values ...string) {
	t.Helper()
	for _, v := range values {
		if strings.Contains(text, v) {
			t.Errorf("%s contains %q:\n%s", label, v, text)
		}
	}
}

func cascStateYAML(t *testing.T, st tfsdk.State) string {
	t.Helper()
	var v types.String
	if diags := st.GetAttribute(context.Background(), path.Root("yaml"), &v); diags.HasError() {
		t.Fatalf("reading yaml from state: %v", diags)
	}
	return v.ValueString()
}

// assertCASCStateOmits fails if any of the given strings appears anywhere in
// the state object, not just in yaml.
func assertCASCStateOmits(t *testing.T, st tfsdk.State, values ...string) {
	t.Helper()
	raw := st.Raw.String()
	var leaked []string
	for _, v := range values {
		if strings.Contains(raw, v) {
			leaked = append(leaked, v)
		}
	}
	if len(leaked) > 0 {
		t.Errorf("state contains %q:\n%s", leaked, raw)
	}
}

func mustParseYAML(t *testing.T, s string) interface{} {
	t.Helper()
	v, err := parseYAML(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}
