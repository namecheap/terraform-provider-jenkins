package jenkins

import (
	"encoding/xml"
	"reflect"
	"strings"
	"testing"
)

func Test_parseFolder(t *testing.T) {
	type args struct {
		def string
	}
	tests := []struct {
		name    string
		args    args
		want    *folder
		wantErr bool
	}{
		{
			name: "success",
			args: args{
				def: `<com.cloudbees.hudson.plugins.folder.Folder plugin="cloudbees-folder@6.15">
  <actions/>
  <description>Example Description</description>
  <displayName>Example Display Name</displayName>
  <properties>
    <com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty>
      <inheritanceStrategy class="org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy"/>
      <permission>com.cloudbees.plugins.credentials.CredentialsProvider.Create:anonymous</permission>
      <permission>com.cloudbees.plugins.credentials.CredentialsProvider.Delete:authenticated</permission>
      <permission>hudson.model.Item.Cancel:authenticated</permission>
      <permission>hudson.model.Item.Discover:anonymous</permission>
    </com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty>
    <org.jenkinsci.plugins.workflow.libs.FolderLibraries plugin="workflow-cps-global-lib@2.17">
      <libraries>
        <org.jenkinsci.plugins.workflow.libs.LibraryConfiguration>
          <name>Example Library Configuration</name>
          <implicit>false</implicit>
          <allowVersionOverride>true</allowVersionOverride>
          <includeInChangesets>true</includeInChangesets>
        </org.jenkinsci.plugins.workflow.libs.LibraryConfiguration>
      </libraries>
    </org.jenkinsci.plugins.workflow.libs.FolderLibraries>
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
      <hudson.model.ListView>
        <owner class="com.cloudbees.hudson.plugins.folder.Folder" reference="../../../.."/>
        <name>Example View</name>
        <filterExecutors>false</filterExecutors>
        <filterQueue>false</filterQueue>
        <properties class="hudson.model.View$PropertyList"/>
        <jobNames>
          <comparator class="hudson.util.CaseInsensitiveComparator"/>
        </jobNames>
        <jobFilters/>
        <columns>
          <hudson.views.StatusColumn/>
          <hudson.views.WeatherColumn/>
          <hudson.views.JobColumn/>
          <hudson.views.LastSuccessColumn/>
          <hudson.views.LastFailureColumn/>
          <hudson.views.LastDurationColumn/>
          <hudson.views.BuildButtonColumn/>
        </columns>
        <recurse>false</recurse>
      </hudson.model.ListView>
    </views>
    <primaryView>All</primaryView>
    <tabBar class="hudson.views.DefaultViewsTabBar"/>
  </folderViews>
  <healthMetrics>
    <com.cloudbees.hudson.plugins.folder.health.WorstChildHealthMetric>
      <nonRecursive>true</nonRecursive>
    </com.cloudbees.hudson.plugins.folder.health.WorstChildHealthMetric>
  </healthMetrics>
  <icon class="com.cloudbees.hudson.plugins.folder.icons.StockFolderIcon"/>
</com.cloudbees.hudson.plugins.folder.Folder>`,
			},
			want: &folder{
				XMLName:     xml.Name{Local: "com.cloudbees.hudson.plugins.folder.Folder"},
				Description: "Example Description",
				DisplayName: "Example Display Name",
				Properties: folderProperties{
					Security: &folderSecurity{
						AuthorizationStrategy: folderAuthorizationStrategyMatrix,
						InheritanceStrategy: folderPermissionInheritanceStrategy{
							Class: "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy",
						},
						Permission: []string{
							"com.cloudbees.plugins.credentials.CredentialsProvider.Create:anonymous",
							"com.cloudbees.plugins.credentials.CredentialsProvider.Delete:authenticated",
							"hudson.model.Item.Cancel:authenticated",
							"hudson.model.Item.Discover:anonymous",
						},
					},
					Other: []xmlRawProperty{
						{
							XMLName: xml.Name{Local: "org.jenkinsci.plugins.workflow.libs.FolderLibraries"},
							Plugin:  "workflow-cps-global-lib@2.17",
							Raw: `
      <libraries>
        <org.jenkinsci.plugins.workflow.libs.LibraryConfiguration>
          <name>Example Library Configuration</name>
          <implicit>false</implicit>
          <allowVersionOverride>true</allowVersionOverride>
          <includeInChangesets>true</includeInChangesets>
        </org.jenkinsci.plugins.workflow.libs.LibraryConfiguration>
      </libraries>
    `,
						},
					},
				},
				FolderViews: xmlRawProperty{
					XMLName: xml.Name{Local: "folderViews"},
					Raw: `
    <views>
      <hudson.model.AllView>
        <owner class="com.cloudbees.hudson.plugins.folder.Folder" reference="../../../.."/>
        <name>All</name>
        <filterExecutors>false</filterExecutors>
        <filterQueue>false</filterQueue>
        <properties class="hudson.model.View$PropertyList"/>
      </hudson.model.AllView>
      <hudson.model.ListView>
        <owner class="com.cloudbees.hudson.plugins.folder.Folder" reference="../../../.."/>
        <name>Example View</name>
        <filterExecutors>false</filterExecutors>
        <filterQueue>false</filterQueue>
        <properties class="hudson.model.View$PropertyList"/>
        <jobNames>
          <comparator class="hudson.util.CaseInsensitiveComparator"/>
        </jobNames>
        <jobFilters/>
        <columns>
          <hudson.views.StatusColumn/>
          <hudson.views.WeatherColumn/>
          <hudson.views.JobColumn/>
          <hudson.views.LastSuccessColumn/>
          <hudson.views.LastFailureColumn/>
          <hudson.views.LastDurationColumn/>
          <hudson.views.BuildButtonColumn/>
        </columns>
        <recurse>false</recurse>
      </hudson.model.ListView>
    </views>
    <primaryView>All</primaryView>
    <tabBar class="hudson.views.DefaultViewsTabBar"/>
  `,
				},
				HealthMetrics: xmlRawProperty{
					XMLName: xml.Name{Local: "healthMetrics"},
					Raw: `
    <com.cloudbees.hudson.plugins.folder.health.WorstChildHealthMetric>
      <nonRecursive>true</nonRecursive>
    </com.cloudbees.hudson.plugins.folder.health.WorstChildHealthMetric>
  `,
				},
			},
		},
		{
			name: "error-invalid-xml",
			args: args{
				def: `Invalid`,
			},
			want:    &folder{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFolder(tt.args.def)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseFolder() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseFolder() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func Test_parseFolderAzureADSecurity(t *testing.T) {
	f, err := parseFolder(`<com.cloudbees.hudson.plugins.folder.Folder>
  <properties>
    <com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty>
      <inheritanceStrategy class="org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy"/>
      <permission>hudson.model.Item.Read:authenticated</permission>
    </com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty>
    <com.microsoft.jenkins.azuread.AzureAdAuthorizationMatrixFolderProperty plugin="azure-ad@580.v2f665882b_a_71">
      <inheritanceStrategy class="org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy"/>
      <permission>USER:hudson.model.Item.Create:9beb5590-e50a-4d84-bf1b-2aa01c537ba5</permission>
      <permission>GROUP:hudson.model.View.Read:authenticated</permission>
      <entries><entry>keep-me</entry></entries>
    </com.microsoft.jenkins.azuread.AzureAdAuthorizationMatrixFolderProperty>
  </properties>
</com.cloudbees.hudson.plugins.folder.Folder>`)
	if err != nil {
		t.Fatal(err)
	}

	if got := f.Properties.security(folderAuthorizationStrategyAzureAD); got == nil {
		t.Fatal("Azure AD security property was not parsed")
	} else if got.AuthorizationStrategy != folderAuthorizationStrategyAzureAD {
		t.Errorf("authorization strategy = %q, want %q", got.AuthorizationStrategy, folderAuthorizationStrategyAzureAD)
	} else if len(got.Permission) != 2 {
		t.Errorf("permissions len = %d, want 2", len(got.Permission))
	} else if got.Plugin != "azure-ad@580.v2f665882b_a_71" {
		t.Errorf("plugin = %q, want Azure AD plugin version", got.Plugin)
	} else if len(got.Extra) != 1 || got.Extra[0].XMLName.Local != "entries" || !strings.Contains(got.Extra[0].Raw, "keep-me") {
		t.Errorf("extra children were not preserved: %#v", got.Extra)
	}
	if got := f.Properties.security(folderAuthorizationStrategyMatrix); got == nil {
		t.Fatal("matrix security property was not parsed")
	}
	if got := f.Properties.security(""); got != f.Properties.Security {
		t.Fatal("matrix security property was not preferred when no strategy was requested")
	}
	if got := (&folderProperties{AzureADSecurity: f.Properties.AzureADSecurity}).security(""); got == nil || got.AuthorizationStrategy != folderAuthorizationStrategyAzureAD {
		t.Fatal("Azure AD security property was not detected without a requested strategy")
	}
}

// Test_parseFolder_adversarial feeds well-formed-but-unexpected XML (extra
// elements, plugin-schema shape drift, XML 1.1 declarations, wrong roots) to
// guard against a Jenkins/plugin config-shape change silently breaking parsing.
// Unknown elements must be ignored, known fields must still parse, and only
// genuinely invalid input (malformed XML or a wrong root element) must error.
func Test_parseFolder_adversarial(t *testing.T) {
	tests := []struct {
		name            string
		def             string
		wantErr         bool
		wantDescription string
	}{
		{
			name: "unknown extra elements are ignored",
			def: `<?xml version="1.0" encoding="UTF-8"?>
<com.cloudbees.hudson.plugins.folder.Folder>
  <description>desc</description>
  <someFutureField>surprise</someFutureField>
  <nestedFuture><child>x</child></nestedFuture>
</com.cloudbees.hudson.plugins.folder.Folder>`,
			wantErr:         false,
			wantDescription: "desc",
		},
		{
			name: "authorization matrix with an added child element",
			def: `<com.cloudbees.hudson.plugins.folder.Folder>
  <description>d</description>
  <properties>
    <com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty>
      <inheritanceStrategy class="org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy"/>
      <permission>hudson.model.Item.Read:alice</permission>
      <entries><entry>unexpected</entry></entries>
    </com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty>
  </properties>
</com.cloudbees.hudson.plugins.folder.Folder>`,
			wantErr:         false,
			wantDescription: "d",
		},
		{
			name: "xml 1.1 declaration is normalized",
			def: `<?xml version="1.1" encoding="UTF-8"?>
<com.cloudbees.hudson.plugins.folder.Folder>
  <description>eleven</description>
</com.cloudbees.hudson.plugins.folder.Folder>`,
			wantErr:         false,
			wantDescription: "eleven",
		},
		{
			name:            "empty but well-formed folder",
			def:             `<com.cloudbees.hudson.plugins.folder.Folder></com.cloudbees.hudson.plugins.folder.Folder>`,
			wantErr:         false,
			wantDescription: "",
		},
		{
			name:    "well-formed XML with a wrong root element",
			def:     `<some.other.Plugin><description>d</description></some.other.Plugin>`,
			wantErr: true,
		},
		{
			name:    "malformed XML (unclosed tag)",
			def:     `<com.cloudbees.hudson.plugins.folder.Folder><description>d`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFolder(tt.def)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseFolder() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got.Description != tt.wantDescription {
				t.Errorf("Description = %q, want %q", got.Description, tt.wantDescription)
			}
		})
	}
}

func Test_folder_Render(t *testing.T) {
	type fields struct {
		Description string
		DisplayName string
		Properties  folderProperties
	}
	tests := []struct {
		name    string
		fields  fields
		want    []byte
		wantErr bool
	}{
		{
			name: "success",
			fields: fields{
				Description: "Example Description",
				DisplayName: "Example Display Name",
				Properties: folderProperties{
					Security: &folderSecurity{
						Permission: []string{"example", "permission"},
						InheritanceStrategy: folderPermissionInheritanceStrategy{
							Class: "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy",
						},
					},
					Other: []xmlRawProperty{
						{
							XMLName: xml.Name{Local: "org.jenkinsci.plugins.workflow.libs.FolderLibraries"},
							Plugin:  "workflow-cps-global-lib@2.17",
							Raw: `
      <libraries>
        <org.jenkinsci.plugins.workflow.libs.LibraryConfiguration>
          <name>Example Library Configuration</name>
          <implicit>false</implicit>
          <allowVersionOverride>true</allowVersionOverride>
          <includeInChangesets>true</includeInChangesets>
        </org.jenkinsci.plugins.workflow.libs.LibraryConfiguration>
      </libraries>
    `,
						},
					},
				},
			},
			want: []byte(`<com.cloudbees.hudson.plugins.folder.Folder>
	<description>Example Description</description>
    <displayName>Example Display Name</displayName>
	<properties>
		<com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty>
      <inheritanceStrategy class="org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy"></inheritanceStrategy>
			<permission>example</permission>
			<permission>permission</permission>
    </com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty>
    <org.jenkinsci.plugins.workflow.libs.FolderLibraries plugin="workflow-cps-global-lib@2.17">
      <libraries>
        <org.jenkinsci.plugins.workflow.libs.LibraryConfiguration>
          <name>Example Library Configuration</name>
          <implicit>false</implicit>
          <allowVersionOverride>true</allowVersionOverride>
          <includeInChangesets>true</includeInChangesets>
        </org.jenkinsci.plugins.workflow.libs.LibraryConfiguration>
      </libraries>
    </org.jenkinsci.plugins.workflow.libs.FolderLibraries>
	</properties>
	<folderViews></folderViews>
	<healthMetrics></healthMetrics>
</com.cloudbees.hudson.plugins.folder.Folder>`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j := &folder{
				Description: tt.fields.Description,
				DisplayName: tt.fields.DisplayName,
				Properties:  tt.fields.Properties,
			}
			got, err := j.Render()
			if (err != nil) != tt.wantErr {
				t.Errorf("folder.Render() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			strGot := strings.ReplaceAll(string(got), "\n", "")
			strGot = strings.ReplaceAll(strGot, "  ", "\t")
			strGot = strings.ReplaceAll(strGot, "\t", "")
			want := strings.ReplaceAll(string(tt.want), "\n", "")
			want = strings.ReplaceAll(want, "  ", "\t")
			want = strings.ReplaceAll(want, "\t", "")

			if strGot != want {
				t.Errorf("folder.Render() = %v, want %v", strGot, want)
			}
		})
	}
}

func Test_folder_RenderAzureADSecurity(t *testing.T) {
	f := &folder{}
	f.Properties.setSecurity(nil, &folderSecurity{
		AuthorizationStrategy: folderAuthorizationStrategyAzureAD,
		InheritanceStrategy: folderPermissionInheritanceStrategy{
			Class: defaultFolderInheritanceStrategy,
		},
		Permission: []string{"GROUP:hudson.model.View.Read:authenticated"},
	})

	got, err := f.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "<com.microsoft.jenkins.azuread.AzureAdAuthorizationMatrixFolderProperty>") {
		t.Fatalf("Azure AD authorization property was not rendered:\n%s", got)
	}
	if strings.Contains(string(got), "<com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty>") {
		t.Fatalf("matrix authorization property was also rendered:\n%s", got)
	}
}

func TestFolderPropertiesSetSecurity(t *testing.T) {
	matrix := &folderSecurity{AuthorizationStrategy: folderAuthorizationStrategyMatrix}
	azureAD := &folderSecurity{
		AuthorizationStrategy: folderAuthorizationStrategyAzureAD,
		Plugin:                "azure-ad@580.v2f665882b_a_71",
		Extra: []xmlRawProperty{
			{XMLName: xml.Name{Local: "entries"}, Raw: "<entry>keep-me</entry>"},
		},
	}
	desiredAzureAD := &folderSecurity{
		AuthorizationStrategy: folderAuthorizationStrategyAzureAD,
		InheritanceStrategy:   folderPermissionInheritanceStrategy{Class: defaultFolderInheritanceStrategy},
		Permission:            []string{"GROUP:hudson.model.View.Read:authenticated"},
	}

	properties := folderProperties{Security: matrix, AzureADSecurity: azureAD}
	properties.setSecurity(matrix, desiredAzureAD)
	if properties.Security != nil {
		t.Fatal("previously managed matrix property was not removed")
	}
	if properties.AzureADSecurity == nil || properties.AzureADSecurity.Plugin != azureAD.Plugin {
		t.Fatal("Azure AD property metadata was not preserved")
	}
	rendered, err := (&folder{Properties: properties}).Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), `plugin="azure-ad@580.v2f665882b_a_71"`) || !strings.Contains(string(rendered), "<entries><entry>keep-me</entry></entries>") {
		t.Fatalf("Azure AD property metadata was lost during render:\n%s", rendered)
	}

	properties = folderProperties{AzureADSecurity: azureAD}
	properties.setSecurity(nil, nil)
	rendered, err = (&folder{Properties: properties}).Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), `plugin="azure-ad@580.v2f665882b_a_71"`) || !strings.Contains(string(rendered), "<entries><entry>keep-me</entry></entries>") {
		t.Fatalf("unmanaged Azure AD property was changed during render:\n%s", rendered)
	}

	properties.setSecurity(azureAD, nil)
	if properties.AzureADSecurity != nil {
		t.Fatal("previously managed Azure AD property was not removed")
	}
}

func TestHandleXml(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "double-quote-1.1",
			input: `<?xml version="1.1" encoding="UTF-8"?><root/>`,
			want:  `<?xml version="1.0" encoding="UTF-8"?><root/>`,
		},
		{
			name:  "single-quote-1.1",
			input: `<?xml version='1.1' encoding='UTF-8'?><root/>`,
			want:  `<?xml version='1.0' encoding='UTF-8'?><root/>`,
		},
		{
			name:  "already-1.0-unchanged",
			input: `<?xml version="1.0" encoding="UTF-8"?><root/>`,
			want:  `<?xml version="1.0" encoding="UTF-8"?><root/>`,
		},
		{
			name:  "no-declaration-unchanged",
			input: `<root/>`,
			want:  `<root/>`,
		},
		{
			name:  "both-variants-in-one-string",
			input: `<?xml version="1.1"?><?xml version='1.1'?><root/>`,
			want:  `<?xml version="1.0"?><?xml version='1.0'?><root/>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(handleXml(tt.input)); got != tt.want {
				t.Errorf("handleXml() = %q, want %q", got, tt.want)
			}
		})
	}
}
