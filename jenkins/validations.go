package jenkins

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/hashicorp/go-cty/cty"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
)

func validateJobName(val interface{}, path cty.Path) diag.Diagnostics {
	if strings.Contains(val.(string), "/") {
		return diag.FromErr(fmt.Errorf("provided name includes path characters. Please use the 'folder' property if specifying a job within a subfolder"))
	}

	return diag.Diagnostics{}
}

func validateFolderName(val interface{}, path cty.Path) diag.Diagnostics {
	if strings.Contains(val.(string), `\`) {
		return diag.Errorf("folder path must not contain backslashes; use '/' as the path separator")
	}
	return diag.Diagnostics{}
}

// validateJobXML checks a job/folder template at plan time. Non-well-formed XML
// (which today fails only at apply, as an opaque Jenkins 500) is reported as an
// error with a line number where available. A well-formed document with no root
// element yields a warning, since Jenkins expects a job configuration document
// such as <project> or <flow-definition>. An empty template is left to the
// schema's required-ness handling.
func validateJobXML(val interface{}, path cty.Path) diag.Diagnostics {
	var diags diag.Diagnostics

	s, ok := val.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return diags
	}

	dec := xml.NewDecoder(strings.NewReader(s))
	depth, rootSeen := 0, false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			detail := err.Error()
			if se, ok := err.(*xml.SyntaxError); ok {
				detail = fmt.Sprintf("line %d: %s", se.Line, se.Msg)
			}
			return append(diags, diag.Diagnostic{
				Severity:      diag.Error,
				Summary:       "Invalid job configuration XML",
				Detail:        detail,
				AttributePath: path,
			})
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
			rootSeen = true
		case xml.EndElement:
			depth--
		}
	}

	if depth != 0 {
		return append(diags, diag.Diagnostic{
			Severity:      diag.Error,
			Summary:       "Invalid job configuration XML",
			Detail:        "unexpected EOF: the document contains an unclosed element",
			AttributePath: path,
		})
	}

	if !rootSeen {
		diags = append(diags, diag.Diagnostic{
			Severity:      diag.Warning,
			Summary:       "Job configuration has no root XML element",
			Detail:        "The template is well-formed but contains no XML element; Jenkins expects a job configuration document such as <project> or <flow-definition>.",
			AttributePath: path,
		})
	}

	return diags
}

// supportedCredentialScopes are the credential scope strings that Jenkins allows to be defined.
var supportedCredentialScopes = []string{"SYSTEM", "GLOBAL"}
