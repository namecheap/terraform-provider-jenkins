package jenkins

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestPipelineJobConfigXMLRoundTrip verifies that the generated config.xml escapes
// the Groovy script and round-trips through the read path (stripXMLDeclaration +
// unmarshal) with identical values, so Read does not report spurious drift.
func TestPipelineJobConfigXMLRoundTrip(t *testing.T) {
	script := "pipeline {\n  agent any\n  stages { stage('x') { steps { echo \"a <b> & c\" } } }\n}"
	m := pipelineJobResourceModel{
		Description: types.StringValue("desc with <angle> & amp"),
		Script:      types.StringValue(script),
		Sandbox:     types.BoolValue(false),
		Disabled:    types.BoolValue(true),
	}

	xmlStr, err := m.configXML()
	if err != nil {
		t.Fatalf("configXML: %v", err)
	}

	// The raw script must not appear verbatim: its special characters are escaped.
	if strings.Contains(xmlStr, "a <b> & c") {
		t.Errorf("script was not XML-escaped in:\n%s", xmlStr)
	}
	if !strings.Contains(xmlStr, cpsFlowDefinitionClass) {
		t.Errorf("definition class missing from config XML")
	}

	var fd flowDefinition
	if err := xml.Unmarshal(stripXMLDeclaration([]byte(xmlStr)), &fd); err != nil {
		t.Fatalf("unmarshal round-trip: %v", err)
	}

	if fd.Description != m.Description.ValueString() {
		t.Errorf("Description = %q, want %q", fd.Description, m.Description.ValueString())
	}
	if fd.Definition.Script != script {
		t.Errorf("Script did not round-trip:\n got %q\nwant %q", fd.Definition.Script, script)
	}
	if fd.Definition.Sandbox != false {
		t.Errorf("Sandbox = %v, want false", fd.Definition.Sandbox)
	}
	if fd.Disabled != true {
		t.Errorf("Disabled = %v, want true", fd.Disabled)
	}
}
