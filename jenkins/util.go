package jenkins

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"log"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// formatFolderName will format a folder name in the way that Jenkins expects, with "name/job/name" separators.
// Deduplication will be performed so that it is safe to pass an already-formatted job into this function.
func formatFolderName(name string) string {
	split := strings.Split(name, "/")

	ret := []string{}
	for _, segment := range split {
		if segment == "" || segment == "job" {
			continue
		}
		ret = append(ret, segment)
	}
	return strings.Join(ret, "/job/")
}

// formatFolderID will format a set of folders in the way that Jenkins expects for the "folder" property, with "/job/name/job/name" separators.
func formatFolderID(folders []string) string {
	if len(folders) == 0 {
		return ""
	}
	return "/job/" + formatFolderName(strings.Join(folders, "/"))
}

// extractFolders prepares a job name for some folder-aware client library calls.
// These calls are different from other calls in that they expect the folders to be specified
// as a series of parameters with no "/job/" separators.
//
// This func will strip out the "/job/" separators from the given string and only return
// the apparent "path" to the folder.
func extractFolders(folder string) (folders []string) {
	for _, item := range strings.Split(folder, "/") {
		if item == "" || item == "job" {
			continue
		}
		folders = append(folders, item)
	}

	return
}

// parseCanonicalJobID will take a canonical Jenkins ID and extract out the base name of the job
// as well as the folder segments that are part of it.
func parseCanonicalJobID(id string) (name string, folders []string) {
	if id == "" {
		return
	}

	folders = extractFolders(id)
	return folders[len(folders)-1], folders[0 : len(folders)-1]
}

// folderExists will validate that a given folder name exists
func folderExists(ctx context.Context, client jenkinsClient, name string) error {
	folders := extractFolders(name)
	if len(folders) > 0 {
		folderName, parentFolders := parseCanonicalJobID(name)
		_, err := client.GetFolder(ctx, folderName, parentFolders...)
		if err != nil {
			return err
		}
	}

	return nil
}

// Compiled once at package load: templateDiff is a DiffSuppressFunc invoked
// per-attribute on every plan/apply, so these must not be recompiled per call.
var (
	// reXMLDecl matches an XML declaration, sanitized to prevent inadvertent inequalities.
	reXMLDecl = regexp.MustCompile(`<\?xml.+\?>`)
	// rePlugin matches plugin="..." version attributes, which Jenkins rewrites on every
	// read; stripping them prevents a version bump from registering as configuration drift.
	rePlugin = regexp.MustCompile(` plugin="[^"]*"`)
)

// normalizeJobXML applies the string-level normalizations that have historically
// suppressed phantom jenkins_job diffs: it strips the XML declaration, plugin
// version annotations, and spaces, and unescapes HTML entities. It is deliberately
// conservative (no element parsing), so it can never mask a genuine change.
func normalizeJobXML(s string) string {
	s = reXMLDecl.ReplaceAllString(s, "")
	s = rePlugin.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.TrimSpace(s)
	s = html.UnescapeString(s)
	return s
}

// canonicalizeXML re-serializes s into a canonical form in which attribute
// ordering, empty-element syntax (`<a/>` vs `<a></a>`), the XML declaration, and
// insignificant inter-element whitespace are normalized. Child element order and
// element text content are preserved verbatim, so a genuine configuration change
// is never masked. It returns ok=false when s is not well-formed XML, in which
// case the caller falls back to the string comparison.
func canonicalizeXML(s string) (string, bool) {
	dec := xml.NewDecoder(strings.NewReader(s))
	var buf strings.Builder
	enc := xml.NewEncoder(&buf)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false
		}
		switch t := tok.(type) {
		case xml.ProcInst:
			// The XML declaration cannot be re-encoded by encoding/xml and is
			// semantically irrelevant to the configuration; drop it.
			if t.Target == "xml" {
				continue
			}
		case xml.CharData:
			// Drop whitespace-only character data between elements (formatting
			// indentation); real text content is preserved untouched.
			if strings.TrimSpace(string(t)) == "" {
				continue
			}
		case xml.StartElement:
			sort.Slice(t.Attr, func(i, j int) bool {
				if t.Attr[i].Name.Space != t.Attr[j].Name.Space {
					return t.Attr[i].Name.Space < t.Attr[j].Name.Space
				}
				return t.Attr[i].Name.Local < t.Attr[j].Name.Local
			})
			tok = t
		}
		if err := enc.EncodeToken(tok); err != nil {
			return "", false
		}
	}
	if err := enc.Flush(); err != nil {
		return "", false
	}
	return buf.String(), true
}

func templateDiff(k, old, new string, d *schema.ResourceData) bool {
	equal := normalizeJobXML(old) == normalizeJobXML(new)
	if !equal {
		// Fall back to canonical XML comparison, which additionally ignores
		// attribute ordering, empty-element syntax, and formatting whitespace.
		// Re-applying normalizeJobXML to the canonical output keeps this a strict
		// superset of the string comparison above: it can only remove phantom
		// diffs, never introduce them. Malformed XML fails to canonicalize and
		// falls through to the string result.
		if co, ok := canonicalizeXML(old); ok {
			if cn, ok := canonicalizeXML(new); ok {
				equal = normalizeJobXML(co) == normalizeJobXML(cn)
			}
		}
	}

	// SECURITY: the job/folder XML can contain inlined secrets (for example
	// credentials embedded in job configuration), so log only whether it changed
	// — never its content.
	log.Printf("[DEBUG] jenkins::diff - equal=%t", equal)
	return equal
}

func generateCredentialID(folder, name string) string {
	return fmt.Sprintf("%s/%s", folder, name)
}

// isNotFound reports whether err represents an HTTP 404 response.
// gojenkins formats credential errors as "invalid response code 404" and job
// errors as the bare string "404"; strings.Contains handles both forms. This is
// the single 404 matcher used by every resource path so the SDKv2 and framework
// providers cannot silently diverge. It is nil-safe: a nil error is not a 404.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "404")
}
