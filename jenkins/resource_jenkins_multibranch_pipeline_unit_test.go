package jenkins

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestMultibranchConfigXMLRoundTrip verifies the generated config.xml carries the
// expected XStream classes and round-trips through the read path with identical
// values, so Read does not report spurious drift.
func TestMultibranchConfigXMLRoundTrip(t *testing.T) {
	m := multibranchPipelineResourceModel{
		Name:          types.StringValue("svc"),
		Description:   types.StringValue("my <svc> & repo"),
		Remote:        types.StringValue("https://github.com/org/repo.git"),
		CredentialsID: types.StringValue("git-token"),
		ScriptPath:    types.StringValue("ci/Jenkinsfile"),
	}

	xmlStr, err := m.configXML()
	if err != nil {
		t.Fatalf("configXML: %v", err)
	}

	for _, want := range []string{multibranchProjectClass, branchSourceListClass, gitSCMSourceClass, branchFactoryClass} {
		if !strings.Contains(xmlStr, want) {
			t.Errorf("config XML missing class %q", want)
		}
	}

	var proj multibranchProject
	if err := xml.Unmarshal(stripXMLDeclaration([]byte(xmlStr)), &proj); err != nil {
		t.Fatalf("unmarshal round-trip: %v", err)
	}

	if proj.Description != m.Description.ValueString() {
		t.Errorf("Description = %q, want %q", proj.Description, m.Description.ValueString())
	}
	if got := proj.Sources.Data.BranchSource.Source.Remote; got != m.Remote.ValueString() {
		t.Errorf("Remote = %q, want %q", got, m.Remote.ValueString())
	}
	if got := proj.Sources.Data.BranchSource.Source.CredentialsID; got != m.CredentialsID.ValueString() {
		t.Errorf("CredentialsID = %q, want %q", got, m.CredentialsID.ValueString())
	}
	if got := proj.Factory.ScriptPath; got != m.ScriptPath.ValueString() {
		t.Errorf("ScriptPath = %q, want %q", got, m.ScriptPath.ValueString())
	}
}
