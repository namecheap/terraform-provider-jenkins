package jenkins

import (
	"encoding/xml"
	"io"
	"strings"
)

// jobRuntimeElement names an element a plugin writes into a job's config.xml
// on its own while Jenkins runs, rather than from the job's configuration:
// name is the element, parent the element it is a direct child of.
type jobRuntimeElement struct {
	parent, name string
}

// jobRuntimeElements are left out of the jenkins_job template comparison and
// of the template Read stores. Each is state a plugin saves with the job, so
// the controller's copy changes without any configuration change and a
// template cannot keep up with it: compared, it would plan a change after
// every save. Add an element here only when the plugin writes it outside of a
// configuration change; anything a user sets belongs in the comparison.
var jobRuntimeElements = []jobRuntimeElement{
	// gitlab-branch-source adds the path of each project an organization
	// folder scan finds, and saves the folder. Webhook events are matched
	// against this set; the plugin has no setter for it.
	{parent: "io.jenkins.plugins.gitlabbranchsource.GitLabSCMNavigator", name: "navigatorProjects"},
}

// stripJobRuntimeElements returns config without its jobRuntimeElements, each
// removed with the line break and indentation in front of it. Everything else
// is kept byte for byte. config is returned unchanged when it holds none of
// them, or when it is not well-formed XML, so the comparison is never looser
// than without this.
func stripJobRuntimeElements(config string) string {
	if !mayHoldJobRuntimeElement(config) {
		return config
	}

	// encoding/xml rejects Jenkins' version='1.1' declaration. Blank it out
	// rather than remove it, so offsets into src are offsets into config.
	src := reXMLDecl.ReplaceAllStringFunc(config, func(m string) string {
		return strings.Repeat(" ", len(m))
	})
	dec := xml.NewDecoder(strings.NewReader(src))
	var parents []string
	var spans [][2]int
	from, depth := -1, 0
	for {
		start := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return config
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if from < 0 && len(parents) > 0 && isJobRuntimeElement(parents[len(parents)-1], t.Name.Local) {
				from, depth = start, len(parents)
			}
			parents = append(parents, t.Name.Local)
		case xml.EndElement:
			parents = parents[:len(parents)-1]
			if from >= 0 && len(parents) == depth {
				spans = append(spans, [2]int{from, int(dec.InputOffset())})
				from = -1
			}
		}
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

// mayHoldJobRuntimeElement reports whether config names any
// jobRuntimeElements element, so a job that holds none is not parsed.
func mayHoldJobRuntimeElement(config string) bool {
	for _, e := range jobRuntimeElements {
		if strings.Contains(config, "<"+e.name) {
			return true
		}
	}
	return false
}

func isJobRuntimeElement(parent, name string) bool {
	for _, e := range jobRuntimeElements {
		if e.parent == parent && e.name == name {
			return true
		}
	}
	return false
}
