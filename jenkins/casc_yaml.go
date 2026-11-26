package jenkins

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
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

// cascDriftMarker stands in, in a drift view, for a controller value that is
// not copied into state because it could hold a resolved secret or keys the
// resource does not manage.
const cascDriftMarker = "<drifted: controller value hidden>"

// reCASCSection matches a valid top-level JCasC section key: a bare identifier
// of letters, digits, underscores, or hyphens (e.g. "jenkins", "unclassified").
var reCASCSection = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// cascWoKeyPattern is the form of a secrets_wo key: the name a ${name}
// expression in yaml refers to. It excludes ':' so a key cannot collide with a
// JCasC secret source prefix such as decodeBase64: or readFile:.
const cascWoKeyPattern = `[A-Za-z_][A-Za-z0-9_.-]*`

// reCASCSecretName matches a secrets_wo key.
var reCASCSecretName = regexp.MustCompile(`^` + cascWoKeyPattern + `$`)

// errCASCSecretValue marks a secrets_wo value that JCasC syntax cannot carry
// where yaml places it.
var errCASCSecretValue = errors.New("secrets_wo value cannot be sent as written")

// isSecretPlaceholder reports whether v is a scalar that is one whole JCasC
// expression, such as ${SECRET}, ${SECRET:-default} or
// ${decodeBase64:${SECRET}}. A managed key whose declared value is such an
// expression is compared structurally only: JCasC resolves it at apply time,
// and the resolved secret must never influence drift or leak into state.
func isSecretPlaceholder(v interface{}) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	s = strings.TrimSpace(s)
	exprs := cascExprs(s)
	return len(exprs) == 1 && exprs[0] == [2]int{0, len(s)} && len(s) > len("${}")
}

// cascExprs returns the [start, end) offsets of the top-level ${...}
// expressions JCasC resolves in s, found the way its commons-text
// StringSubstitutor finds them: "^${" is an escaped literal "${", and an
// expression ends at the '}' that balances every "${" opened inside it,
// escaped or not. JCasC resolves nothing from an expression that is never
// closed onwards, so neither does the scan.
func cascExprs(s string) [][2]int {
	var out [][2]int
	for i := 0; i+1 < len(s); i++ {
		if s[i] != '$' || s[i+1] != '{' {
			continue
		}
		if i > 0 && s[i-1] == '^' {
			i++
			continue
		}
		end := cascExprEnd(s, i)
		if end < 0 {
			break
		}
		out = append(out, [2]int{i, end})
		i = end - 1
	}
	return out
}

// cascExprEnd returns the offset just past the '}' closing the expression
// opened at start, or -1 when it is never closed.
func cascExprEnd(s string, start int) int {
	depth := 0
	for j := start + 2; j < len(s); j++ {
		switch {
		case s[j] == '$' && j+1 < len(s) && s[j+1] == '{':
			depth++
			j++
		case s[j] == '}':
			if depth == 0 {
				return j + 1
			}
			depth--
		}
	}
	return -1
}

// cascSecretRef is a ${name} expression in a string of yaml: its offsets, the
// name, and the offsets of the top-level expression it is nested in, which are
// its own when it is not nested.
type cascSecretRef struct {
	start, end int
	name       string
	outer      [2]int
}

// cascSecretRefs returns, in order, the ${name} expressions JCasC would
// resolve in s, at any depth: JCasC resolves the expressions nested in another
// one before the outer one, as in ${decodeBase64:${name}}.
func cascSecretRefs(s string) []cascSecretRef {
	var out []cascSecretRef
	var walk func(base int, s string, outer *[2]int)
	walk = func(base int, s string, outer *[2]int) {
		for _, e := range cascExprs(s) {
			span := [2]int{base + e[0], base + e[1]}
			top := span
			if outer != nil {
				top = *outer
			}
			if body := s[e[0]+2 : e[1]-1]; reCASCSecretName.MatchString(body) {
				out = append(out, cascSecretRef{start: span[0], end: span[1], name: body, outer: top})
				continue
			}
			walk(span[0]+2, s[e[0]+2:e[1]-1], &top)
		}
	}
	walk(0, s, nil)
	return out
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
	actual, found, err := exportedSection(exportedFullYAML, section)
	if err != nil {
		return false, err
	}
	if !found {
		// The section is absent from the controller: in sync only if nothing is
		// declared for it.
		return declared == nil, nil
	}

	return yamlSubset(declared, actual), nil
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
//   - a drifted sequence the controller did not grow takes the controller's
//     length, each element viewed against the declared element at the same
//     index;
//   - anything else, including a drifted sequence the controller grew, is
//     replaced by cascDriftMarker.
//
// Under drift the view therefore always differs from the declared document,
// so the plan shows which keys moved without showing what it cannot show
// safely. found is false when the section is absent from the export.
func cascDriftView(declaredSectionYAML, exportedFullYAML, section string) (view string, found bool, err error) {
	declared, err := parseYAML(declaredSectionYAML)
	if err != nil {
		return "", false, fmt.Errorf("parsing declared YAML: %w", err)
	}
	actual, found, err := exportedSection(exportedFullYAML, section)
	if err != nil || !found {
		return "", found, err
	}
	out, err := yaml.Marshal(driftView(declared, actual))
	if err != nil {
		return "", false, err
	}
	return string(out), true, nil
}

// driftView restricts actual to the keys of declared; see cascDriftView.
func driftView(declared, actual interface{}) interface{} {
	if yamlSubset(declared, actual) {
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
				out[k] = driftView(dv, av)
			}
		}
		return out
	case []interface{}:
		// Elements are matched by index, so once the controller reorders a
		// list a resolved placeholder could land under a plain declared
		// value. A drifted list holding any ${...} is hidden whole. So is a
		// list the controller grew: an element the document does not
		// declare is unmanaged content that may hold ciphertext or a value
		// another JCasC source resolved, and inserted before the declared
		// ones it shifts into a declared index.
		a, ok := actual.([]interface{})
		if !ok || hasInterpolation(d) || len(a) > len(d) {
			return cascDriftMarker
		}
		out := make([]interface{}, len(a))
		for i, av := range a {
			out[i] = driftView(d[i], av)
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

// hasInterpolation reports whether any string scalar in v contains a ${...}
// expression.
func hasInterpolation(v interface{}) bool {
	switch t := v.(type) {
	case string:
		return len(cascExprs(t)) > 0
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
// example wrapSection("jenkins", "systemMessage: hi", nil) yields
// "jenkins:\n  systemMessage: hi\n". Each ${name} expression naming a key of
// secrets is replaced by its value first; see substituteSecrets.
func wrapSection(section, declaredSectionYAML string, secrets map[string]string) (string, error) {
	subtree, err := parseYAML(declaredSectionYAML)
	if err != nil {
		return "", fmt.Errorf("parsing declared YAML: %w", err)
	}
	subtree, err = substituteSecrets(subtree, secrets)
	if err != nil {
		return "", err
	}
	out, err := yaml.Marshal(map[string]interface{}{section: subtree})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// substituteSecrets returns tree with every ${name} expression whose name is a
// key of secrets replaced by that value, in string scalars only. It works on the
// parsed document, so a value holding newlines, quotes or ': ' cannot change
// the YAML structure. Expressions naming other keys, forms such as
// ${name:-default}, and ^${name} (JCasC's escape for a literal) are left for
// JCasC. The error wraps errCASCSecretValue.
func substituteSecrets(tree interface{}, secrets map[string]string) (interface{}, error) {
	if len(secrets) == 0 {
		return tree, nil
	}
	var err error
	out := mapStrings(tree, func(s string) string {
		r, serr := substituteString(s, secrets)
		if serr != nil && err == nil {
			err = serr
		}
		return r
	})
	return out, err
}

// substituteString substitutes secrets into one string scalar; see
// substituteSecrets. JCasC resolves ${...} in the document it receives, so a
// "${" inside a value is sent as "^${", which JCasC turns back into a literal
// "${" when a closing brace follows in the assembled scalar. An unclosed
// "${" is left literal. A '$' and a '{' that only meet across the edge of a
// value would form a "${" too, so that '$' is escaped the same way.
//
// Two cases have no escape, so they are errors rather than a document JCasC
// would read differently: a value ending in '^' right before an expression,
// which JCasC would take as the escape of that expression; and a value nested
// in another expression holding '$', '{', '}', '^' or ':-', since JCasC
// interprets these as expression boundaries, escaping or a default delimiter.
func substituteString(s string, secrets map[string]string) (string, error) {
	var out []byte
	var escapes []int
	last := 0
	prev := ""
	appendText := func(text string, secret bool) error {
		if !secret && strings.HasPrefix(text, "${") && len(out) > 0 && out[len(out)-1] == '^' {
			return fmt.Errorf("%w: the value of %q ends with '^' right before a JCasC expression in %q, and JCasC would read that '^' as an escape and leave the expression unresolved", errCASCSecretValue, prev, s)
		}
		if strings.HasPrefix(text, "{") && len(out) > 0 && out[len(out)-1] == '$' {
			escapes = append(escapes, len(out)-1)
			out = append(out[:len(out)-1], '^', '$')
		}
		for i := 0; i < len(text); i++ {
			if secret && strings.HasPrefix(text[i:], "${") {
				escapes = append(escapes, len(out))
				out = append(out, '^')
			}
			out = append(out, text[i])
		}
		return nil
	}
	for _, ref := range cascSecretRefs(s) {
		v, ok := secrets[ref.name]
		if !ok {
			continue
		}
		if ref.outer != [2]int{ref.start, ref.end} && (strings.ContainsAny(v, "${}^") || strings.Contains(v, ":-")) {
			return "", fmt.Errorf("%w: the value of %q is nested in the JCasC expression %q and holds '$', '{', '}', '^' or ':-', which JCasC would read as part of the expression; transform the value in Terraform instead, e.g. with base64decode()", errCASCSecretValue, ref.name, s[ref.outer[0]:ref.outer[1]])
		}
		if err := appendText(s[last:ref.start], false); err != nil {
			return "", err
		}
		if err := appendText(v, true); err != nil {
			return "", err
		}
		last = ref.end
		prev = ref.name
	}
	if last == 0 {
		return s, nil
	}
	if err := appendText(s[last:], false); err != nil {
		return "", err
	}
	// JCasC removes an escape only if a closing brace follows. Decide after
	// assembling the scalar so a brace from a suffix or adjacent value counts.
	lastClose := strings.LastIndexByte(string(out), '}')
	for i := len(escapes) - 1; i >= 0; i-- {
		at := escapes[i]
		if at > lastClose {
			out = append(out[:at], out[at+1:]...)
		}
	}
	return string(out), nil
}

// mapStrings returns a copy of tree with fn applied to every string scalar.
func mapStrings(tree interface{}, fn func(string) string) interface{} {
	switch t := tree.(type) {
	case string:
		return fn(t)
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, v := range t {
			out[k] = mapStrings(v, fn)
		}
		return out
	case map[interface{}]interface{}:
		out := make(map[interface{}]interface{}, len(t))
		for k, v := range t {
			out[k] = mapStrings(v, fn)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, v := range t {
			out[i] = mapStrings(v, fn)
		}
		return out
	default:
		return tree
	}
}

// unreferencedSecrets returns, sorted, the keys of secrets that no ${name}
// expression in the declared section refers to: a key that is never
// substituted is almost always a typo, and its value would silently not apply.
func unreferencedSecrets[V any](declaredSectionYAML string, secrets map[string]V) ([]string, error) {
	tree, err := parseYAML(declaredSectionYAML)
	if err != nil {
		return nil, fmt.Errorf("parsing declared YAML: %w", err)
	}
	referenced := map[string]bool{}
	mapStrings(tree, func(s string) string {
		for _, ref := range cascSecretRefs(s) {
			referenced[ref.name] = true
		}
		return s
	})
	var out []string
	for k := range secrets {
		if !referenced[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
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
