package jenkins

import (
	"encoding/base64"
	"regexp"
	"strings"
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
// value, and a re-encryption (Jenkins picks a fresh IV whenever it saves a
// Secret) no longer changes it either.

// redactedSecretMarker replaces each encrypted secret value. It is not itself
// recognized as ciphertext, so redaction is idempotent.
const redactedSecretMarker = "{redacted}"

// folderCredentialsPropertyElement is the folder property holding the folder's
// credentials store (credentials plugin, FolderCredentialsProvider).
const folderCredentialsPropertyElement = "com.cloudbees.hudson.plugins.folder.properties.FolderCredentialsProvider_-FolderCredentialsProperty"

var (
	// reFolderCredentialsProperty matches a whole FolderCredentialsProperty
	// element, self-closing or not, with any (quoted) attributes, together with
	// the line break and indentation in front of it so no blank line is left
	// behind. Attributes are matched as quoted name=value pairs rather than
	// [^>]*, so a "/" at the end of an attribute value cannot be mistaken for a
	// self-closing tag. The element cannot contain itself, so the lazy body
	// ends at its own closing tag.
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
