package jenkins

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// supportedCredentialScopes are the credential scope strings that Jenkins allows to be defined.
var supportedCredentialScopes = []string{"SYSTEM", "GLOBAL"}

var (
	// pathSegmentRegexp matches a value free of the characters that end or
	// re-split a URL path segment.
	pathSegmentRegexp = regexp.MustCompile(`^[^/?#%;\\]*$`)
	// domainNameRegexp is pathSegmentRegexp without "?" and "#", which
	// escapePathSegment carries through to Jenkins as part of a domain name.
	domainNameRegexp = regexp.MustCompile(`^[^/%;\\]*$`)
	// percentEncodedRegexp matches a percent-encoded octet.
	percentEncodedRegexp = regexp.MustCompile(`%[0-9A-Fa-f]{2}`)
)

// pathSegmentValidators reject a credential ID that cannot be used verbatim as
// one segment of a Jenkins URL path: one containing / ? # % ; or \, or the
// dot-segments "." and "..". Credential IDs are interpolated into
// credentials-store URLs, where such a value could address a different store
// (issue #250). An empty value is left to the caller.
func pathSegmentValidators() []validator.String {
	return []validator.String{
		stringvalidator.RegexMatches(
			pathSegmentRegexp,
			`must not include path characters (/ ? # % ; \); use the 'folder' property to select a folder`,
		),
		stringvalidator.NoneOf(".", ".."),
	}
}

// domainNameValidators are pathSegmentValidators for a credentials domain name,
// which may contain "?" and "#" (e.g. "C# builds"): escapePathSegment encodes
// them, and domains with such names already exist. "%" stays rejected so a
// domain written pre-encoded for the old unescaped URLs fails at plan rather
// than silently missing at apply.
func domainNameValidators() []validator.String {
	return []validator.String{
		stringvalidator.RegexMatches(
			domainNameRegexp,
			`must not include path characters (/ % ; \); use the 'folder' property to select a folder`,
		),
		stringvalidator.NoneOf(".", ".."),
	}
}

// credentialFolderValidator rejects a credentials folder that would not address
// that folder's store; see credentialsFolder.
type credentialFolderValidator struct{}

var _ validator.String = credentialFolderValidator{}

func (credentialFolderValidator) Description(context.Context) string {
	return `must name a folder, with no "." or ".." segment and no ? # % ; or \ characters`
}

func (v credentialFolderValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (credentialFolderValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := credentialsFolder(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Folder Path", err.Error())
	}
}

// preEncodedValidator warns about a data-source domain or credential ID that
// looks percent-encoded. These values are now escaped by the provider and sent
// literally, so one pre-encoded to get through the old unescaped URLs (e.g.
// "C%23 builds") is encoded twice and not found (issue #250). It is a warning,
// not an error, because "%" is legal in a domain name.
type preEncodedValidator struct{}

var _ validator.String = preEncodedValidator{}

func (preEncodedValidator) Description(context.Context) string {
	return "should be the literal value, not percent-encoded"
}

func (v preEncodedValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (preEncodedValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if v := req.ConfigValue.ValueString(); percentEncodedRegexp.MatchString(v) {
		resp.Diagnostics.AddAttributeWarning(
			req.Path,
			"Value Looks Percent-Encoded",
			fmt.Sprintf("%q contains a percent-encoded sequence. The provider escapes this value itself, "+
				"so it is sent literally and a pre-encoded value will not be found. "+
				"Use the literal value instead, e.g. \"C# builds\" rather than \"C%%23 builds\".", v),
		)
	}
}

// folderNameValidator rejects folder paths that contain backslashes. Jenkins
// uses "/" as its only path separator, so a backslash is always a mistake.
type folderNameValidator struct{}

var _ validator.String = folderNameValidator{}

func (folderNameValidator) Description(context.Context) string {
	return "folder path must not contain backslashes; use '/' as the path separator"
}

func (v folderNameValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (folderNameValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if strings.Contains(req.ConfigValue.ValueString(), `\`) {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid Folder Path",
			"folder path must not contain backslashes; use '/' as the path separator",
		)
	}
}

// jobXMLValidator checks a job/folder template at plan time. Non-well-formed XML
// (which today fails only at apply, as an opaque Jenkins 500) is reported as an
// error with a line number where available. A well-formed document with no root
// element yields a warning, since Jenkins expects a job configuration document
// such as <project> or <flow-definition>. An empty template is left to the
// schema's required-ness handling.
type jobXMLValidator struct{}

var _ validator.String = jobXMLValidator{}

func (jobXMLValidator) Description(context.Context) string {
	return "must be well-formed Jenkins job configuration XML"
}

func (v jobXMLValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (jobXMLValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	s := req.ConfigValue.ValueString()
	if strings.TrimSpace(s) == "" {
		return
	}

	// Strip the XML declaration before parsing: Jenkins uses
	// `<?xml version='1.1'?>`, which Go's encoding/xml rejects outright. Removing
	// it (the declaration carries no configuration) leaves the newline in place,
	// so reported line numbers stay accurate. This matches how templates are
	// normalized elsewhere in the provider.
	dec := xml.NewDecoder(strings.NewReader(reXMLDecl.ReplaceAllString(s, "")))
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
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid job configuration XML", detail)
			return
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
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid job configuration XML",
			"unexpected EOF: the document contains an unclosed element",
		)
		return
	}

	if !rootSeen {
		resp.Diagnostics.AddAttributeWarning(
			req.Path,
			"Job configuration has no root XML element",
			"The template is well-formed but contains no XML element; Jenkins expects a job configuration document such as <project> or <flow-definition>.",
		)
	}
}
