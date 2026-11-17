package jenkins

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// reElementName matches an element name an ignored_elements segment may hold.
// It is narrower than an XML Name, but covers what XStream writes for a Java
// class or field (dots, and "_-" for an inner class).
var reElementName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9._-]*$`)

// elementPath is an ignored_elements entry: the local names of an element and
// of each of its ancestors, from the root element down. A "*" segment matches
// any name.
type elementPath []string

// parseElementPath parses an ignored_elements entry such as
// "/jenkins.branch.OrganizationFolder/navigators/*/navigatorProjects". The
// path must name an element below the root, and its last segment must be a
// name: ignoring the root, or every child of an element, would ignore
// configuration the template is there to manage.
func parseElementPath(s string) (elementPath, error) {
	if !strings.HasPrefix(s, "/") {
		return nil, errors.New("must start with /")
	}
	segments := strings.Split(s[1:], "/")
	if len(segments) < 2 {
		return nil, errors.New("must name an element below the root element, such as /root/child")
	}
	for _, seg := range segments {
		if seg != "*" && !reElementName.MatchString(seg) {
			return nil, fmt.Errorf("segment %q is neither an element name nor *", seg)
		}
	}
	if segments[len(segments)-1] == "*" {
		return nil, errors.New("must end with an element name, not *")
	}
	return elementPath(segments), nil
}

// matches reports whether stack, the local names of an element and of its
// ancestors from the root down, is an element p names.
func (p elementPath) matches(stack []string) bool {
	if len(stack) != len(p) {
		return false
	}
	for i, seg := range p {
		if seg != "*" && seg != stack[i] {
			return false
		}
	}
	return true
}

// parseElementPaths parses paths, skipping any that do not parse: the
// attribute validator has already reported those.
func parseElementPaths(paths []string) []elementPath {
	var out []elementPath
	for _, s := range paths {
		if p, err := parseElementPath(s); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// matchesAny reports whether stack is an element one of paths names.
func matchesAny(paths []elementPath, stack []string) bool {
	for _, p := range paths {
		if p.matches(stack) {
			return true
		}
	}
	return false
}

// stripIgnoredElements returns config without the elements paths name, each
// removed with the line break and indentation in front of it. Everything else
// is kept byte for byte. config is returned unchanged when it holds none of
// them, or when it is not well-formed XML, so the comparison is never looser
// than without ignored_elements.
func stripIgnoredElements(config string, paths []string) string {
	parsed := parseElementPaths(paths)
	if len(parsed) == 0 {
		return config
	}

	var spans [][2]int
	if _, err := walkIgnoredElements(config, parsed, ignoredElementVisitor{
		match: func(_, _ string, from, to int) { spans = append(spans, [2]int{from, to}) },
	}); err != nil {
		return config
	}

	var out strings.Builder
	last := 0
	for _, s := range spans {
		begin := s[0]
		for begin > last && (config[begin-1] == ' ' || config[begin-1] == '\t') {
			begin--
		}
		if begin > last && config[begin-1] == '\n' {
			begin--
			if begin > last && config[begin-1] == '\r' {
				begin--
			}
		} else {
			begin = s[0]
		}
		out.WriteString(config[last:begin])
		last = s[1]
	}
	out.WriteString(config[last:])
	return out.String()
}

// withLiveIgnoredElements returns template, a jenkins_job template about to be
// posted, with live's copy of each element paths name: Jenkins' copy is what
// the plugin that writes the element expects back, and the template's copy
// is the configured one, which state keeps too (withStateIgnoredElements).
//
// An element is placed by its parent, identified in both documents by the
// names of its ancestors and, for each, its position among same-named
// siblings, and by its own name. Under a parent, the first matching element
// of a name live has copies of is replaced with all of them, and any other of
// that name is dropped; copies of a name template has none of become the
// parent's last children. Matching elements of a name live has none of under
// that parent are kept as written: a navigator added by this update has to
// carry its own initial value. Positions only pair the same parents while
// both documents have as many elements of each name on the parent's path:
// where they do not, as when this update adds or removes a navigator of a
// type the folder already has, every parent of that name and below is kept
// as written, rather than given another one's copies.
//
// It fails rather than post a template that may lack an element its plugin
// cannot load without: if either document is not well-formed, and if a parent
// kept as written for want of a pairing has no element of a name live has
// under a parent of the same path.
func withLiveIgnoredElements(template, live string, paths []string) (string, error) {
	return mergeIgnoredElements(template, live, paths, true)
}

// mergeIgnoredElements is withLiveIgnoredElements. Without strict, a parent
// kept as written may lack an element live has: withStateIgnoredElements
// merges into Jenkins' own document, which is not posted.
func mergeIgnoredElements(template, live string, paths []string, strict bool) (string, error) {
	parsed := parseElementPaths(paths)
	if len(parsed) == 0 {
		return template, nil
	}

	// Jenkins' copies by parent key, then by name, names in document order.
	type parentCopies struct {
		names []string
		text  map[string]string
	}
	copies := map[string]*parentCopies{}
	// The names Jenkins has copies of, by the path of their parent.
	liveNames := map[string][]string{}
	liveCounts, err := walkIgnoredElements(live, parsed, ignoredElementVisitor{
		match: func(parent, name string, from, to int) {
			c := copies[parent]
			if c == nil {
				c = &parentCopies{text: map[string]string{}}
				copies[parent] = c
			}
			if _, ok := c.text[name]; !ok {
				c.names = append(c.names, name)
				if path := keyPath(parent); !slices.Contains(liveNames[path], name) {
					liveNames[path] = append(liveNames[path], name)
				}
			}
			c.text[name] += live[from:to]
		},
	})
	if err != nil {
		return "", fmt.Errorf("parsing the current configuration: %w", err)
	}
	if len(copies) == 0 {
		return template, nil
	}

	type edit struct {
		parent   string
		from, to int
		text     string
	}
	var edits []edit
	placed := map[[2]string]bool{}
	// The elements template has, and each parent and name it has none of
	// while Jenkins has one under a parent of the same path.
	has := map[[2]string]bool{}
	var lacking [][2]string
	counts, err := walkIgnoredElements(template, parsed, ignoredElementVisitor{
		match: func(parent, name string, from, to int) {
			has[[2]string{parent, name}] = true
			c := copies[parent]
			if c == nil {
				return
			}
			text, ok := c.text[name]
			if !ok {
				return
			}
			if placed[[2]string{parent, name}] {
				text = ""
			}
			placed[[2]string{parent, name}] = true
			edits = append(edits, edit{parent, from, to, text})
		},
		end: func(key, tag string, endTag int, selfClosing bool) {
			for _, n := range liveNames[keyPath(key)] {
				if !has[[2]string{key, n}] {
					lacking = append(lacking, [2]string{key, n})
				}
			}
			c := copies[key]
			if c == nil {
				return
			}
			var text string
			for _, n := range c.names {
				if !placed[[2]string{key, n}] {
					text += c.text[n]
				}
			}
			if text == "" {
				return
			}
			if selfClosing {
				// endTag is just past "/>": reopen the element around the copies.
				edits = append(edits, edit{key, endTag - 2, endTag, ">" + text + "</" + tag + ">"})
				return
			}
			edits = append(edits, edit{key, endTag, endTag, text})
		},
	})
	if err != nil {
		return "", fmt.Errorf("parsing the template: %w", err)
	}
	if strict {
		for _, l := range lacking {
			if !sameElementCounts(l[0], counts, liveCounts) {
				return "", fmt.Errorf("the template has no <%[1]s> in a %[2]s element, and Jenkins has one in such an element; "+
					"the template changes how many of these elements there are, so Jenkins' <%[1]s> cannot be matched to it. "+
					"Add <%[1]s> to each of them in the template, empty where it has no initial value", l[1], keyPath(l[0]))
			}
		}
	}

	var out strings.Builder
	last := 0
	for _, e := range edits {
		if !sameElementCounts(e.parent, counts, liveCounts) {
			continue
		}
		out.WriteString(template[last:e.from])
		out.WriteString(e.text)
		last = e.to
	}
	out.WriteString(template[last:])
	return out.String(), nil
}

// withStateIgnoredElements returns config, a job's config.xml that Read is
// about to store, with state's copy of each element paths name, state being
// the template stored before. Jenkins' copy of an ignored element is not
// managed, so state keeps the applied template's: a plan that changes the
// template for another reason then shows no change in those elements, and a
// template taken from state (ignore_changes, generated configuration) holds
// the configured copy. Copies are placed as withLiveIgnoredElements places
// them, with the same limits. config is returned unchanged when either
// document is not well-formed.
func withStateIgnoredElements(config, state string, paths []string) string {
	merged, err := mergeIgnoredElements(config, state, paths, false)
	if err != nil {
		return config
	}
	return merged
}

// ignoredElementVisitor receives walkIgnoredElements' events, in document
// order. Element keys are the names of the element and of its ancestors, each
// with its position among same-named siblings, so the same element has the
// same key in two versions of a document that have as many elements of each
// of those names (sameElementCounts).
type ignoredElementVisitor struct {
	// match is called with the key of the parent of an element paths name,
	// and the element's name and offsets. Elements inside it are not visited.
	match func(parent, name string, from, to int)
	// end is called for every other element, with its key, its name as its
	// start tag writes it (prefix included), and the offset of its end tag, or
	// of the end of its start tag when selfClosing.
	end func(key, tag string, endTag int, selfClosing bool)
}

// walkIgnoredElements decodes doc and reports its elements to v. It returns
// the number of children of each name of every element, keyed by the
// element's key (empty for the document), "/" and the name.
func walkIgnoredElements(doc string, paths []elementPath, v ignoredElementVisitor) (map[string]int, error) {
	type frame struct {
		key, name, tag string
		selfClosing    bool
	}
	src := blankNumericCharRefs(blankLeadingXMLDecl(doc))
	dec := xml.NewDecoder(strings.NewReader(src))
	counts := map[string]int{}
	stack := []frame{{}}
	var names []string
	from, depth := -1, 0
	for {
		start := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			sibling := stack[len(stack)-1].key + "/" + t.Name.Local
			n := counts[sibling]
			counts[sibling] = n + 1
			end := int(dec.InputOffset())
			stack = append(stack, frame{
				key:         sibling + "[" + strconv.Itoa(n) + "]",
				name:        t.Name.Local,
				tag:         startTagName(src[start:end]),
				selfClosing: strings.HasSuffix(src[start:end], "/>"),
			})
			names = append(names, t.Name.Local)
			if from < 0 && matchesAny(paths, names) {
				from, depth = start, len(names)
			}
		case xml.EndElement:
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			names = names[:len(names)-1]
			switch {
			case from >= 0 && len(names) == depth-1:
				if v.match != nil {
					v.match(stack[len(stack)-1].key, f.name, from, int(dec.InputOffset()))
				}
				from = -1
			case from < 0 && v.end != nil:
				endTag := start
				if f.selfClosing {
					endTag = int(dec.InputOffset())
				}
				v.end(f.key, f.tag, endTag, f.selfClosing)
			}
		}
	}
	return counts, nil
}

// startTagName returns the element name in tag, a start tag such as
// <x:p a="1"/>, as written there.
func startTagName(tag string) string {
	tag = tag[1:]
	if i := strings.IndexAny(tag, " \t\r\n/>"); i >= 0 {
		return tag[:i]
	}
	return tag
}

// keyPath returns key without its positions: the names of an element and of
// its ancestors, as an ignored_elements entry writes them.
func keyPath(key string) string {
	var b strings.Builder
	for _, seg := range strings.Split(key, "/")[1:] {
		b.WriteString("/" + seg[:strings.LastIndexByte(seg, '[')])
	}
	return b.String()
}

// sameElementCounts reports whether a and b, walkIgnoredElements counts of
// two documents, agree on how many elements share the name of key's element
// and of each of its ancestors, so that key names the same element in both.
func sameElementCounts(key string, a, b map[string]int) bool {
	parent := ""
	for _, seg := range strings.Split(key, "/")[1:] {
		name := parent + "/" + seg[:strings.LastIndexByte(seg, '[')]
		if a[name] != b[name] {
			return false
		}
		parent += "/" + seg
	}
	return true
}

// ignoredElementPathValidator rejects an ignored_elements entry
// parseElementPath does not accept.
type ignoredElementPathValidator struct{}

var _ validator.String = ignoredElementPathValidator{}

func (ignoredElementPathValidator) Description(context.Context) string {
	return "must be an element path from the root element, such as /root/child, with * matching any name except the last"
}

func (v ignoredElementPathValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v ignoredElementPathValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsUnknown() {
		return
	}
	if req.ConfigValue.IsNull() {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Ignored Element Path", "An ignored_elements entry must not be null.")
		return
	}
	if _, err := parseElementPath(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Ignored Element Path",
			"The path "+req.ConfigValue.ValueString()+" "+err.Error()+": it "+v.Description(ctx)+".")
	}
}
