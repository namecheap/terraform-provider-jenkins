package jenkins

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// nodeConfigFixture is a permanent inbound node as Jenkins 2.583 exports it,
// with a property the resource does not manage next to the Job Restrictions
// one.
const nodeConfigFixture = `<?xml version="1.1" encoding="UTF-8"?>
<slave>
  <name>agent1</name>
  <remoteFS>/var/lib/jenkins-agent</remoteFS>
  <numExecutors>2</numExecutors>
  <mode>EXCLUSIVE</mode>
  <retentionStrategy class="hudson.slaves.RetentionStrategy$Always"/>
  <launcher class="hudson.slaves.JNLPLauncher">
    <workDirSettings>
      <disabled>false</disabled>
      <internalDir>remoting</internalDir>
      <failIfWorkDirIsMissing>false</failIfWorkDirIsMissing>
    </workDirSettings>
    <webSocket>true</webSocket>
  </launcher>
  <label>platform</label>
  <nodeProperties>
    <hudson.slaves.EnvironmentVariablesNodeProperty>
      <envVars serialization="custom">
        <unserializable-parents/>
        <tree-map>
          <default>
            <comparator class="java.lang.String$CaseInsensitiveComparator"/>
          </default>
          <int>1</int>
          <string>FOO</string>
          <string>bar</string>
        </tree-map>
      </envVars>
    </hudson.slaves.EnvironmentVariablesNodeProperty>
    <com.synopsys.arc.jenkinsci.plugins.jobrestrictions.nodes.JobRestrictionProperty plugin="job-restrictions@242.v6edda_c9e4ca_f">
      <jobRestriction class="com.synopsys.arc.jenkinsci.plugins.jobrestrictions.restrictions.job.RegexNameRestriction">
        <regexExpression>^platform/apps/.*</regexExpression>
        <checkShortName>false</checkShortName>
      </jobRestriction>
    </com.synopsys.arc.jenkinsci.plugins.jobrestrictions.nodes.JobRestrictionProperty>
  </nodeProperties>
</slave>`

const (
	envVarsPropertyXML = `<hudson.slaves.EnvironmentVariablesNodeProperty><envVars serialization="custom"><unserializable-parents/><tree-map><default><comparator class="java.lang.String$CaseInsensitiveComparator"/></default><int>1</int><string>FOO</string><string>bar</string></tree-map></envVars></hudson.slaves.EnvironmentVariablesNodeProperty>`
	anyRestrictionXML  = `<com.synopsys.arc.jenkinsci.plugins.jobrestrictions.nodes.JobRestrictionProperty><jobRestriction class="com.synopsys.arc.jenkinsci.plugins.jobrestrictions.restrictions.logic.AnyJobRestriction"/></com.synopsys.arc.jenkinsci.plugins.jobrestrictions.nodes.JobRestrictionProperty>`
)

func regexRestrictionXML(regex string, checkShortName bool) string {
	check := "false"
	if checkShortName {
		check = "true"
	}
	return `<com.synopsys.arc.jenkinsci.plugins.jobrestrictions.nodes.JobRestrictionProperty plugin="job-restrictions@242.v6edda_c9e4ca_f">` +
		`<jobRestriction class="com.synopsys.arc.jenkinsci.plugins.jobrestrictions.restrictions.job.RegexNameRestriction">` +
		`<regexExpression>` + regex + `</regexExpression><checkShortName>` + check + `</checkShortName>` +
		`</jobRestriction></com.synopsys.arc.jenkinsci.plugins.jobrestrictions.nodes.JobRestrictionProperty>`
}

// nodeXML returns a minimal node config.xml with the given launcher element
// and nodeProperties content.
func nodeXML(launcher, properties string) string {
	return `<slave><name>agent1</name><remoteFS>/x</remoteFS><numExecutors>1</numExecutors><mode>NORMAL</mode>` +
		launcher + `<label></label><nodeProperties>` + properties + `</nodeProperties></slave>`
}

func parseNodeConfig(t *testing.T, doc string) nodeConfig {
	t.Helper()
	var cfg nodeConfig
	if err := xml.Unmarshal(stripXMLDeclaration([]byte(doc)), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return cfg
}

func renderNodeConfig(t *testing.T, cfg nodeConfig) string {
	t.Helper()
	out, err := cfg.render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return string(out)
}

func TestNodeConfigJobRestriction(t *testing.T) {
	cases := []struct {
		name       string
		properties string
		want       *jobRestriction
	}{
		{"absent", "", nil},
		{"other property only", envVarsPropertyXML, nil},
		{"regex", regexRestrictionXML("^platform/apps/.*", false), &jobRestriction{Regex: "^platform/apps/.*"}},
		{"check short name", regexRestrictionXML("^build$", true), &jobRestriction{Regex: "^build$", CheckShortName: true}},
		{"escaped regex", regexRestrictionXML("^a&lt;&amp;", false), &jobRestriction{Regex: "^a<&"}},
		// Only the regex restriction has a place in the schema; another type
		// set in the UI reads as no restriction.
		{"other restriction type", anyRestrictionXML, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseNodeConfig(t, nodeXML("", tc.properties)).jobRestriction()
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("jobRestriction() = %+v, want nil", *got)
			case tc.want != nil && (got == nil || *got != *tc.want):
				t.Errorf("jobRestriction() = %v, want %+v", got, *tc.want)
			}
		})
	}
}

func TestNodeConfigWebSocket(t *testing.T) {
	cases := []struct {
		name     string
		launcher string
		want     bool
	}{
		{"inbound over WebSocket", `<launcher class="hudson.slaves.JNLPLauncher"><webSocket>true</webSocket></launcher>`, true},
		{"inbound over TCP", `<launcher class="hudson.slaves.JNLPLauncher"><webSocket>false</webSocket></launcher>`, false},
		{"inbound without the element", `<launcher class="hudson.slaves.JNLPLauncher"/>`, false},
		{"no launcher", "", false},
		{"ssh launcher", `<launcher class="hudson.plugins.sshslaves.SSHLauncher" plugin="ssh-slaves@3.0"><host>h</host></launcher>`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseNodeConfig(t, nodeXML(tc.launcher, "")).webSocket(); got != tc.want {
				t.Errorf("webSocket() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNodeConfigApply drives the read-modify-write the resource performs on
// update: managed fields take the planned values, everything else Jenkins
// exported is written back.
func TestNodeConfigApply(t *testing.T) {
	cfg := parseNodeConfig(t, nodeConfigFixture)
	err := cfg.apply(nodeResourceModel{
		Name:         types.StringValue("agent1"),
		NumExecutors: types.Int64Value(4),
		Description:  types.StringValue("d & <x>"),
		RemoteFS:     types.StringValue("/srv/agent"),
		Labels:       types.StringValue("a b"),
		Mode:         types.StringValue("NORMAL"),
		WebSocket:    types.BoolValue(false),
		JobRestriction: &nodeJobRestrictionModel{
			Regex:          types.StringValue("^x/(y|z)<&"),
			CheckShortName: types.BoolValue(true),
		},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	out := renderNodeConfig(t, cfg)

	if !strings.HasPrefix(out, "<slave>") {
		t.Errorf("root element changed: %.40s", out)
	}
	got := parseNodeConfig(t, out)
	if got.Name != "agent1" || got.Description != "d & <x>" || got.RemoteFS != "/srv/agent" ||
		got.NumExecutors != 4 || got.Label != "a b" || got.mode() != "NORMAL" {
		t.Errorf("managed fields = %+v", got)
	}
	if got.webSocket() {
		t.Error("webSocket() = true, want false")
	}
	if r := got.jobRestriction(); r == nil || *r != (jobRestriction{Regex: "^x/(y|z)<&", CheckShortName: true}) {
		t.Errorf("jobRestriction() = %v", r)
	}
	if n := strings.Count(out, "<com.synopsys.arc.jenkinsci.plugins.jobrestrictions.nodes.JobRestrictionProperty"); n != 1 {
		t.Errorf("%d JobRestrictionProperty elements, want 1", n)
	}
	for _, kept := range []string{
		`<retentionStrategy class="hudson.slaves.RetentionStrategy$Always">`,
		`<launcher class="hudson.slaves.JNLPLauncher">`,
		`<internalDir>remoting</internalDir>`,
		`<envVars serialization="custom">`,
		`<comparator class="java.lang.String$CaseInsensitiveComparator"/>`,
		`<string>FOO</string>`,
	} {
		if !strings.Contains(out, kept) {
			t.Errorf("rendered config lost %s:\n%s", kept, out)
		}
	}
}

func TestNodeConfigSetJobRestriction(t *testing.T) {
	regex := &jobRestriction{Regex: "^a/.*"}
	cases := []struct {
		name       string
		properties string
		set        *jobRestriction
		want       *jobRestriction
		// wantAny is whether a restriction of another type survives.
		wantAny bool
	}{
		{"add next to another property", envVarsPropertyXML, regex, regex, false},
		{"replace regex", regexRestrictionXML("^old", true), regex, regex, false},
		{"remove regex", envVarsPropertyXML + regexRestrictionXML("^old", false), nil, nil, false},
		// The resource owns the regex restriction only: another type set in
		// the UI is left alone while the block is unset...
		{"keep other type when unset", anyRestrictionXML, nil, nil, true},
		// ...and replaced once it is set, Jenkins holding one per node.
		{"replace other type when set", anyRestrictionXML, regex, regex, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := parseNodeConfig(t, nodeXML("", tc.properties))
			if err := cfg.setJobRestriction(tc.set); err != nil {
				t.Fatalf("setJobRestriction: %v", err)
			}
			out := renderNodeConfig(t, cfg)

			got := parseNodeConfig(t, out).jobRestriction()
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("jobRestriction() = %+v, want nil", *got)
			case tc.want != nil && (got == nil || *got != *tc.want):
				t.Errorf("jobRestriction() = %v, want %+v", got, *tc.want)
			}
			if hasAny := strings.Contains(out, "AnyJobRestriction"); hasAny != tc.wantAny {
				t.Errorf("AnyJobRestriction present = %v, want %v", hasAny, tc.wantAny)
			}
			if strings.Contains(tc.properties, "<string>FOO</string>") && !strings.Contains(out, "<string>FOO</string>") {
				t.Errorf("unmanaged property lost:\n%s", out)
			}
		})
	}
}

func TestNodeConfigSetWebSocket(t *testing.T) {
	const ssh = `<launcher class="hudson.plugins.sshslaves.SSHLauncher" plugin="ssh-slaves@3.0"><host>h</host></launcher>`
	cases := []struct {
		name     string
		launcher string
		on       bool
		wantErr  bool
	}{
		{"enable on inbound", `<launcher class="hudson.slaves.JNLPLauncher"><webSocket>false</webSocket></launcher>`, true, false},
		{"disable on inbound", `<launcher class="hudson.slaves.JNLPLauncher"><webSocket>true</webSocket></launcher>`, false, false},
		{"enable on inbound without the element", `<launcher class="hudson.slaves.JNLPLauncher"/>`, true, false},
		{"leave ssh alone", ssh, false, false},
		{"enable on ssh", ssh, true, true},
		{"enable without launcher", "", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := parseNodeConfig(t, nodeXML(tc.launcher, ""))
			err := cfg.setWebSocket(tc.on)
			if (err != nil) != tc.wantErr {
				t.Fatalf("setWebSocket(%v) error = %v, wantErr %v", tc.on, err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			out := renderNodeConfig(t, cfg)
			if got := parseNodeConfig(t, out).webSocket(); got != tc.on {
				t.Errorf("webSocket() after set = %v, want %v", got, tc.on)
			}
			// An SSH launcher has no webSocket field: XStream would reject it.
			if tc.launcher == ssh && (strings.Contains(out, "webSocket") || !strings.Contains(out, "<host>h</host>")) {
				t.Errorf("ssh launcher changed:\n%s", out)
			}
		})
	}
}

func TestUpdateNodeConfig(t *testing.T) {
	t.Run("posts the document", func(t *testing.T) {
		var gotMethod, gotPath, gotType, gotBody string
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath, gotType = r.Method, r.URL.EscapedPath(), r.Header.Get("Content-Type")
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
		})
		defer srv.Close()

		if err := covBClient(t, srv).UpdateNodeConfig(context.Background(), "linux agent", []byte("<slave/>")); err != nil {
			t.Fatalf("UpdateNodeConfig: %v", err)
		}
		if gotMethod != http.MethodPost || gotPath != "/computer/linux%20agent/config.xml" {
			t.Errorf("request = %s %s, want POST /computer/linux%%20agent/config.xml", gotMethod, gotPath)
		}
		if !strings.HasPrefix(gotType, "application/xml") {
			t.Errorf("Content-Type = %q, want application/xml", gotType)
		}
		if gotBody != "<slave/>" {
			t.Errorf("body = %q, want <slave/>", gotBody)
		}
	})

	t.Run("error status", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		})
		defer srv.Close()

		err := covBClient(t, srv).UpdateNodeConfig(context.Background(), "agent1", []byte("<slave/>"))
		if err == nil || !strings.Contains(err.Error(), "403") {
			t.Fatalf("error = %v, want it to report 403", err)
		}
	})
}

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
	if cfg.Mode != "NORMAL" {
		t.Errorf("Mode = %q, want NORMAL", cfg.Mode)
	}
}

// TestNodeConfigMode verifies that a config.xml without <mode> reads back as
// NORMAL, so Read agrees with the schema default instead of planning a
// change on every run.
func TestNodeConfigMode(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"missing", "", "NORMAL"},
		{"normal", "NORMAL", "NORMAL"},
		{"exclusive", "EXCLUSIVE", "EXCLUSIVE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (nodeConfig{Mode: tc.in}).mode(); got != tc.want {
				t.Errorf("mode() with Mode=%q = %q, want %q", tc.in, got, tc.want)
			}
		})
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
