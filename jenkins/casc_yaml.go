package jenkins

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"

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

// reInterpolation matches a JCasC ${...} expression anywhere in a string.
// JCasC resolves embedded expressions too, so the value exported for such a
// string may hold a resolved secret.
var reInterpolation = regexp.MustCompile(`\$\{[^}]+\}`)

// cascDriftMarker stands in, in a drift view, for a controller value that is
// not copied into state because it could hold a resolved secret or keys the
// resource does not manage.
const cascDriftMarker = "<drifted: controller value hidden>"

// reCASCSection matches a valid top-level JCasC section key: a bare identifier
// of letters, digits, underscores, or hyphens (e.g. "jenkins", "unclassified").
var reCASCSection = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// reCASCPath matches an unordered_paths entry: mapping keys or '*' joined by
// dots, e.g. "authorizationStrategy.roleBased.roles.*.*.permissions". '*' only
// stands alone, since a segment such as "role*" would be matched literally.
var reCASCPath = regexp.MustCompile(`^(\*|[^.\s*]+)(\.(\*|[^.\s*]+))*$`)

// cascPaths holds the unordered_paths patterns, split into segments. A segment
// matches a mapping key of the same name, and '*' matches any mapping key or
// any sequence element.
type cascPaths [][]string

func newCASCPaths(patterns []string) cascPaths {
	out := make(cascPaths, 0, len(patterns))
	for _, p := range patterns {
		out = append(out, strings.Split(p, "."))
	}
	return out
}

// unordered reports whether the sequence at path at is compared as a multiset.
func (p cascPaths) unordered(at []string) bool {
	for _, pattern := range p {
		if len(pattern) != len(at) {
			continue
		}
		match := true
		for i, seg := range pattern {
			if seg != "*" && seg != at[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// child returns at extended by seg without sharing at's backing array, so
// sibling recursions cannot overwrite each other's path. Without patterns no
// path can match, so none is built.
func (p cascPaths) child(at []string, seg string) []string {
	if len(p) == 0 {
		return nil
	}
	out := make([]string, len(at)+1)
	copy(out, at)
	out[len(at)] = seg
	return out
}

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
//     because JCasC list configurators are replace-semantics. A sequence whose
//     path matches unordered must have the same length and pair each declared
//     element with a distinct actual element it is a subset of, in any order.
//   - Scalars: must be equal.
//
// A declared scalar that is a ${...} secret placeholder matches any actual
// value, so a resolved secret never registers as drift. at is the path of
// declared within the section.
func yamlSubset(declared, actual interface{}, at []string, unordered cascPaths) bool {
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
			if !yamlSubset(dv, av, unordered.child(at, k), unordered) {
				return false
			}
		}
		return true
	case []interface{}:
		a, ok := actual.([]interface{})
		if !ok || len(a) != len(d) {
			return false
		}
		if unordered.unordered(at) {
			_, n := matchUnordered(d, a, at, unordered)
			return n == len(d)
		}
		for i := range d {
			if !yamlSubset(d[i], a[i], unordered.child(at, "*"), unordered) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(declared, actual)
	}
}

// matchUnordered pairs the elements of declared with distinct elements of
// actual they are a subset of, as a maximum bipartite matching (augmenting
// paths), so a declared element that fits several actual ones cannot take the
// only partner of another. pairs[i] is the actual index paired with declared
// element i, or -1; n is the number of pairs.
func matchUnordered(declared, actual []interface{}, at []string, unordered cascPaths) (pairs []int, n int) {
	elem := unordered.child(at, "*")
	fits := make([][]bool, len(declared))
	for i, d := range declared {
		fits[i] = make([]bool, len(actual))
		for j, a := range actual {
			fits[i][j] = yamlSubset(d, a, elem, unordered)
		}
	}

	pairs = make([]int, len(declared))
	owner := make([]int, len(actual))
	for i := range pairs {
		pairs[i] = -1
	}
	for j := range owner {
		owner[j] = -1
	}

	var augment func(i int, seen []bool) bool
	augment = func(i int, seen []bool) bool {
		for j := range actual {
			if !fits[i][j] || seen[j] {
				continue
			}
			seen[j] = true
			if owner[j] == -1 || augment(owner[j], seen) {
				owner[j] = i
				pairs[i] = j
				return true
			}
		}
		return false
	}
	for i := range declared {
		if augment(i, make([]bool, len(actual))) {
			n++
		}
	}
	return pairs, n
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
func cascInSync(declaredSectionYAML, exportedFullYAML, section string, unordered cascPaths) (bool, error) {
	declared, err := parseYAML(declaredSectionYAML)
	if err != nil {
		return false, fmt.Errorf("parsing declared YAML: %w", err)
	}
	actual, found, err := exportedSection(exportedFullYAML, section)
	if err != nil {
		return false, err
	}
	if !found {
		// The section is absent from the controller: in sync only if nothing is
		// declared for it.
		return declared == nil, nil
	}

	return yamlSubset(declared, actual, nil, unordered), nil
}

// cascDriftView renders the yaml Read stores when the controller no longer
// satisfies the declared section. Storing the exported section instead would
// put the values JCasC resolved for ${...} placeholders, and every unmanaged
// key, into plan output and state (#249). The view keeps the declared
// document's shape:
//
//   - subtrees that are in sync, including ${...} placeholders, stay exactly as
//     declared;
//   - a declared key missing on the controller is dropped;
//   - a drifted plain scalar takes the controller's scalar value;
//   - a drifted sequence takes the controller's length, each element viewed
//     against the declared element at the same index;
//   - a drifted unordered sequence keeps the declared elements that still
//     pair with a controller element, then the unpaired controller elements;
//   - anything else is replaced by cascDriftMarker.
//
// Under drift the view therefore always differs from the declared document,
// so the plan shows which keys moved without showing what it cannot show
// safely. found is false when the section is absent from the export.
func cascDriftView(declaredSectionYAML, exportedFullYAML, section string, unordered cascPaths) (view string, found bool, err error) {
	declared, err := parseYAML(declaredSectionYAML)
	if err != nil {
		return "", false, fmt.Errorf("parsing declared YAML: %w", err)
	}
	actual, found, err := exportedSection(exportedFullYAML, section)
	if err != nil || !found {
		return "", found, err
	}
	out, err := yaml.Marshal(driftView(declared, actual, nil, unordered))
	if err != nil {
		return "", false, err
	}
	return string(out), true, nil
}

// driftView restricts actual to the keys of declared; see cascDriftView.
func driftView(declared, actual interface{}, at []string, unordered cascPaths) interface{} {
	if yamlSubset(declared, actual, at, unordered) {
		return declared
	}

	switch d := declared.(type) {
	case map[string]interface{}:
		a, ok := actual.(map[string]interface{})
		if !ok {
			return cascDriftMarker
		}
		out := make(map[string]interface{}, len(d))
		for k, dv := range d {
			if av, ok := a[k]; ok {
				out[k] = driftView(dv, av, unordered.child(at, k), unordered)
			}
		}
		return out
	case []interface{}:
		// Elements are matched by index, so once the controller reorders a
		// list a resolved placeholder could land under a plain declared
		// value. A drifted list holding any ${...} is hidden whole.
		a, ok := actual.([]interface{})
		if !ok || hasInterpolation(d) {
			return cascDriftMarker
		}
		if unordered.unordered(at) {
			return unorderedDriftView(d, a, at, unordered)
		}
		out := make([]interface{}, len(a))
		for i, av := range a {
			switch {
			case i < len(d):
				out[i] = driftView(d[i], av, unordered.child(at, "*"), unordered)
			case isYAMLScalar(av):
				out[i] = av
			default:
				// An extra element has no declared keys to restrict it to.
				out[i] = cascDriftMarker
			}
		}
		return out
	default:
		// A refresh after a drifted one receives the stored view as declared,
		// so a value hidden once must stay hidden.
		if declared == cascDriftMarker || hasInterpolation(declared) || !isYAMLScalar(actual) {
			return cascDriftMarker
		}
		return actual
	}
}

// unorderedDriftView renders a drifted unordered sequence: the declared
// elements that pair with a controller element, in declared order, then the
// unpaired controller scalars, in export order, then one cascDriftMarker per
// other unpaired controller element, which has no declared counterpart to
// restrict it to. The markers come last so that a refresh, which pairs the
// copied scalars with themselves, renders the view unchanged. The caller has
// already hidden a sequence holding ${...}.
func unorderedDriftView(declared, actual []interface{}, at []string, unordered cascPaths) []interface{} {
	pairs, _ := matchUnordered(declared, actual, at, unordered)
	paired := make([]bool, len(actual))
	out := make([]interface{}, 0, len(actual))
	for i, j := range pairs {
		if j >= 0 {
			paired[j] = true
			out = append(out, declared[i])
		}
	}
	hidden := 0
	for j, av := range actual {
		switch {
		case paired[j]:
		case isYAMLScalar(av):
			out = append(out, av)
		default:
			hidden++
		}
	}
	for ; hidden > 0; hidden-- {
		out = append(out, cascDriftMarker)
	}
	return out
}

// hasInterpolation reports whether any string scalar in v contains a ${...}
// expression.
func hasInterpolation(v interface{}) bool {
	switch t := v.(type) {
	case string:
		return reInterpolation.MatchString(t)
	case map[string]interface{}:
		for _, e := range t {
			if hasInterpolation(e) {
				return true
			}
		}
	case map[interface{}]interface{}:
		for _, e := range t {
			if hasInterpolation(e) {
				return true
			}
		}
	case []interface{}:
		for _, e := range t {
			if hasInterpolation(e) {
				return true
			}
		}
	}
	return false
}

// isYAMLScalar reports whether a parsed YAML value is neither a mapping nor a
// sequence.
func isYAMLScalar(v interface{}) bool {
	switch v.(type) {
	case map[string]interface{}, map[interface{}]interface{}, []interface{}:
		return false
	}
	return true
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
	sub, found, err := exportedSection(exportedFullYAML, section)
	if err != nil || !found {
		return "", found, err
	}
	out, err := yaml.Marshal(sub)
	if err != nil {
		return "", false, err
	}
	return string(out), true, nil
}

// exportedSection parses a full exported JCasC document and returns the given
// top-level section's subtree. found is false when the section is absent.
func exportedSection(exportedFullYAML, section string) (subtree interface{}, found bool, err error) {
	exported, err := parseYAML(exportedFullYAML)
	if err != nil {
		return nil, false, fmt.Errorf("parsing exported YAML: %w", err)
	}
	m, ok := exported.(map[string]interface{})
	if !ok {
		return nil, false, fmt.Errorf("exported configuration is not a YAML mapping")
	}
	subtree, found = m[section]
	return subtree, found, nil
}
