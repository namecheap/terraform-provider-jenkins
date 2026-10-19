package jenkins

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Read-level regression tests for #255: a folder's config.xml embeds its whole
// credentials store (FolderCredentialsProperty) as {AQAA...} ciphertext, and
// the template attributes used to copy it into state verbatim.

// Fake hudson.util.Secret / SecretBytes ciphertexts in the formats Jenkins
// serializes: {AQAA...} (payload version 1, 16-byte IV) and SecretBytes'
// prefix-less braced base64. redactCipherReencrypted is the same secret
// encrypted again with a fresh IV, as Jenkins does each time a secret is set
// from plain text (a credential update, a JCasC reload), even to the same value.
const (
	redactCipherToken       = "{AQAAABAAAAAQBwgJCgsMDQ4PEBESExQVFg0QExYZHB8iJSgrLjE0Nzo=}"
	redactCipherReencrypted = "{AQAAABAAAAAQDg8QERITFBUWFxgZGhscHRodICMmKSwvMjU4Oz5BREc=}"
	redactCipherPassword    = "{AQAAABAAAAAgFRYXGBkaGxwdHh8gISIjJCcqLTAzNjk8P0JFSEtOUVRXWl1gY2ZpbG9ydXh7foGE}"
	redactSecretBytes       = "{AAsWISw3Qk1YY255hI+apbC7xtHc5/L9CBMeKTQ/SlU=}"
)

// A folder config.xml as Jenkins 2.583 serves it, split around the
// FolderCredentialsProperty so tests can build it with and without the
// property: redactFolderHead + redactFolderCredentials(...) + redactFolderTail.
const redactFolderHead = `<?xml version='1.1' encoding='UTF-8'?>
<com.cloudbees.hudson.plugins.folder.Folder plugin="cloudbees-folder@6.1106.v1a_8b_c33d1a_c8">
  <actions/>
  <description>team folder</description>
  <displayName>Team</displayName>
  <properties>`

const redactFolderTail = `
    <com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty>
      <inheritanceStrategy class="org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy"/>
      <permission>hudson.model.Item.Read:alice</permission>
    </com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty>
  </properties>
  <folderViews class="com.cloudbees.hudson.plugins.folder.views.DefaultFolderViewHolder">
    <views>
      <hudson.model.AllView>
        <owner class="com.cloudbees.hudson.plugins.folder.Folder" reference="../../../.."/>
        <name>All</name>
        <filterExecutors>false</filterExecutors>
        <filterQueue>false</filterQueue>
        <properties class="hudson.model.View$PropertyList"/>
      </hudson.model.AllView>
    </views>
    <tabBar class="hudson.views.DefaultViewsTabBar"/>
  </folderViews>
  <healthMetrics/>
  <icon class="com.cloudbees.hudson.plugins.folder.icons.StockFolderIcon"/>
</com.cloudbees.hudson.plugins.folder.Folder>`

// redactFolderCredentials returns the folder credentials store element, with
// the leading line break and indentation it has inside <properties>.
func redactFolderCredentials(token string) string {
	return `
    <com.cloudbees.hudson.plugins.folder.properties.FolderCredentialsProvider_-FolderCredentialsProperty plugin="credentials@1514.v6e8e6c2a_8a_2b_">
      <domainCredentialsMap class="hudson.util.CopyOnWriteMap$Hash">
        <entry>
          <com.cloudbees.plugins.credentials.domains.Domain>
            <specifications/>
          </com.cloudbees.plugins.credentials.domains.Domain>
          <java.util.concurrent.CopyOnWriteArrayList>
            <org.jenkinsci.plugins.plaincredentials.impl.StringCredentialsImpl plugin="plain-credentials@199.v9f8e1f741799">
              <scope>GLOBAL</scope>
              <id>api-token</id>
              <description>API token</description>
              <secret>` + token + `</secret>
            </org.jenkinsci.plugins.plaincredentials.impl.StringCredentialsImpl>
            <com.cloudbees.plugins.credentials.impl.UsernamePasswordCredentialsImpl>
              <scope>GLOBAL</scope>
              <id>deploy</id>
              <description></description>
              <username>deploy</username>
              <password>` + redactCipherPassword + `</password>
              <usernameSecret>false</usernameSecret>
            </com.cloudbees.plugins.credentials.impl.UsernamePasswordCredentialsImpl>
            <org.jenkinsci.plugins.plaincredentials.impl.FileCredentialsImpl plugin="plain-credentials@199.v9f8e1f741799">
              <scope>GLOBAL</scope>
              <id>kubeconfig</id>
              <description></description>
              <fileName>kubeconfig</fileName>
              <secretBytes>` + redactSecretBytes + `</secretBytes>
            </org.jenkinsci.plugins.plaincredentials.impl.FileCredentialsImpl>
          </java.util.concurrent.CopyOnWriteArrayList>
        </entry>
      </domainCredentialsMap>
    </com.cloudbees.hudson.plugins.folder.properties.FolderCredentialsProvider_-FolderCredentialsProperty>`
}

// redactFolderWithCredentials is a folder config.xml holding three folder
// credentials, the first encrypted as token.
func redactFolderWithCredentials(token string) string {
	return redactFolderHead + redactFolderCredentials(token) + redactFolderTail
}

// redactFolderNoCredentials is the same folder without a credentials store.
const redactFolderNoCredentials = redactFolderHead + redactFolderTail

// redactFolderWithOtherSecret is redactFolderWithCredentials plus an encrypted
// value outside the credentials store (a folder-level SCM token), which full
// redaction must replace in place.
func redactFolderWithOtherSecret() string {
	return strings.Replace(redactFolderWithCredentials(redactCipherToken), "<healthMetrics/>",
		"<healthMetrics/>\n  <scmToken>"+redactCipherReencrypted+"</scmToken>", 1)
}

// redactJobXML is a freestyle job whose password parameter default Jenkins
// stores encrypted, next to a shell step using ${VAR} placeholders.
const redactJobXML = `<?xml version='1.1' encoding='UTF-8'?>
<project>
  <description>deploy</description>
  <properties>
    <hudson.model.ParametersDefinitionProperty>
      <parameterDefinitions>
        <hudson.model.PasswordParameterDefinition>
          <name>TOKEN</name>
          <defaultValue>` + redactCipherToken + `</defaultValue>
        </hudson.model.PasswordParameterDefinition>
      </parameterDefinitions>
    </hudson.model.ParametersDefinitionProperty>
  </properties>
  <builders>
    <hudson.tasks.Shell>
      <command>curl -H "Authorization: ${TOKEN}" "${JENKINS_URL}" -d '{"a":{"b":1}}'</command>
    </hudson.tasks.Shell>
  </builders>
  <disabled>false</disabled>
</project>`

// assertNoFolderSecrets fails when template still carries any part of the
// folder credentials store.
func assertNoFolderSecrets(t *testing.T, template string) {
	t.Helper()
	for _, leak := range []string{"{AQAA", "FolderCredentialsProperty", redactSecretBytes, "<secret>", "<password>"} {
		if strings.Contains(template, leak) {
			t.Errorf("template stored in state contains %q:\n%s", leak, template)
		}
	}
}

// redactFolderServer serves a folder's config.xml like Jenkins: GET returns the
// current document, POST replaces it. It records every POSTed body. With
// dropGet set, GET drops the connection instead of answering. With errorStatus
// set, GET answers with that status and errorPage instead: the client reports
// a 5xx as an error, but GetConfig returns a 4xx body as the document.
type redactFolderServer struct {
	mu          sync.Mutex
	config      string
	posted      []string
	dropGet     bool
	errorStatus int
	errorPage   string
}

func (s *redactFolderServer) job(t *testing.T, base string) *jenkins.Job {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "crumbIssuer"):
			w.WriteHeader(http.StatusNotFound)
		case strings.Contains(r.URL.Path, "/config.xml") && r.Method == http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			s.config = string(b)
			s.posted = append(s.posted, s.config)
		case strings.Contains(r.URL.Path, "/config.xml") && s.dropGet:
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = conn.Close()
			}
		case strings.Contains(r.URL.Path, "/config.xml") && s.errorStatus != 0:
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(s.errorStatus)
			_, _ = io.WriteString(w, s.errorPage)
		case strings.Contains(r.URL.Path, "/config.xml"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, s.config)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, "{}")
		}
	}))
	t.Cleanup(srv.Close)

	client, err := newJenkinsClient(&Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("newJenkinsClient: %v", err)
	}
	return &jenkins.Job{Jenkins: client.Jenkins, Raw: new(jenkins.JobResponse), Base: base}
}

func TestFolderResourceReadRedactsTemplate(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		config      string
		wantContain []string
	}{
		{name: "folder credentials store", config: redactFolderWithCredentials(redactCipherToken)},
		{name: "same credentials re-encrypted", config: redactFolderWithCredentials(redactCipherReencrypted)},
		{
			// Full redaction: ciphertext outside the store is replaced too.
			name:        "encrypted value outside the credentials store",
			config:      redactFolderWithOtherSecret(),
			wantContain: []string{"<scmToken>{redacted}</scmToken>"},
		},
	}

	var templates []string
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := covcLiveJob(t, "/job/team", tt.config)
			r := &folderResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
				mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
			}}}
			var sr resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &sr)

			st := tfsdk.State{Schema: sr.Schema}
			if d := st.Set(ctx, &folderResourceModel{
				ID: types.StringValue("/job/team"), Name: types.StringValue("team"), Folder: types.StringNull(),
				DisplayName: types.StringValue(""), Description: types.StringValue(""),
				Security: types.SetNull(folderSecurityObjectType), Template: types.StringNull(),
			}); d.HasError() {
				t.Fatalf("state.Set: %v", d)
			}

			resp := resource.ReadResponse{State: st}
			r.Read(ctx, resource.ReadRequest{State: st}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Read: %v", resp.Diagnostics)
			}
			var got folderResourceModel
			if d := resp.State.Get(ctx, &got); d.HasError() {
				t.Fatalf("state.Get: %v", d)
			}

			assertNoFolderSecrets(t, got.Template.ValueString())
			for _, want := range append([]string{"AuthorizationMatrixProperty"}, tt.wantContain...) {
				if !strings.Contains(got.Template.ValueString(), want) {
					t.Errorf("template is missing %q:\n%s", want, got.Template.ValueString())
				}
			}
			if got.Description.ValueString() != "team folder" || got.DisplayName.ValueString() != "Team" {
				t.Errorf("description/display_name = %q/%q, want team folder/Team", got.Description.ValueString(), got.DisplayName.ValueString())
			}
			templates = append(templates, got.Template.ValueString())
		})
	}

	// Setting a secret again from plain text, even to the same value, encrypts
	// it with a fresh IV. The stored template must not change with it, or the
	// folder would show as changed after every such credential update.
	if len(templates) >= 2 && templates[0] != templates[1] {
		t.Errorf("template differs between refreshes of the same credentials:\n%s\n---\n%s", templates[0], templates[1])
	}
}

// TestFolderResourceUpdateKeepsCredentials pins that the redaction is applied
// to state only: Update re-reads config.xml from Jenkins, so the folder
// credentials store it posts back is the real one, not the redacted template.
func TestFolderResourceUpdateKeepsCredentials(t *testing.T) {
	ctx := context.Background()

	srv := &redactFolderServer{config: redactFolderWithCredentials(redactCipherToken)}
	job := srv.job(t, "/job/team")
	r := &folderResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
	}}}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)

	plan := tfsdk.Plan{Schema: sr.Schema}
	if d := plan.Set(ctx, &folderResourceModel{
		ID: types.StringValue("/job/team"), Name: types.StringValue("team"), Folder: types.StringNull(),
		DisplayName: types.StringValue("Team"), Description: types.StringValue("renamed"),
		Security: types.SetNull(folderSecurityObjectType), Template: types.StringUnknown(),
	}); d.HasError() {
		t.Fatalf("plan.Set: %v", d)
	}

	resp := resource.UpdateResponse{State: tfsdk.State{Schema: sr.Schema}}
	r.Update(ctx, resource.UpdateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}

	if len(srv.posted) != 1 {
		t.Fatalf("Update posted config.xml %d times, want 1", len(srv.posted))
	}
	posted := srv.posted[0]
	for _, want := range []string{"<description>renamed</description>", folderCredentialsPropertyElementName, redactCipherToken, redactCipherPassword, redactSecretBytes} {
		if !strings.Contains(posted, want) {
			t.Errorf("posted config.xml is missing %q, so Update would wipe the folder's credentials:\n%s", want, posted)
		}
	}

	var got folderResourceModel
	if d := resp.State.Get(ctx, &got); d.HasError() {
		t.Fatalf("state.Get: %v", d)
	}
	assertNoFolderSecrets(t, got.Template.ValueString())
}

// TestFolderResourceUpdateFailsClosed pins that jenkins_folder Update fails,
// posting nothing, when it cannot read the folder's current config.xml. The
// credentials store only reaches the posted document through that read; a
// folder rendered from the plan alone has none, so posting it would delete
// every folder credential.
func TestFolderResourceUpdateFailsClosed(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		dropGet     bool
		errorStatus int
		errorPage   string
	}{
		{
			name:    "connection dropped",
			dropGet: true,
		},
		{
			// Retried, then reported as an error by the client.
			name:        "server error",
			errorStatus: http.StatusServiceUnavailable,
			errorPage:   "<html><body><h1>503 Service Unavailable</h1></body></html>",
		},
		{
			// A 4xx reaches GetConfig as the document, without an error.
			name:        "error page returned as the document",
			errorStatus: http.StatusForbidden,
			errorPage:   "<html><body><h1>403 Forbidden</h1></body></html>",
		},
		{
			// Likewise, as an empty document.
			name:        "empty document",
			errorStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &redactFolderServer{
				config:      redactFolderWithCredentials(redactCipherToken),
				dropGet:     tt.dropGet,
				errorStatus: tt.errorStatus,
				errorPage:   tt.errorPage,
			}
			job := srv.job(t, "/job/team")
			r := &folderResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
				mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
			}}}
			var sr resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &sr)

			plan := tfsdk.Plan{Schema: sr.Schema}
			if d := plan.Set(ctx, &folderResourceModel{
				ID: types.StringValue("/job/team"), Name: types.StringValue("team"), Folder: types.StringNull(),
				DisplayName: types.StringValue("Team"), Description: types.StringValue("renamed"),
				Security: types.SetNull(folderSecurityObjectType), Template: types.StringUnknown(),
			}); d.HasError() {
				t.Fatalf("plan.Set: %v", d)
			}

			resp := resource.UpdateResponse{State: tfsdk.State{Schema: sr.Schema}}
			r.Update(ctx, resource.UpdateRequest{Plan: plan}, &resp)

			if !resp.Diagnostics.HasError() {
				t.Errorf("Update succeeded, want an error")
			}
			srv.mu.Lock()
			defer srv.mu.Unlock()
			if len(srv.posted) != 0 {
				t.Errorf("Update posted config.xml %d times, want 0; the folder now has no credentials store:\n%s", len(srv.posted), srv.posted[0])
			}
		})
	}
}

// folderCredentialsPropertyElementName is spelled out here rather than taken
// from the implementation, so this file also builds without the fix.
const folderCredentialsPropertyElementName = "com.cloudbees.hudson.plugins.folder.properties.FolderCredentialsProvider_-FolderCredentialsProperty"

func TestTemplateDataSourcesRedact(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		ds          datasource.DataSource
		model       any
		config      string
		template    func(datasource.ReadResponse) string
		wantContain []string
	}{
		{
			name:   "jenkins_folder",
			ds:     &folderDataSource{},
			model:  &folderDataSourceModel{Name: types.StringValue("team"), Folder: types.StringNull(), ID: types.StringNull(), Description: types.StringNull(), DisplayName: types.StringNull(), Template: types.StringNull()},
			config: redactFolderWithOtherSecret(),
			template: func(resp datasource.ReadResponse) string {
				var m folderDataSourceModel
				resp.State.Get(ctx, &m)
				return m.Template.ValueString()
			},
			wantContain: []string{"<description>team folder</description>", "AuthorizationMatrixProperty", "<scmToken>{redacted}</scmToken>"},
		},
		{
			name:   "jenkins_job reading a folder",
			ds:     &jobDataSource{},
			model:  &jobDataSourceModel{Name: types.StringValue("team"), Folder: types.StringNull(), ID: types.StringNull(), Template: types.StringNull()},
			config: redactFolderWithCredentials(redactCipherToken),
			template: func(resp datasource.ReadResponse) string {
				var m jobDataSourceModel
				resp.State.Get(ctx, &m)
				return m.Template.ValueString()
			},
			wantContain: []string{"<description>team folder</description>", "AuthorizationMatrixProperty"},
		},
		{
			name:   "jenkins_job with an encrypted parameter default",
			ds:     &jobDataSource{},
			model:  &jobDataSourceModel{Name: types.StringValue("deploy"), Folder: types.StringNull(), ID: types.StringNull(), Template: types.StringNull()},
			config: redactJobXML,
			template: func(resp datasource.ReadResponse) string {
				var m jobDataSourceModel
				resp.State.Get(ctx, &m)
				return m.Template.ValueString()
			},
			wantContain: []string{"<defaultValue>{redacted}</defaultValue>", `${TOKEN}" "${JENKINS_URL}" -d '{"a":{"b":1}}'`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := covcLiveJob(t, "/job/x", tt.config)
			client := &mockJenkinsClient{
				mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
			}
			switch d := tt.ds.(type) {
			case *folderDataSource:
				d.dataSourceHelper = &dataSourceHelper{client: client}
			case *jobDataSource:
				d.dataSourceHelper = &dataSourceHelper{client: client}
			}

			var sr datasource.SchemaResponse
			tt.ds.Schema(ctx, datasource.SchemaRequest{}, &sr)
			st := tfsdk.State{Schema: sr.Schema}
			if d := st.Set(ctx, tt.model); d.HasError() {
				t.Fatalf("config set: %v", d)
			}
			resp := datasource.ReadResponse{State: tfsdk.State{Schema: sr.Schema}}
			tt.ds.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: sr.Schema, Raw: st.Raw}}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Read: %v", resp.Diagnostics)
			}

			got := tt.template(resp)
			assertNoFolderSecrets(t, got)
			for _, want := range tt.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("template is missing %q:\n%s", want, got)
				}
			}
		})
	}
}

// initPrivateState gives v, a pointer to a framework request or response
// struct, the empty private-state store the framework server allocates before
// calling the resource. Its type lives in an internal framework package, so a
// test can only allocate it through reflection.
func initPrivateState(t *testing.T, v any) {
	t.Helper()
	f := reflect.ValueOf(v).Elem().FieldByName("Private")
	f.Set(reflect.New(f.Type().Elem()))
}

// TestJobResourceReadFolderCredentials covers the jenkins_job resource, whose
// template is the practitioner's own and is compared with the refreshed value
// by templatePlanModifier: the folder credentials store is dropped on refresh
// unless the applied template contains it, ciphertext elsewhere is kept, and
// the refreshed value must stay semantically equal to the configured one, or
// every plan would show a diff.
func TestJobResourceReadFolderCredentials(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		configured string // template applied by Create; "" for an imported job
		server     string // config.xml Jenkins serves afterwards
		want       string // template the refresh stores
	}{
		{
			// Credentials added to the folder by jenkins_credential_* (or by
			// hand) after the folder was created from a plain template.
			name:       "template without credentials store",
			configured: redactFolderNoCredentials,
			server:     redactFolderWithCredentials(redactCipherToken),
			want:       redactFolderNoCredentials,
		},
		{
			name:   "imported folder",
			server: redactFolderWithCredentials(redactCipherToken),
			want:   redactFolderNoCredentials,
		},
		{
			// The practitioner manages the store through this template: it is
			// their configuration, so it stays in state like any other element.
			name:       "template with credentials store",
			configured: redactFolderWithCredentials(redactCipherToken),
			server:     redactFolderWithCredentials(redactCipherToken),
			want:       redactFolderWithCredentials(redactCipherToken),
		},
		{
			// An encrypted parameter default pasted from config.xml is part of
			// the configuration: redacting it would make state differ from it
			// on every refresh.
			name:       "template with an encrypted parameter default",
			configured: redactJobXML,
			server:     redactJobXML,
			want:       redactJobXML,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			live := covcLiveJob(t, "/job/team", tt.server)
			r := &jobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
				mockCreateJobInFolder: func(context.Context, string, string, ...string) (*jenkins.Job, error) { return live, nil },
				mockGetJob:            func(context.Context, string, ...string) (*jenkins.Job, error) { return live, nil },
			}}}
			var sr resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &sr)

			readReq := resource.ReadRequest{State: tfsdk.State{Schema: sr.Schema}}
			if tt.configured != "" {
				plan := tfsdk.Plan{Schema: sr.Schema}
				if d := plan.Set(ctx, &jobResourceModel{
					ID: types.StringUnknown(), Name: types.StringValue("team"), Folder: types.StringNull(),
					Template: types.StringValue(tt.configured), Disabled: types.BoolNull(),
				}); d.HasError() {
					t.Fatalf("plan.Set: %v", d)
				}
				cResp := resource.CreateResponse{State: tfsdk.State{Schema: sr.Schema}}
				initPrivateState(t, &cResp)
				r.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
				if cResp.Diagnostics.HasError() {
					t.Fatalf("Create: %v", cResp.Diagnostics)
				}
				readReq.State = cResp.State
				readReq.Private = cResp.Private
			} else {
				// ImportState leaves only the identity in state.
				if d := readReq.State.Set(ctx, &jobResourceModel{
					ID: types.StringValue("/job/team"), Name: types.StringValue("team"), Folder: types.StringNull(),
					Template: types.StringNull(), Disabled: types.BoolNull(),
				}); d.HasError() {
					t.Fatalf("state.Set: %v", d)
				}
			}

			resp := resource.ReadResponse{State: readReq.State}
			r.Read(ctx, readReq, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Read: %v", resp.Diagnostics)
			}
			var got jobResourceModel
			if d := resp.State.Get(ctx, &got); d.HasError() {
				t.Fatalf("state.Get: %v", d)
			}
			stored := got.Template.ValueString()

			if stored != tt.want {
				t.Errorf("refreshed template =\n%s\nwant\n%s", stored, tt.want)
			}
			if !strings.Contains(tt.want, "{AQAA") {
				assertNoFolderSecrets(t, stored)
			}
			if tt.configured != "" && !templatesEqual(stored, tt.configured) {
				t.Errorf("refreshed template is not equal to the configured one, so every plan would show a diff:\nstate:\n%s\nconfig:\n%s", stored, tt.configured)
			}
		})
	}
}

// TestJobResourceUpdateFolderCredentials covers what jenkins_job Update posts
// for a folder, and the private-state record it leaves for the next refresh. A
// credentials store the template does not contain is not managed by it, so
// Update must keep the store Jenkins has rather than post a document without
// it: refreshes hide such a store, so a plan would otherwise never show that
// applying it deletes every folder credential.
func TestJobResourceUpdateFolderCredentials(t *testing.T) {
	ctx := context.Background()

	renamed := strings.Replace(redactFolderNoCredentials,
		"<description>team folder</description>", "<description>renamed</description>", 1)
	// The store's start tag broken over lines right after the element name,
	// as a practitioner may format it.
	brokenStartTag := strings.Replace(redactFolderWithCredentials(redactCipherReencrypted),
		folderCredentialsPropertyElementName+" plugin=", folderCredentialsPropertyElementName+"\n        plugin=", 1)

	tests := []struct {
		name string
		// created is the template Create applied. "" stands for state written
		// by an earlier provider version: the raw server config.xml as the
		// template and no private state (an import is the same after its
		// first refresh).
		created string
		// server is the config.xml Jenkins serves when the plan is made, after
		// any out-of-band change.
		server string
		// planned returns the template Update applies, given the refreshed one.
		planned  func(refreshed string) string
		disabled types.Bool
		// wantPostedStore is the ciphertext the config.xml Update posts must
		// carry inside a credentials store; "" when it must carry no store.
		wantPostedStore    string
		wantPostedContains []string
		wantPostedLacks    []string
		// later, when set, is what Jenkins serves at the next refresh instead
		// of the posted config.xml.
		later string
		// wantRefreshedStore is whether that refresh keeps the store in state.
		wantRefreshedStore bool
	}{
		{
			// jenkins_credential_* with folder = jenkins_job.team.id (or the UI,
			// or JCasC) added credentials to a folder created from a plain
			// template. Editing the template must not delete them.
			name:               "template edit keeps an unmanaged store",
			created:            redactFolderNoCredentials,
			server:             redactFolderWithCredentials(redactCipherToken),
			planned:            func(string) string { return renamed },
			wantPostedStore:    redactCipherToken,
			wantPostedContains: []string{"<description>renamed</description>"},
		},
		{
			// lifecycle { ignore_changes = [template] }: Terraform plans the
			// refreshed state as the template, and toggling disabled posts it.
			// Before #255 that value held the raw store, so it was posted back.
			name:            "ignore_changes posts the refreshed template",
			server:          redactFolderWithCredentials(redactCipherToken),
			planned:         func(refreshed string) string { return refreshed },
			disabled:        types.BoolValue(false),
			wantPostedStore: redactCipherToken,
		},
		{
			name:               "template takes over the store",
			created:            redactFolderNoCredentials,
			server:             redactFolderNoCredentials,
			planned:            func(string) string { return redactFolderWithCredentials(redactCipherReencrypted) },
			wantPostedStore:    redactCipherReencrypted,
			wantRefreshedStore: true,
		},
		{
			// The template takes over a store added out of band. Its own store
			// replaces that one: adding the live store as well, as for a
			// template without one, would post two stores.
			name:               "template takes over the store, start tag broken over lines",
			created:            redactFolderNoCredentials,
			server:             redactFolderWithCredentials(redactCipherToken),
			planned:            func(string) string { return brokenStartTag },
			wantPostedStore:    redactCipherReencrypted,
			wantPostedLacks:    []string{redactCipherToken},
			wantRefreshedStore: true,
		},
		{
			// Removing the store from a template that managed it is a change the
			// plan shows (state holds the store), and applying it deletes it.
			// Credentials added again out of band afterwards are not managed,
			// so they must stay out of state.
			name:    "template drops the store it managed",
			created: redactFolderWithCredentials(redactCipherToken),
			server:  redactFolderWithCredentials(redactCipherToken),
			planned: func(string) string { return redactFolderNoCredentials },
			later:   redactFolderWithCredentials(redactCipherReencrypted),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &redactFolderServer{config: tt.server}
			job := srv.job(t, "/job/team")
			r := &jobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
				mockCreateJobInFolder: func(context.Context, string, string, ...string) (*jenkins.Job, error) { return job, nil },
				mockGetJob:            func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
			}}}
			var sr resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &sr)

			// Prior state and private state, then the refresh of the plan.
			readReq := resource.ReadRequest{State: tfsdk.State{Schema: sr.Schema}}
			if tt.created != "" {
				plan := tfsdk.Plan{Schema: sr.Schema}
				if d := plan.Set(ctx, &jobResourceModel{
					ID: types.StringUnknown(), Name: types.StringValue("team"), Folder: types.StringNull(),
					Template: types.StringValue(tt.created), Disabled: types.BoolNull(),
				}); d.HasError() {
					t.Fatalf("plan.Set: %v", d)
				}
				cResp := resource.CreateResponse{State: tfsdk.State{Schema: sr.Schema}}
				initPrivateState(t, &cResp)
				r.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
				if cResp.Diagnostics.HasError() {
					t.Fatalf("Create: %v", cResp.Diagnostics)
				}
				readReq.State = cResp.State
				readReq.Private = cResp.Private
			} else {
				if d := readReq.State.Set(ctx, &jobResourceModel{
					ID: types.StringValue("/job/team"), Name: types.StringValue("team"), Folder: types.StringNull(),
					Template: types.StringValue(tt.server), Disabled: types.BoolNull(),
				}); d.HasError() {
					t.Fatalf("state.Set: %v", d)
				}
				initPrivateState(t, &readReq)
			}
			rResp := resource.ReadResponse{State: readReq.State}
			r.Read(ctx, readReq, &rResp)
			if rResp.Diagnostics.HasError() {
				t.Fatalf("Read: %v", rResp.Diagnostics)
			}
			var refreshed jobResourceModel
			if d := rResp.State.Get(ctx, &refreshed); d.HasError() {
				t.Fatalf("state.Get: %v", d)
			}

			// Update. The framework hands the same private-state data to the
			// request and the response.
			planned := tt.planned(refreshed.Template.ValueString())
			plan := tfsdk.Plan{Schema: sr.Schema}
			if d := plan.Set(ctx, &jobResourceModel{
				ID: types.StringValue("/job/team"), Name: types.StringValue("team"), Folder: types.StringNull(),
				Template: types.StringValue(planned), Disabled: tt.disabled,
			}); d.HasError() {
				t.Fatalf("plan.Set: %v", d)
			}
			uResp := resource.UpdateResponse{State: tfsdk.State{Schema: sr.Schema}, Private: readReq.Private}
			r.Update(ctx, resource.UpdateRequest{Plan: plan, State: rResp.State, Private: readReq.Private}, &uResp)
			if uResp.Diagnostics.HasError() {
				t.Fatalf("Update: %v", uResp.Diagnostics)
			}

			if len(srv.posted) != 1 {
				t.Fatalf("Update posted config.xml %d times, want 1", len(srv.posted))
			}
			posted := srv.posted[0]
			switch {
			case tt.wantPostedStore != "" && !strings.Contains(posted, folderCredentialsPropertyElementName):
				t.Errorf("Update posted config.xml WITHOUT the folder credentials store, so every folder credential is deleted:\n%s", posted)
			case tt.wantPostedStore != "" && !strings.Contains(posted, tt.wantPostedStore):
				t.Errorf("posted credentials store does not carry %q:\n%s", tt.wantPostedStore, posted)
			case tt.wantPostedStore == "" && strings.Contains(posted, folderCredentialsPropertyElementName):
				t.Errorf("Update posted a credentials store the template removed:\n%s", posted)
			}
			for _, want := range tt.wantPostedContains {
				if !strings.Contains(posted, want) {
					t.Errorf("posted config.xml is missing %q:\n%s", want, posted)
				}
			}
			for _, lack := range tt.wantPostedLacks {
				if strings.Contains(posted, lack) {
					t.Errorf("posted config.xml carries %q:\n%s", lack, posted)
				}
			}
			if n := strings.Count(posted, "</"+folderCredentialsPropertyElementName); n > 1 {
				t.Errorf("Update posted config.xml with %d folder credentials stores, want at most 1:\n%s", n, posted)
			}

			// The next refresh.
			if tt.later != "" {
				srv.mu.Lock()
				srv.config = tt.later
				srv.mu.Unlock()
			}
			rResp2 := resource.ReadResponse{State: uResp.State}
			r.Read(ctx, resource.ReadRequest{State: uResp.State, Private: uResp.Private}, &rResp2)
			if rResp2.Diagnostics.HasError() {
				t.Fatalf("Read after Update: %v", rResp2.Diagnostics)
			}
			var got jobResourceModel
			if d := rResp2.State.Get(ctx, &got); d.HasError() {
				t.Fatalf("state.Get: %v", d)
			}
			stored := got.Template.ValueString()
			if tt.wantRefreshedStore {
				if !strings.Contains(stored, folderCredentialsPropertyElementName) {
					t.Errorf("refresh after Update dropped the credentials store the template manages:\n%s", stored)
				}
			} else {
				assertNoFolderSecrets(t, stored)
			}
			if !templatesEqual(stored, planned) {
				t.Errorf("refreshed template is not equal to the applied one, so every plan would show a diff:\nstate:\n%s\nconfig:\n%s", stored, planned)
			}
		})
	}
}

// TestJobResourceUpdateFailsClosed pins that jenkins_job Update fails, posting
// nothing, when it cannot carry an unmanaged folder credentials store over to
// the template: posting the template without it would delete every folder
// credential, and refreshes hide the store, so no plan would have shown that.
func TestJobResourceUpdateFailsClosed(t *testing.T) {
	ctx := context.Background()

	renamed := strings.Replace(redactFolderNoCredentials,
		"<description>team folder</description>", "<description>renamed</description>", 1)

	tests := []struct {
		name        string
		template    string
		dropGet     bool
		errorStatus int
	}{
		{
			name:     "current config.xml cannot be read",
			template: renamed,
			dropGet:  true,
		},
		{
			// Retried, then reported as an error by the client: the error page
			// must not be taken for a config.xml without a store.
			name:        "current config.xml answers with a server error",
			template:    renamed,
			errorStatus: http.StatusServiceUnavailable,
		},
		{
			// Rejected at plan by jobXMLValidator; Update must not rely on that.
			name:     "store cannot be added to the template",
			template: "<com.cloudbees.hudson.plugins.folder.Folder><properties><description>renamed</description></com.cloudbees.hudson.plugins.folder.Folder>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &redactFolderServer{
				config:      redactFolderWithCredentials(redactCipherToken),
				dropGet:     tt.dropGet,
				errorStatus: tt.errorStatus,
				errorPage:   "<html><body><h1>Service Unavailable</h1></body></html>",
			}
			job := srv.job(t, "/job/team")
			r := &jobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
				mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
			}}}
			var sr resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &sr)

			// Prior state from a template without the store, as refreshed.
			state := tfsdk.State{Schema: sr.Schema}
			if d := state.Set(ctx, &jobResourceModel{
				ID: types.StringValue("/job/team"), Name: types.StringValue("team"), Folder: types.StringNull(),
				Template: types.StringValue(redactFolderNoCredentials), Disabled: types.BoolNull(),
			}); d.HasError() {
				t.Fatalf("state.Set: %v", d)
			}
			plan := tfsdk.Plan{Schema: sr.Schema}
			if d := plan.Set(ctx, &jobResourceModel{
				ID: types.StringValue("/job/team"), Name: types.StringValue("team"), Folder: types.StringNull(),
				Template: types.StringValue(tt.template), Disabled: types.BoolNull(),
			}); d.HasError() {
				t.Fatalf("plan.Set: %v", d)
			}

			req := resource.UpdateRequest{Plan: plan, State: state}
			initPrivateState(t, &req)
			resp := resource.UpdateResponse{State: tfsdk.State{Schema: sr.Schema}, Private: req.Private}
			r.Update(ctx, req, &resp)

			if !resp.Diagnostics.HasError() {
				t.Errorf("Update succeeded, want an error")
			}
			srv.mu.Lock()
			defer srv.mu.Unlock()
			if len(srv.posted) != 0 {
				t.Errorf("Update posted config.xml %d times, want 0; the folder now has no credentials store:\n%s", len(srv.posted), srv.posted[0])
			}
		})
	}
}

// TestJobTemplateRejectsRedactedSecrets pins that a template copied from a
// data source, whose encrypted values read {redacted}, is rejected at plan
// time. Jenkins cannot decrypt the marker, so it would silently store the
// literal string as the secret.
func TestJobTemplateRejectsRedactedSecrets(t *testing.T) {
	ctx := context.Background()

	var sr resource.SchemaResponse
	(&jobResource{}).Schema(ctx, resource.SchemaRequest{}, &sr)
	attr, ok := sr.Schema.Attributes["template"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("template attribute is %T, want schema.StringAttribute", sr.Schema.Attributes["template"])
	}

	tests := []struct {
		name     string
		template string
		wantErr  bool
	}{
		{
			name:     "password parameter default from the jenkins_job data source",
			template: strings.Replace(redactJobXML, redactCipherToken, "{redacted}", 1),
			wantErr:  true,
		},
		{
			name:     "folder secret from the jenkins_folder data source, indented",
			template: "<com.cloudbees.hudson.plugins.folder.Folder>\n  <scmToken>\n    {redacted}\n  </scmToken>\n</com.cloudbees.hudson.plugins.folder.Folder>",
			wantErr:  true,
		},
		{
			name:     "original ciphertext",
			template: redactJobXML,
		},
		{
			name:     "marker inside other text",
			template: `<project><description>secrets read {redacted} in the data source</description></project>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validator.StringRequest{Path: path.Root("template"), ConfigValue: types.StringValue(tt.template)}
			var resp validator.StringResponse
			for _, v := range attr.Validators {
				v.ValidateString(ctx, req, &resp)
			}
			if got := resp.Diagnostics.HasError(); got != tt.wantErr {
				t.Errorf("template validation error = %v, want %v (diagnostics: %v)", got, tt.wantErr, resp.Diagnostics)
			}
		})
	}
}
