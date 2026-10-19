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
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Read-level regression tests for #255: a folder's config.xml embeds its whole
// credentials store (FolderCredentialsProperty) as {AQAA...} ciphertext, and
// the template attributes used to copy it into state verbatim.

// Fake hudson.util.Secret / SecretBytes ciphertexts in the formats Jenkins
// serializes: {AQAA...} (payload version 1, 16-byte IV) and SecretBytes'
// prefix-less braced base64. redactCipherReencrypted is the same secret after
// Jenkins re-saved it with a fresh IV.
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
// current document, POST replaces it. It records every POSTed body.
type redactFolderServer struct {
	mu     sync.Mutex
	config string
	posted []string
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
	return &jenkins.Job{Jenkins: client.Jenkins, Base: base}
}

func TestFolderResourceReadRedactsTemplate(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name   string
		config string
	}{
		{name: "folder credentials store", config: redactFolderWithCredentials(redactCipherToken)},
		{name: "same credentials re-encrypted", config: redactFolderWithCredentials(redactCipherReencrypted)},
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
			if !strings.Contains(got.Template.ValueString(), "AuthorizationMatrixProperty") {
				t.Errorf("template lost the rest of config.xml:\n%s", got.Template.ValueString())
			}
			if got.Description.ValueString() != "team folder" || got.DisplayName.ValueString() != "Team" {
				t.Errorf("description/display_name = %q/%q, want team folder/Team", got.Description.ValueString(), got.DisplayName.ValueString())
			}
			templates = append(templates, got.Template.ValueString())
		})
	}

	// Jenkins re-encrypts a Secret with a fresh IV whenever it saves the
	// folder. The stored template must not change with it, or every refresh
	// after an unrelated save would report the folder as changed.
	if len(templates) == 2 && templates[0] != templates[1] {
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

// folderCredentialsPropertyElementName is spelled out here rather than taken
// from the implementation, so this file also builds without the fix.
const folderCredentialsPropertyElementName = "com.cloudbees.hudson.plugins.folder.properties.FolderCredentialsProvider_-FolderCredentialsProperty"

func TestTemplateDataSourcesRedact(t *testing.T) {
	ctx := context.Background()

	// A credential-bearing property other than the folder store: anything
	// encrypted outside FolderCredentialsProperty is replaced in place.
	folderWithOtherSecret := strings.Replace(redactFolderWithCredentials(redactCipherToken), "<healthMetrics/>",
		"<healthMetrics/>\n  <scmToken>"+redactCipherReencrypted+"</scmToken>", 1)

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
			config: folderWithOtherSecret,
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

// initPrivateState gives resp the private-state store the framework server
// allocates before calling Create. Its type lives in an internal framework
// package, so a test can only allocate it through reflection.
func initPrivateState(t *testing.T, resp *resource.CreateResponse) {
	t.Helper()
	f := reflect.ValueOf(resp).Elem().FieldByName("Private")
	f.Set(reflect.New(f.Type().Elem()))
}

// TestJobResourceReadFolderCredentials covers the jenkins_job resource, whose
// template is the practitioner's own and is compared with the refreshed value
// by templatePlanModifier: the folder credentials store is dropped on refresh
// unless the applied template contains it, and the refreshed value must stay
// semantically equal to the configured one either way, or every plan would
// show a diff.
func TestJobResourceReadFolderCredentials(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		configured string // template applied by Create; "" for an imported job
		server     string // config.xml Jenkins serves afterwards
		wantStored bool   // whether the credentials store stays in state
	}{
		{
			// Credentials added to the folder by jenkins_credential_* (or by
			// hand) after the folder was created from a plain template.
			name:       "template without credentials store",
			configured: redactFolderNoCredentials,
			server:     redactFolderWithCredentials(redactCipherToken),
		},
		{
			name:   "imported folder",
			server: redactFolderWithCredentials(redactCipherToken),
		},
		{
			// The practitioner manages the store through this template: it is
			// their configuration, so it stays in state like any other element.
			name:       "template with credentials store",
			configured: redactFolderWithCredentials(redactCipherToken),
			server:     redactFolderWithCredentials(redactCipherToken),
			wantStored: true,
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

			if tt.wantStored {
				if stored != tt.server {
					t.Errorf("template = %q, want config.xml unchanged", stored)
				}
			} else {
				assertNoFolderSecrets(t, stored)
				if !strings.Contains(stored, "AuthorizationMatrixProperty") {
					t.Errorf("template lost the rest of config.xml:\n%s", stored)
				}
			}
			if tt.configured != "" && !templatesEqual(stored, tt.configured) {
				t.Errorf("refreshed template is not equal to the configured one, so every plan would show a diff:\nstate:\n%s\nconfig:\n%s", stored, tt.configured)
			}
		})
	}
}
