package jenkins

import (
	"reflect"
	"strings"
	"testing"
)

func TestIsSecretPlaceholder(t *testing.T) {
	cases := map[string]struct {
		in   interface{}
		want bool
	}{
		"bare placeholder":         {"${SECRET}", true},
		"placeholder with default": {"${SECRET:-fallback}", true},
		"padded placeholder":       {"  ${SECRET}  ", true},
		"plain string":             {"hello", false},
		"embedded not whole":       {"prefix-${SECRET}", false},
		"non-string int":           {42, false},
		"nil":                      {nil, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := isSecretPlaceholder(tc.in); got != tc.want {
				t.Errorf("isSecretPlaceholder(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseYAML(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		v, err := parseYAML("a: 1\nb: two\n")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		m, ok := v.(map[string]interface{})
		if !ok {
			t.Fatalf("expected a mapping, got %T", v)
		}
		if m["a"] != 1 || m["b"] != "two" {
			t.Errorf("unexpected parse result: %#v", m)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		if _, err := parseYAML("a: [unterminated\n"); err == nil {
			t.Fatal("expected a parse error for malformed YAML")
		}
	})
}

func TestYAMLSubset(t *testing.T) {
	mustParse := func(s string) interface{} {
		v, err := parseYAML(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return v
	}

	cases := []struct {
		name             string
		declared, actual string
		want             bool
	}{
		{"equal scalars", "x", "x", true},
		{"unequal scalars", "x", "y", false},
		{"map subset ignores extra keys", "a: 1", "a: 1\nb: 2", true},
		{"map missing declared key", "a: 1\nc: 3", "a: 1\nb: 2", false},
		{"map changed value is drift", "a: 1", "a: 2", false},
		{"nested map subset", "outer:\n  a: 1", "outer:\n  a: 1\n  b: 2", true},
		{"nested map drift", "outer:\n  a: 1", "outer:\n  a: 9", false},
		{"equal lists", "[1, 2, 3]", "[1, 2, 3]", true},
		{"list length differs", "[1, 2]", "[1, 2, 3]", false},
		{"list element differs", "[1, 2, 3]", "[1, 2, 4]", false},
		{"secret placeholder ignores actual", "token: ${SECRET}", "token: resolved-value", true},
		{"secret placeholder with default", "token: ${SECRET:-x}", "token: anything", true},
		{"type mismatch map vs scalar", "a: 1", "a", false},
		{"bool values", "flag: true", "flag: true\nother: false", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := yamlSubset(mustParse(tc.declared), mustParse(tc.actual))
			if got != tc.want {
				t.Errorf("yamlSubset(%q, %q) = %v, want %v", tc.declared, tc.actual, got, tc.want)
			}
		})
	}
}

func TestWrapSection(t *testing.T) {
	t.Run("wraps a subtree under its section key", func(t *testing.T) {
		doc, err := wrapSection("jenkins", "systemMessage: hi")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		v, err := parseYAML(doc)
		if err != nil {
			t.Fatalf("wrapped doc is not valid YAML: %v", err)
		}
		m, ok := v.(map[string]interface{})
		if !ok {
			t.Fatalf("expected a mapping, got %T", v)
		}
		inner, ok := m["jenkins"].(map[string]interface{})
		if !ok || inner["systemMessage"] != "hi" {
			t.Errorf("unexpected wrapped structure: %#v", m)
		}
	})

	t.Run("invalid YAML errors", func(t *testing.T) {
		if _, err := wrapSection("jenkins", "a: [bad\n"); err == nil {
			t.Error("expected an error for malformed YAML")
		}
	})
}

func TestExtractSectionYAML(t *testing.T) {
	exported := "jenkins:\n  systemMessage: hi\nsecurity:\n  x: 1\n"

	t.Run("extracts and round-trips a section", func(t *testing.T) {
		sub, found, err := extractSectionYAML(exported, "jenkins")
		if err != nil || !found {
			t.Fatalf("found=%v err=%v", found, err)
		}
		v, _ := parseYAML(sub)
		m, ok := v.(map[string]interface{})
		if !ok || m["systemMessage"] != "hi" {
			t.Errorf("unexpected extracted subtree: %#v", v)
		}
	})

	t.Run("absent section", func(t *testing.T) {
		_, found, err := extractSectionYAML(exported, "tool")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found {
			t.Error("expected found=false for an absent section")
		}
	})

	t.Run("invalid YAML errors", func(t *testing.T) {
		if _, _, err := extractSectionYAML("a: [bad\n", "jenkins"); err == nil {
			t.Error("expected an error for malformed YAML")
		}
	})
}

func TestCASCInSync(t *testing.T) {
	exported := `
jenkins:
  systemMessage: "Managed by Terraform"
  numExecutors: 2
  mode: NORMAL
security:
  apiToken:
    creationOfLegacyTokenEnabled: false
`

	t.Run("declared subtree in sync", func(t *testing.T) {
		declared := `systemMessage: "Managed by Terraform"`
		ok, err := cascInSync(declared, exported, "jenkins")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok {
			t.Error("expected in-sync (no drift)")
		}
	})

	t.Run("unmanaged keys in export are ignored", func(t *testing.T) {
		// Declares only systemMessage; numExecutors/mode in the export must not
		// register as drift.
		declared := `systemMessage: "Managed by Terraform"`
		ok, err := cascInSync(declared, exported, "jenkins")
		if err != nil || !ok {
			t.Errorf("expected in-sync ignoring unmanaged keys, got ok=%v err=%v", ok, err)
		}
	})

	t.Run("changed managed value is drift", func(t *testing.T) {
		declared := `systemMessage: "Different message"`
		ok, err := cascInSync(declared, exported, "jenkins")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			t.Error("expected drift for a changed managed value")
		}
	})

	t.Run("secret placeholder never drifts", func(t *testing.T) {
		exportedWithToken := "unclassified:\n  myPlugin:\n    token: \"resolved-secret-value\"\n"
		declared := "myPlugin:\n  token: ${MY_TOKEN}\n"
		ok, err := cascInSync(declared, exportedWithToken, "unclassified")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok {
			t.Error("expected no drift: a ${...} secret must be compared structurally")
		}
	})

	t.Run("section absent with nothing declared is in sync", func(t *testing.T) {
		ok, err := cascInSync("", exported, "tool")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok {
			t.Error("expected in-sync when the section is absent and nothing declared")
		}
	})

	t.Run("section absent with something declared is drift", func(t *testing.T) {
		declared := `installations: []`
		ok, err := cascInSync(declared, exported, "tool")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			t.Error("expected drift when a declared section is missing from the controller")
		}
	})

	t.Run("invalid declared YAML errors", func(t *testing.T) {
		if _, err := cascInSync("a: [bad\n", exported, "jenkins"); err == nil {
			t.Error("expected an error for malformed declared YAML")
		}
	})

	t.Run("invalid exported YAML errors", func(t *testing.T) {
		if _, err := cascInSync("a: 1", "b: [bad\n", "jenkins"); err == nil {
			t.Error("expected an error for malformed exported YAML")
		}
	})
}

func TestCASCDriftView(t *testing.T) {
	marker := "'" + cascDriftMarker + "'"
	cases := []struct {
		name             string
		declared, export string
		want             string
		wantNotContain   []string
	}{
		{
			name:     "placeholder kept while a sibling drifts",
			declared: "token: ${TOKEN}\nurl: http://a/\n",
			export:   "s:\n  token: resolved-token\n  url: http://b/\n  extra: 1\n",
			want:     "token: ${TOKEN}\nurl: http://b/\n",
		},
		{
			name:     "declared key missing on the controller is dropped",
			declared: "a: 1\nb: 2\n",
			export:   "s:\n  a: 1\n",
			want:     "a: 1\n",
		},
		{
			name:     "declared scalar replaced by a mapping is hidden",
			declared: "auth: none\n",
			export:   "s:\n  auth:\n    password: resolved-pw\n",
			want:     "auth: " + marker + "\n",
		},
		{
			name:     "declared mapping replaced by a scalar is hidden",
			declared: "auth:\n  user: u\n",
			export:   "s:\n  auth: other\n",
			want:     "auth: " + marker + "\n",
		},
		{
			name:     "scalar sequence grows",
			declared: "list: [a]\n",
			export:   "s:\n  list: [a, b]\n",
			want:     "list: [a, b]\n",
		},
		{
			name:     "scalar sequence shrinks",
			declared: "list: [a, b]\n",
			export:   "s:\n  list: [b]\n",
			want:     "list: [b]\n",
		},
		{
			name:           "extra mapping element is hidden",
			declared:       "list:\n  - name: a\n",
			export:         "s:\n  list:\n    - name: a\n      x: 1\n    - name: b\n      password: resolved-pw\n",
			want:           "list:\n  - name: a\n  - " + marker + "\n",
			wantNotContain: []string{"resolved-pw"},
		},
		{
			name:           "drifted sequence holding a placeholder is hidden whole",
			declared:       "list:\n  - ${A}\n  - b\n",
			export:         "s:\n  list: [b, resolved-a]\n",
			want:           "list: " + marker + "\n",
			wantNotContain: []string{"resolved-a"},
		},
		{
			name:           "embedded interpolation is never copied",
			declared:       "url: https://${USER}@host/\n",
			export:         "s:\n  url: https://resolved-user@host/\n",
			want:           "url: " + marker + "\n",
			wantNotContain: []string{"resolved-user"},
		},
		{
			name:     "empty declared document is hidden",
			declared: "",
			export:   "s:\n  a: 1\n",
			want:     marker + "\n",
		},
		{
			name:           "declared scalar replaced by a sequence is hidden",
			declared:       "users: none\n",
			export:         "s:\n  users:\n    - name: admin\n      password: resolved-pw\n",
			want:           "users: " + marker + "\n",
			wantNotContain: []string{"resolved-pw", "admin"},
		},
		{
			name:           "declared sequence replaced by a mapping is hidden",
			declared:       "list: [a]\n",
			export:         "s:\n  list:\n    password: resolved-pw\n",
			want:           "list: " + marker + "\n",
			wantNotContain: []string{"resolved-pw"},
		},
		{
			name:     "declared sequence replaced by a scalar is hidden",
			declared: "list: [a]\n",
			export:   "s:\n  list: b\n",
			want:     "list: " + marker + "\n",
		},
		{
			name:           "extra sequence element is hidden",
			declared:       "list: [a]\n",
			export:         "s:\n  list:\n    - a\n    - [b, resolved-pw]\n",
			want:           "list:\n  - a\n  - " + marker + "\n",
			wantNotContain: []string{"resolved-pw"},
		},
		{
			// yaml.v3 decodes a mapping with a non-string key as
			// map[interface{}]interface{}, which takes the scalar path.
			name:           "drifted mapping with non-string keys is hidden",
			declared:       "ports:\n  8080: http\n",
			export:         "s:\n  ports:\n    8080: https\n    9090: resolved-pw\n",
			want:           "ports: " + marker + "\n",
			wantNotContain: []string{"resolved-pw", "9090"},
		},
		{
			name:           "placeholder under a non-string key is never copied",
			declared:       "ports:\n  8080: ${PORT_TOKEN}\n",
			export:         "s:\n  ports: resolved-port-token\n",
			want:           "ports: " + marker + "\n",
			wantNotContain: []string{"resolved-port-token"},
		},
		{
			name:           "drifted sequence holding a placeholder under a non-string key is hidden whole",
			declared:       "list:\n  - 8080: ${PORT_TOKEN}\n  - b\n",
			export:         "s:\n  list: [resolved-port-token, c]\n",
			want:           "list: " + marker + "\n",
			wantNotContain: []string{"resolved-port-token"},
		},
		{
			name:           "embedded placeholder with a default is never copied",
			declared:       "url: jdbc:postgresql://db/app?password=${DB_PASS:-changeme}\n",
			export:         "s:\n  url: jdbc:postgresql://db/app?password=resolved-db-pass\n",
			want:           "url: " + marker + "\n",
			wantNotContain: []string{"resolved-db-pass"},
		},
		{
			// ${A:-${B}} is not a whole placeholder to isSecretPlaceholder, so
			// it always drifts; the resolved value must still stay hidden.
			name:           "nested placeholder default is never copied",
			declared:       "token: ${A:-${B}}\n",
			export:         "s:\n  token: resolved-b\n",
			want:           "token: " + marker + "\n",
			wantNotContain: []string{"resolved-b"},
		},
		{
			name:           "multi-line string with an embedded placeholder is never copied",
			declared:       "script: |\n  echo start\n  curl -u admin:${API_TOKEN} http://x/\n",
			export:         "s:\n  script: \"echo start\\ncurl -u admin:resolved-api-token http://x/\\n\"\n",
			want:           "script: " + marker + "\n",
			wantNotContain: []string{"resolved-api-token", "echo start"},
		},
		{
			name:           "embedded placeholder deep in a mapping is never copied",
			declared:       "a:\n  b:\n    c: pre-${S}\n  d: 1\n",
			export:         "s:\n  a:\n    b:\n      c: pre-resolved-s\n      x: unmanaged\n    d: 1\n",
			want:           "a:\n  b:\n    c: " + marker + "\n  d: 1\n",
			wantNotContain: []string{"resolved-s", "unmanaged"},
		},
		{
			name:           "padded placeholder kept while a sibling drifts",
			declared:       "token: \"  ${TOKEN}  \"\nurl: http://a/\n",
			export:         "s:\n  token: resolved-token\n  url: http://b/\n",
			want:           "token: \"  ${TOKEN}  \"\nurl: http://b/\n",
			wantNotContain: []string{"resolved-token"},
		},
		{
			// JCasC exports a Secret-typed field as ciphertext, so a literal
			// declared for it never matches and is copied as a drifted plain
			// scalar. The literal is already in the configuration and state;
			// ${...} keeps both out.
			name:     "literal for a Secret field takes the exported ciphertext",
			declared: "password: dummy-literal-password\nurl: http://a/\n",
			export:   "s:\n  password: \"" + cascTestCiphertext + "\"\n  url: http://a/\n",
			want:     "password: \"" + cascTestCiphertext + "\"\nurl: http://a/\n",
		},
		{
			name:           "placeholder for a Secret field never takes the exported ciphertext",
			declared:       "password: ${PASSWORD}\nurl: http://a/\n",
			export:         "s:\n  password: \"" + cascTestCiphertext + "\"\n  url: http://b/\n",
			want:           "password: ${PASSWORD}\nurl: http://b/\n",
			wantNotContain: []string{cascTestCiphertext},
		},
		{
			// Extra scalar elements are the controller's value for a list
			// declared without ${...}, copied like a drifted plain scalar
			// whatever they hold: ciphertext, or a value another JCasC
			// source resolved.
			name:     "extra scalar elements of a plain list are copied verbatim",
			declared: "list: [a]\n",
			export:   "s:\n  list: [a, \"" + cascTestCiphertext + "\", resolved-elsewhere]\n",
			want:     "list: [a, \"" + cascTestCiphertext + "\", resolved-elsewhere]\n",
		},
		{
			name:           "extra scalar elements of a list holding a placeholder are hidden",
			declared:       "list: [\"${A}\"]\n",
			export:         "s:\n  list: [resolved-a, \"" + cascTestCiphertext + "\", resolved-elsewhere]\n",
			want:           "list: " + marker + "\n",
			wantNotContain: []string{"resolved-a", cascTestCiphertext, "resolved-elsewhere"},
		},
		{
			// Values are hidden by where this document declares a ${...},
			// not by content: a key declared as a plain value takes the
			// controller's value even when another resource or JCasC file
			// resolved it from a ${...}.
			name:           "plain key resolved by another source is copied",
			declared:       "token: ${TOKEN}\nadminAddress: ops@example.invalid\n",
			export:         "s:\n  token: resolved-token\n  adminAddress: resolved-elsewhere\n",
			want:           "token: ${TOKEN}\nadminAddress: resolved-elsewhere\n",
			wantNotContain: []string{"resolved-token"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view, found, err := cascDriftView(tc.declared, tc.export, "s")
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			got, want := mustParseYAML(t, view), mustParseYAML(t, tc.want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("view mismatch:\n got: %s\nwant: %s", view, tc.want)
			}
			for _, s := range tc.wantNotContain {
				if strings.Contains(view, s) {
					t.Errorf("view contains %q:\n%s", s, view)
				}
			}

			// A drift view must still report drift, or the plan would show no
			// diff, and refreshing it again must not change it.
			inSync, err := cascInSync(tc.declared, tc.export, "s")
			if err != nil || inSync {
				t.Fatalf("test case is not drifted: inSync=%v err=%v", inSync, err)
			}
			if reflect.DeepEqual(got, mustParseYAML(t, tc.declared)) {
				t.Errorf("view equals the declared document under drift:\n%s", view)
			}
			again := view
			if ok, _ := cascInSync(view, tc.export, "s"); !ok {
				again, _, _ = cascDriftView(view, tc.export, "s")
			}
			if !reflect.DeepEqual(mustParseYAML(t, again), got) {
				t.Errorf("refreshing the view changed it:\n first: %s\nsecond: %s", view, again)
			}
			for _, s := range tc.wantNotContain {
				if strings.Contains(again, s) {
					t.Errorf("refreshed view contains %q:\n%s", s, again)
				}
			}
		})
	}

	t.Run("absent section", func(t *testing.T) {
		_, found, err := cascDriftView("a: 1", "other:\n  a: 1\n", "s")
		if err != nil || found {
			t.Errorf("found=%v err=%v, want found=false", found, err)
		}
	})

	t.Run("invalid declared YAML errors", func(t *testing.T) {
		if _, _, err := cascDriftView("a: [bad\n", "s:\n  a: 1\n", "s"); err == nil {
			t.Error("expected an error for malformed declared YAML")
		}
	})

	t.Run("invalid exported YAML errors", func(t *testing.T) {
		if _, _, err := cascDriftView("a: 1", "s: [bad\n", "s"); err == nil {
			t.Error("expected an error for malformed exported YAML")
		}
	})
}
