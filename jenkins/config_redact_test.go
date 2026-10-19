package jenkins

import (
	"testing"
)

func TestRedactJenkinsConfig(t *testing.T) {
	const prop = folderCredentialsPropertyElement

	// A hudson.util.Secret payload is 25+16n bytes, so its base64 ends in "=",
	// "==" or no padding depending on the secret's length; SecretBytes values
	// can have any length. redactCipherToken and redactCipherPassword cover
	// "=" and none.
	const (
		cipherDoublePad      = "{AQAAABAAAAAwAwoRGB8mLTQ7QklQV15lbAUQGyYxPEdSXWhzfomUn6q1wMvW4ez3Ag0YIy45RE9aZXB7hpGcp7K9yNPe6fT/Cg==}"
		secretBytesDoublePad = "{AQ4bKDVCT1xpdoOQnaq3xA==}"
		secretBytesNoPad     = "{CRwvQlVoe46htMfa7QATJjlM}"
	)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "folder credentials store with nested credentials",
			in:   redactFolderWithCredentials(redactCipherToken),
			want: redactFolderNoCredentials,
		},
		{
			// A ">" inside a quoted attribute value must not end the start tag:
			// read as the end of a non-empty start tag, the match would run on
			// to the next property's closing tag and take <keep/> with it.
			name: "property with double- and single-quoted attributes",
			in:   `<properties><` + prop + ` plugin="credentials@1514.v6e8e6c2a_8a_2b_" note="a/>b"/><keep/><` + prop + ` other='c>d'><secret>` + redactCipherToken + `</secret></` + prop + `><x/></properties>`,
			want: `<properties><keep/><x/></properties>`,
		},
		{
			name: "self-closing property",
			in:   "<properties>\n  <" + prop + ` plugin="credentials@1514"/>` + "\n  <x/>\n</properties>",
			want: "<properties>\n  <x/>\n</properties>",
		},
		{
			name: "every property is removed",
			in:   "<a>\n  <" + prop + "><s>" + redactCipherToken + "</s></" + prop + ">\n  <b/>\n  <" + prop + ">\n    <s>" + redactCipherPassword + "</s>\n  </" + prop + " >\n</a>",
			want: "<a>\n  <b/>\n</a>",
		},
		{
			name: "element whose name only starts with the property name is kept",
			in:   `<a><` + prop + `Extra><v>1</v></` + prop + `Extra></a>`,
			want: `<a><` + prop + `Extra><v>1</v></` + prop + `Extra></a>`,
		},
		{
			name: "multiple ciphertexts outside the property",
			in:   `<p><password>` + redactCipherToken + `</password><token>` + redactCipherPassword + `</token><key>` + redactSecretBytes + `</key></p>`,
			want: `<p><password>{redacted}</password><token>{redacted}</token><key>{redacted}</key></p>`,
		},
		{
			name: "ciphertext with every base64 padding",
			in:   `<p><a>` + cipherDoublePad + `</a><b>` + redactCipherPassword + `</b><c>` + secretBytesDoublePad + `</c><d>` + secretBytesNoPad + `</d><e>x ` + cipherDoublePad + `</e></p>`,
			want: `<p><a>{redacted}</a><b>{redacted}</b><c>{redacted}</c><d>{redacted}</d><e>x {redacted}</e></p>`,
		},
		{
			// A braced token that is not ciphertext must not end the scan: in a
			// job's config.xml a ${VAR} in the description comes before the
			// encrypted password parameter default.
			name: "ciphertext after a placeholder",
			in:   `<description>uses ${TOKEN} and {abcd}</description><defaultValue>` + redactCipherToken + `</defaultValue><key>` + redactSecretBytes + `</key>`,
			want: `<description>uses ${TOKEN} and {abcd}</description><defaultValue>{redacted}</defaultValue><key>{redacted}</key>`,
		},
		{
			name: "AQAA ciphertext in an attribute value",
			in:   `<p token="` + redactCipherToken + `"/>`,
			want: `<p token="{redacted}"/>`,
		},
		{
			// The element-text check looks one byte either side of the token,
			// which must not run off either end of the document.
			name: "prefix-less braced base64 opening the document",
			in:   redactSecretBytes + `<a/>`,
			want: redactSecretBytes + `<a/>`,
		},
		{
			name: "prefix-less braced base64 closing the document",
			in:   `<a>` + redactSecretBytes,
			want: `<a>` + redactSecretBytes,
		},
		{
			name: "AQAA ciphertext embedded in text",
			in:   `<script>def t = '` + redactCipherToken + `'</script>`,
			want: `<script>def t = '{redacted}'</script>`,
		},
		{
			name: "placeholders are not ciphertext",
			in:   `<command>echo ${PATH} ${ABCDEFGHIJKLMNOPQRSTUVWX}</command><v>${ABCDEFGHIJKLMNOPQRSTUVWX}</v>`,
			want: `<command>echo ${PATH} ${ABCDEFGHIJKLMNOPQRSTUVWX}</command><v>${ABCDEFGHIJKLMNOPQRSTUVWX}</v>`,
		},
		{
			name: "JSON is not ciphertext",
			in:   `<json>{"a":{"b":[1,2]},"c":"{abcd}"}</json><e>{}</e>`,
			want: `<json>{"a":{"b":[1,2]},"c":"{abcd}"}</json><e>{}</e>`,
		},
		{
			name: "short or malformed braced base64 element text is not ciphertext",
			in:   `<a>{PATH}</a><b>{abcd1234}</b><c>{ABCDEFGHIJKLMNOPQRSTUVW}</c>`,
			want: `<a>{PATH}</a><b>{abcd1234}</b><c>{ABCDEFGHIJKLMNOPQRSTUVW}</c>`,
		},
		{
			name: "prefix-less braced base64 inside text is not ciphertext",
			in:   `<description>see ` + redactSecretBytes + ` here</description>`,
			want: `<description>see ` + redactSecretBytes + ` here</description>`,
		},
		{
			name: "no secrets",
			in:   redactFolderNoCredentials,
			want: redactFolderNoCredentials,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactJenkinsConfig(tt.in)
			if got != tt.want {
				t.Errorf("redactJenkinsConfig() =\n%s\nwant\n%s", got, tt.want)
			}
			if again := redactJenkinsConfig(got); again != got {
				t.Errorf("redactJenkinsConfig is not idempotent:\n%s\nthen\n%s", got, again)
			}
		})
	}
}

// TestRedactJenkinsConfigDeterministic pins that encrypting the same secret
// again, which Jenkins does with a fresh IV each time a secret is set from
// plain text, does not change the redacted value.
func TestRedactJenkinsConfigDeterministic(t *testing.T) {
	tests := []struct {
		name string
		a, b string
	}{
		{
			name: "inside the folder credentials store",
			a:    redactFolderWithCredentials(redactCipherToken),
			b:    redactFolderWithCredentials(redactCipherReencrypted),
		},
		{
			name: "outside it",
			a:    `<p><password>` + redactCipherToken + `</password></p>`,
			b:    `<p><password>` + redactCipherReencrypted + `</password></p>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if a, b := redactJenkinsConfig(tt.a), redactJenkinsConfig(tt.b); a != b {
				t.Errorf("redacted values differ:\n%s\n---\n%s", a, b)
			}
		})
	}
}

// TestStripFolderCredentials pins the narrower jenkins_job resource redaction:
// only the folder credentials store goes, ciphertext elsewhere is left for the
// template comparison.
func TestStripFolderCredentials(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "folder credentials store",
			in:   redactFolderWithCredentials(redactCipherToken),
			want: redactFolderNoCredentials,
		},
		{
			name: "ciphertext outside the store is kept",
			in:   redactJobXML,
			want: redactJobXML,
		},
		{
			// A self-closing property whose attribute value contains "/>": a
			// start tag cut short there would leave `y"/>` behind.
			name: `attribute value containing "/>"`,
			in:   `<properties><` + folderCredentialsPropertyElement + ` plugin="x/>y"/><x/></properties>`,
			want: `<properties><x/></properties>`,
		},
		{
			// Any XML whitespace may separate the attributes and end either
			// tag, not just the single space Jenkins writes.
			name: "tags broken over lines and tabs",
			in:   "<properties>\r\n  <" + folderCredentialsPropertyElement + "\n\tplugin=\"credentials@1514\"\n\tnote = 'a'\n><secret>" + redactCipherToken + "</secret></" + folderCredentialsPropertyElement + "\n>\r\n  <x/>\r\n</properties>",
			want: "<properties>\r\n  <x/>\r\n</properties>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripFolderCredentials(tt.in); got != tt.want {
				t.Errorf("stripFolderCredentials() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestHasFolderCredentials(t *testing.T) {
	const prop = folderCredentialsPropertyElement

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "element with attributes", in: redactFolderWithCredentials(redactCipherToken), want: true},
		{name: "plain open tag", in: `<a><` + prop + `></` + prop + `></a>`, want: true},
		{name: "self-closing", in: `<a><` + prop + `/></a>`, want: true},
		// Any XML whitespace may follow the element name, not just the single
		// space Jenkins writes. Missing such a store would make Update treat a
		// template that manages it as one that does not, and add the live
		// store next to it.
		{name: "line break after the name", in: "<a><" + prop + "\n  plugin=\"credentials@1514\"></" + prop + "></a>", want: true},
		{name: "CRLF after the name", in: "<a><" + prop + "\r\n></" + prop + "></a>", want: true},
		{name: "tab after the name", in: "<a><" + prop + "\tplugin=\"credentials@1514\"/></a>", want: true},
		{name: "absent", in: redactFolderNoCredentials, want: false},
		{name: "longer element name", in: `<a><` + prop + `Extra/></a>`, want: false},
		{name: "name in text", in: `<description>` + prop + `</description>`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasFolderCredentials(tt.in); got != tt.want {
				t.Errorf("hasFolderCredentials() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJobTemplateFromServer(t *testing.T) {
	server := redactFolderWithCredentials(redactCipherToken)

	tests := []struct {
		name string
		keep bool
		want string
	}{
		{name: "configured template has no credentials store", keep: false, want: redactFolderNoCredentials},
		{name: "configured template has a credentials store", keep: true, want: server},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jobTemplateFromServer(server, tt.keep); got != tt.want {
				t.Errorf("jobTemplateFromServer() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestFolderCredentialsFlag(t *testing.T) {
	tests := []struct {
		name     string
		template string
		want     string
	}{
		{name: "with store", template: redactFolderWithCredentials(redactCipherToken), want: "true"},
		{name: "without store", template: redactFolderNoCredentials, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(folderCredentialsFlag(tt.template)); got != tt.want {
				t.Errorf("folderCredentialsFlag() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWithFolderCredentials(t *testing.T) {
	const (
		prop   = folderCredentialsPropertyElement
		folder = "com.cloudbees.hudson.plugins.folder.Folder"
		decl   = "<?xml version='1.1' encoding='UTF-8'?>\n"
	)
	store := `<` + prop + ` plugin="credentials@1514"><secret>` + redactCipherToken + `</secret></` + prop + `>`
	live := decl + `<` + folder + `><properties>` + store + `</properties></` + folder + `>`

	tests := []struct {
		name     string
		template string
		live     string
		want     string
		wantErr  bool
	}{
		{
			name:     "folder config.xml without a store",
			template: redactFolderNoCredentials,
			live:     redactFolderWithCredentials(redactCipherToken),
			want:     redactFolderWithCredentials(redactCipherToken),
		},
		{
			name:     "nothing to keep",
			template: redactFolderNoCredentials,
			live:     redactFolderNoCredentials,
			want:     redactFolderNoCredentials,
		},
		{
			name:     "nothing to keep, template not parsed",
			template: "<project><unclosed></project>",
			live:     redactJobXML,
			want:     "<project><unclosed></project>",
		},
		{
			name:     "empty properties element",
			template: decl + `<` + folder + `><properties/><x/></` + folder + `>`,
			live:     live,
			want:     decl + `<` + folder + `><properties>` + store + `</properties><x/></` + folder + `>`,
		},
		{
			// Only the root's own <properties> holds folder properties; a view's
			// nested one must not be used.
			name:     "no top-level properties element",
			template: decl + `<` + folder + `><views><v><properties class="hudson.model.View$PropertyList"/></v></views></` + folder + `>`,
			live:     live,
			want:     decl + `<` + folder + `><properties>` + store + `</properties><views><v><properties class="hudson.model.View$PropertyList"/></v></views></` + folder + `>`,
		},
		{
			name:     "top-level properties after a nested one",
			template: `<` + folder + `><views><v><properties/></v></views><properties><x/></properties></` + folder + `>`,
			live:     live,
			want:     `<` + folder + `><views><v><properties/></v></views><properties>` + store + `<x/></properties></` + folder + `>`,
		},
		{
			name:     "empty root element",
			template: decl + `<` + folder + ` plugin="cloudbees-folder@6.1106"/>`,
			live:     live,
			want:     decl + `<` + folder + ` plugin="cloudbees-folder@6.1106"><properties>` + store + `</properties></` + folder + `>`,
		},
		{
			name:     "malformed template",
			template: `<` + folder + `><properties></` + folder + `>`,
			live:     live,
			wantErr:  true,
		},
		{
			name:     "no root element",
			template: "  ",
			live:     live,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := withFolderCredentials(tt.template, tt.live)
			if (err != nil) != tt.wantErr {
				t.Fatalf("withFolderCredentials() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("withFolderCredentials() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
