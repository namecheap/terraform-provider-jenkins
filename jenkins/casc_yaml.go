package jenkins

import (
	"fmt"
	"reflect"
	"regexp"

	"gopkg.in/yaml.v3"
)

// This file holds the drift-detection core for the forthcoming
// jenkins_configuration_as_code resource (see docs/design/casc.md). It is pure,
// dependency-free logic — no network, no provider surface — so it can be built
// and unit-tested ahead of the resource itself.
//
// The resource manages one top-level JCasC section per instance. Drift is
// detected by comparing the declared section subtree against the full
// configuration returned by GET /configuration-as-code/export as a semantic
// deep subset: only keys the practitioner declares are compared, so the many
// default and unmanaged keys in the export are ignored.

// reSecretPlaceholder matches a JCasC secret interpolation expression such as
// ${SECRET} or ${SECRET:-default}. A managed key whose declared value is such
// an expression is compared structurally only: JCasC resolves it at apply time,
// and the resolved secret must never influence drift or leak into state.
var reSecretPlaceholder = regexp.MustCompile(`^\s*\$\{[^}]+\}\s*$`)

// reCASCSection matches a valid top-level JCasC section key: a bare identifier
// of letters, digits, underscores, or hyphens (e.g. "jenkins", "unclassified").
var reCASCSection = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// isSecretPlaceholder reports whether v is a scalar JCasC ${...} secret
// expression.
func isSecretPlaceholder(v interface{}) bool {
	s, ok := v.(string)
	return ok && reSecretPlaceholder.MatchString(s)
}

// parseYAML decodes a YAML document into a generic structure
// (map[string]interface{}, []interface{}, scalars) for semantic comparison.
func parseYAML(s string) (interface{}, error) {
	var out interface{}
	if err := yaml.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// yamlSubset reports whether declared is a deep subset of actual:
//
//   - Mappings: every key declared must exist in actual with a subset-equal
//     value; keys present only in actual (defaults, unmanaged settings) are
//     ignored.
//   - Sequences: must match element-for-element and length (order-significant),
//     because JCasC list configurators are replace-semantics.
//   - Scalars: must be equal.
//
// A declared scalar that is a ${...} secret placeholder matches any actual
// value, so a resolved secret never registers as drift.
func yamlSubset(declared, actual interface{}) bool {
	if isSecretPlaceholder(declared) {
		return true
	}

	switch d := declared.(type) {
	case map[string]interface{}:
		a, ok := actual.(map[string]interface{})
		if !ok {
			return false
		}
		for k, dv := range d {
			av, ok := a[k]
			if !ok {
				return false
			}
			if !yamlSubset(dv, av) {
				return false
			}
		}
		return true
	case []interface{}:
		a, ok := actual.([]interface{})
		if !ok || len(a) != len(d) {
			return false
		}
		for i := range d {
			if !yamlSubset(d[i], a[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(declared, actual)
	}
}

// cascInSync reports whether the declared single-section YAML is a semantic
// subset of the full exported JCasC configuration. section is the top-level key
// the declared document manages (e.g. "jenkins"); declaredSectionYAML is the
// bare subtree for that section, and exportedFullYAML is the whole document
// returned by /configuration-as-code/export.
//
// It returns true when the controller already satisfies everything declared
// (no drift), false when a managed key differs or is missing, and an error only
// when either document cannot be parsed.
func cascInSync(declaredSectionYAML, exportedFullYAML, section string) (bool, error) {
	declared, err := parseYAML(declaredSectionYAML)
	if err != nil {
		return false, fmt.Errorf("parsing declared YAML: %w", err)
	}
	exported, err := parseYAML(exportedFullYAML)
	if err != nil {
		return false, fmt.Errorf("parsing exported YAML: %w", err)
	}

	exportedMap, ok := exported.(map[string]interface{})
	if !ok {
		return false, fmt.Errorf("exported configuration is not a YAML mapping")
	}

	actual, ok := exportedMap[section]
	if !ok {
		// The section is absent from the controller: in sync only if nothing is
		// declared for it.
		return declared == nil, nil
	}

	return yamlSubset(declared, actual), nil
}

// wrapSection wraps a declared section subtree under its top-level section key,
// producing the single-section YAML document POSTed to JCasC configure. For
// example wrapSection("jenkins", "systemMessage: hi") yields
// "jenkins:\n  systemMessage: hi\n".
func wrapSection(section, declaredSectionYAML string) (string, error) {
	subtree, err := parseYAML(declaredSectionYAML)
	if err != nil {
		return "", fmt.Errorf("parsing declared YAML: %w", err)
	}
	out, err := yaml.Marshal(map[string]interface{}{section: subtree})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// extractSectionYAML returns the given top-level section's subtree from a full
// exported JCasC document, marshaled back to YAML. found is false when the
// section is absent from the export.
func extractSectionYAML(exportedFullYAML, section string) (yamlDoc string, found bool, err error) {
	exported, err := parseYAML(exportedFullYAML)
	if err != nil {
		return "", false, fmt.Errorf("parsing exported YAML: %w", err)
	}
	m, ok := exported.(map[string]interface{})
	if !ok {
		return "", false, fmt.Errorf("exported configuration is not a YAML mapping")
	}
	sub, ok := m[section]
	if !ok {
		return "", false, nil
	}
	out, err := yaml.Marshal(sub)
	if err != nil {
		return "", false, err
	}
	return string(out), true, nil
}
