package jenkins

import "testing"

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
