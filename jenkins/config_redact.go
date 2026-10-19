package jenkins

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// A Jenkins config.xml read back for state can carry encrypted secrets. The
// worst case is a folder: its config.xml embeds the whole folder credentials
// store (FolderCredentialsProperty), every Secret in it encrypted with the
// controller's master key. That ciphertext is not safe to copy into Terraform
// state: anyone who can configure and build a job on the controller can have
// Jenkins decrypt it (e.g. by pasting it into a password parameter default),
// and state is read far more widely than the controller's credential store.
//
// The helpers below remove that material from config.xml before it is stored.
// They work on the raw text, not a parse/re-render, so everything else in the
// document is kept byte for byte and the result is a pure function of the
// server's config.xml: refreshing an unchanged folder always yields the same
// value, and so does a secret encrypted again with a fresh IV (Jenkins picks a
// new IV each time a secret is set from plain text, e.g. a credential update
// or JCasC reload, even to the same value).

// redactedSecretMarker replaces each encrypted secret value. It is not itself
// recognized as ciphertext, so redaction is idempotent. Jenkins cannot decrypt
// it either: posted back as a Secret it would be stored as that literal plain
// text, which is why redactedSecretValidator rejects it in a jenkins_job
// template.
const redactedSecretMarker = "{redacted}"

// folderCredentialsPropertyElement is the folder property holding the folder's
// credentials store (credentials plugin, FolderCredentialsProvider).
const folderCredentialsPropertyElement = "com.cloudbees.hudson.plugins.folder.properties.FolderCredentialsProvider_-FolderCredentialsProperty" //nolint:gosec // G101 false positive: an XML element name, not a credential

var (
	// reFolderCredentialsProperty matches a whole FolderCredentialsProperty
	// element, self-closing or not, with any (quoted) attributes, together with
	// the line break and indentation in front of it so no blank line is left
	// behind. Attributes are matched as quoted name=value pairs rather than
	// [^>]*, so a ">" inside an attribute value (XML allows it unescaped) is
	// not taken for the end of the start tag, which would cut a self-closing
	// element short or run on to a later element's closing tag. The element
	// cannot contain itself, so the lazy body ends at its own closing tag.
	reFolderCredentialsProperty = regexp.MustCompile(
		`(?s)(?:\r?\n[ \t]*)?<` + regexp.QuoteMeta(folderCredentialsPropertyElement) +
			`(?:\s+[^\s=/>]+\s*=\s*(?:"[^"]*"|'[^']*'))*\s*` +
			`(?:/>|>.*?</` + regexp.QuoteMeta(folderCredentialsPropertyElement) + `\s*>)`)

	// reFolderCredentialsPropertyTag reports whether a document contains a
	// FolderCredentialsProperty start tag at all.
	reFolderCredentialsPropertyTag = regexp.MustCompile(`<` + regexp.QuoteMeta(folderCredentialsPropertyElement) + `[\s/>]`)

	// reBracedBase64 matches the shape Jenkins serializes encrypted values in:
	// standard base64 wrapped in braces (hudson.util.Secret, and the
	// credentials plugin's SecretBytes). isSecretCiphertext decides which
	// matches really are ciphertext.
	reBracedBase64 = regexp.MustCompile(`\{[A-Za-z0-9+/]+={0,2}\}`)

	// reRedactedSecretValue matches an element whose whole text is
	// redactedSecretMarker, which is how a redacted Secret field reads.
	reRedactedSecretValue = regexp.MustCompile(`>\s*` + regexp.QuoteMeta(redactedSecretMarker) + `\s*<`)

	// reLeadingXMLDecl matches the XML declaration opening a document.
	reLeadingXMLDecl = regexp.MustCompile(`\A\s*<\?xml[^?]*\?>`)
)

// redactJenkinsConfig returns config with every FolderCredentialsProperty
// element removed and every encrypted secret value replaced with
// redactedSecretMarker. It is for computed, read-only template attributes
// (jenkins_folder, and the jenkins_folder and jenkins_job data sources); the
// jenkins_job resource compares its template with the configuration and uses
// stripFolderCredentials instead.
func redactJenkinsConfig(config string) string {
	return redactSecretCiphertext(stripFolderCredentials(config))
}

// stripFolderCredentials removes every FolderCredentialsProperty element from
// config.
func stripFolderCredentials(config string) string {
	return reFolderCredentialsProperty.ReplaceAllLiteralString(config, "")
}

// hasFolderCredentials reports whether config contains a
// FolderCredentialsProperty element.
func hasFolderCredentials(config string) bool {
	return reFolderCredentialsPropertyTag.MatchString(config)
}

// withFolderCredentials returns template, a jenkins_job template about to be
// posted, with every folder credentials store in live, the item's current
// config.xml, copied verbatim into it as the first children of the root's
// <properties> element (which is added if missing). The encrypted credentials
// thus go back unchanged to the Jenkins that encrypted them, as they do on a
// jenkins_folder update. template is returned unchanged when live has no store.
func withFolderCredentials(template, live string) (string, error) {
	stores := reFolderCredentialsProperty.FindAllString(live, -1)
	if len(stores) == 0 {
		return template, nil
	}
	store := strings.Join(stores, "")

	// encoding/xml rejects Jenkins' version='1.1' declaration. Blank it out
	// rather than remove it, so offsets into src are offsets into template.
	src := reLeadingXMLDecl.ReplaceAllStringFunc(template, func(m string) string {
		return strings.Repeat(" ", len(m))
	})
	dec := xml.NewDecoder(strings.NewReader(src))
	// The store replaces template[from:to] with insert, at the first root's
	// first <properties> child if there is one. The whole template is parsed
	// even after that place is found, so a malformed one is an error.
	depth, from, to, insert, inProperties := 0, -1, -1, "", false
	for {
		start := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parsing the template: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			end := int(dec.InputOffset())
			// A start tag ends in "/>" only when it is self-closing: an
			// attribute value always ends with its closing quote.
			selfClosing := strings.HasSuffix(src[start:end], "/>")
			switch {
			case depth == 1 && from < 0 && selfClosing:
				// An empty root, such as <com.cloudbees.hudson.plugins.folder.Folder/>.
				name := strings.Fields(src[start+1 : end-2])[0]
				from, to, insert = end-2, end, "><properties>"+store+"</properties></"+name+">"
			case depth == 1 && from < 0:
				// Without a <properties> element, add one first in the root.
				from, to, insert = end, end, "<properties>"+store+"</properties>"
			case depth == 2 && !inProperties && t.Name.Local == "properties":
				inProperties = true
				from, to, insert = end, end, store
				if selfClosing {
					from, insert = end-2, ">"+store+"</properties>"
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	if from < 0 {
		return "", errors.New("the template has no root element")
	}
	return template[:from] + insert + template[to:], nil
}

// redactedSecretValidator rejects a jenkins_job template in which an element's
// whole value is redactedSecretMarker, as the encrypted values in a template
// copied from the jenkins_job or jenkins_folder data source are. Jenkins
// cannot decrypt the marker, so it would silently store "{redacted}" itself as
// the secret (Secret.fromString falls back to plain text).
type redactedSecretValidator struct{}

var _ validator.String = redactedSecretValidator{}

func (redactedSecretValidator) Description(context.Context) string {
	return "must not contain values redacted by a data source"
}

func (v redactedSecretValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (redactedSecretValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if reRedactedSecretValue.MatchString(req.ConfigValue.ValueString()) {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Template contains a redacted secret",
			"An element of the template has the value "+redactedSecretMarker+", which the jenkins_job and jenkins_folder data sources put in place of an encrypted secret. "+
				"Jenkins cannot decrypt it and would store "+redactedSecretMarker+" itself as the secret. "+
				"A data source's template is not a copy of the item's configuration: supply the secret values explicitly instead.",
		)
	}
}

// redactSecretCiphertext replaces every encrypted secret value in config with
// redactedSecretMarker. See isSecretCiphertext for what counts as one.
func redactSecretCiphertext(config string) string {
	locs := reBracedBase64.FindAllStringIndex(config, -1)
	if len(locs) == 0 {
		return config
	}

	var b strings.Builder
	last := 0
	for _, loc := range locs {
		start, end := loc[0], loc[1]
		if !isSecretCiphertext(config, start, end) {
			continue
		}
		b.WriteString(config[last:start])
		b.WriteString(redactedSecretMarker)
		last = end
	}
	if last == 0 {
		return config
	}
	b.WriteString(config[last:])
	return b.String()
}

// isSecretCiphertext reports whether the braced base64 token config[start:end]
// is a Jenkins-encrypted value.
//
//   - hudson.util.Secret writes "{" + base64(payload) + "}", where the payload
//     starts with the version byte 0x01 and the 4-byte big-endian IV length
//     (16): 01 00 00 00 10, which base64-encodes to "AQAAABA". Any token
//     starting with "{AQAA" is therefore treated as ciphertext wherever it
//     appears.
//   - Other braced base64 values, such as the credentials plugin's SecretBytes
//     (secret files, certificate keystores), have no fixed prefix. They are
//     treated as ciphertext only where Jenkins itself looks for an encrypted
//     value when it redacts config.xml for EXTENDED_READ users: as the entire
//     text content of an element. They must also decode as standard base64 to
//     at least one AES block (16 bytes). That keeps a placeholder such as
//     "${PATH}" inside a script, or "{ ... }" JSON, untouched.
//
// The pre-2017 unbraced legacy format cannot be told apart from ordinary
// base64 text without the controller's key, so it is not matched; Jenkins
// rewrites such values in the braced format the next time it saves the item.
func isSecretCiphertext(config string, start, end int) bool {
	body := config[start+1 : end-1]
	if strings.HasPrefix(body, "AQAA") {
		return true
	}
	if start == 0 || config[start-1] != '>' || end >= len(config) || config[end] != '<' {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(body)
	return err == nil && len(raw) >= 16
}
