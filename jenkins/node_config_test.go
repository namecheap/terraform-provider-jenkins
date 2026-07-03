package jenkins

import (
	"encoding/xml"
	"testing"
)

// TestNodeConfigParse verifies that a node config.xml with the XML 1.1 declaration
// Jenkins emits is decoded into nodeConfig with the expected field mapping.
func TestNodeConfigParse(t *testing.T) {
	raw := []byte(`<?xml version='1.1' encoding='UTF-8'?>
<slave>
  <name>tf-acc-node</name>
  <description>managed by terraform</description>
  <remoteFS>/home/jenkins/agent</remoteFS>
  <numExecutors>2</numExecutors>
  <mode>NORMAL</mode>
  <retentionStrategy class="hudson.slaves.RetentionStrategy$Always"/>
  <launcher class="hudson.slaves.JNLPLauncher"/>
  <label>linux docker</label>
  <nodeProperties/>
</slave>`)

	var cfg nodeConfig
	if err := xml.Unmarshal(stripXMLDeclaration(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if cfg.Name != "tf-acc-node" {
		t.Errorf("Name = %q, want tf-acc-node", cfg.Name)
	}
	if cfg.Description != "managed by terraform" {
		t.Errorf("Description = %q", cfg.Description)
	}
	if cfg.RemoteFS != "/home/jenkins/agent" {
		t.Errorf("RemoteFS = %q", cfg.RemoteFS)
	}
	if cfg.NumExecutors != 2 {
		t.Errorf("NumExecutors = %d, want 2", cfg.NumExecutors)
	}
	if cfg.Label != "linux docker" {
		t.Errorf("Label = %q, want 'linux docker'", cfg.Label)
	}
}

func TestStripXMLDeclaration(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"xml 1.1", `<?xml version='1.1' encoding='UTF-8'?><slave/>`, `<slave/>`},
		{"xml 1.0", `<?xml version="1.0"?><slave/>`, `<slave/>`},
		{"no declaration", `<slave/>`, `<slave/>`},
		{"leading whitespace", "\n  <?xml version='1.1'?><slave/>", `<slave/>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(stripXMLDeclaration([]byte(tc.in))); got != tc.want {
				t.Errorf("stripXMLDeclaration(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
