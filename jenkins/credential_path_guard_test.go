package jenkins

import (
	"context"
	"net/http"
	"path"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// More issue #250 regression tests, alongside credential_path_test.go: every
// credential resource and data source the provider registers, import IDs
// (which skip plan validation), and encoded or look-alike forms of the
// rejected values.

const (
	// climbingCredentialID is a credential ID that, unescaped, climbs from
	// .../domain/_/credential/ in a folder store to the system store's
	// credential "victim".
	climbingCredentialID = "../../../../../../../../credentials/store/system/domain/_/credential/victim"
	escapedClimbingID    = "..%2F..%2F..%2F..%2F..%2F..%2F..%2F..%2Fcredentials%2Fstore%2Fsystem%2Fdomain%2F_%2Fcredential%2Fvictim"

	// climbingCredentialDomain climbs from team-a's store to the system
	// store's global domain.
	climbingCredentialDomain = "../../../../../../credentials/store/system/domain/_"
	escapedClimbingDomain    = "..%2F..%2F..%2F..%2F..%2F..%2Fcredentials%2Fstore%2Fsystem%2Fdomain%2F_"
)

// pathTestCredentialResources returns the constructor of every credential
// resource the provider registers, keyed by type name, so a credential type
// added later is covered here without editing this file. The domain resource
// is not a credential and is tested on its own.
func pathTestCredentialResources(t *testing.T) map[string]func() resource.Resource {
	t.Helper()
	ctx := context.Background()
	out := map[string]func() resource.Resource{}
	for _, newResource := range New().Resources(ctx) {
		var meta resource.MetadataResponse
		newResource().Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "jenkins"}, &meta)
		if strings.HasPrefix(meta.TypeName, "jenkins_credential_") && meta.TypeName != "jenkins_credential_domain" {
			out[meta.TypeName] = newResource
		}
	}
	if len(out) < 9 {
		t.Fatalf("found %d credential resources, want at least 9", len(out))
	}
	return out
}

// pathTestCredentialDataSources is pathTestCredentialResources for data
// sources, including the jenkins_credentials list.
func pathTestCredentialDataSources(t *testing.T) map[string]func() datasource.DataSource {
	t.Helper()
	ctx := context.Background()
	out := map[string]func() datasource.DataSource{}
	for _, newDataSource := range New().DataSources(ctx) {
		var meta datasource.MetadataResponse
		newDataSource().Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "jenkins"}, &meta)
		if strings.HasPrefix(meta.TypeName, "jenkins_credential_") || meta.TypeName == "jenkins_credentials" {
			out[meta.TypeName] = newDataSource
		}
	}
	if len(out) < 9 {
		t.Fatalf("found %d credential data sources, want at least 9", len(out))
	}
	return out
}

// pathTestObject returns a value of the schema type typ with the given
// string attributes set and every other attribute null, so one model works for
// every credential type.
func pathTestObject(ctx context.Context, t *testing.T, typ attr.Type, vals map[string]string) types.Object {
	t.Helper()
	objType, ok := typ.(types.ObjectType)
	if !ok {
		t.Fatalf("schema type is %T, want an object", typ)
	}
	attrs := make(map[string]attr.Value, len(objType.AttrTypes))
	for name, attrType := range objType.AttrTypes {
		if v, ok := vals[name]; ok {
			attrs[name] = types.StringValue(v)
			continue
		}
		null, err := attrType.ValueFromTerraform(ctx, tftypes.NewValue(attrType.TerraformType(ctx), nil))
		if err != nil {
			t.Fatalf("null %s: %v", name, err)
		}
		attrs[name] = null
	}
	for name := range vals {
		if _, ok := objType.AttrTypes[name]; !ok {
			t.Fatalf("schema has no attribute %q", name)
		}
	}
	obj, diags := types.ObjectValue(objType.AttrTypes, attrs)
	if diags.HasError() {
		t.Fatalf("ObjectValue: %v", diags)
	}
	return obj
}

// pathTestOpRequest is the request a credential resource op sends for the
// escaped domain d and ID n in team-a's store.
func pathTestOpRequest(op, d, n string) recordedRequest {
	base := teamAStore + "domain/" + d + "/"
	switch op {
	case "create":
		return recordedRequest{method: http.MethodPost, path: base + "createCredentials"}
	case "read":
		return recordedRequest{method: http.MethodGet, path: base + "credential/" + n + "/config.xml/"}
	case "update":
		return recordedRequest{method: http.MethodPost, path: base + "credential/" + n + "/config.xml"}
	default:
		return recordedRequest{method: http.MethodPost, path: base + "credential/" + n + "/doDelete"}
	}
}

// pathTestResource returns a new r configured with client, and its schema.
func pathTestResource(ctx context.Context, t *testing.T, newResource func() resource.Resource, client *jenkinsAdapter) (resource.Resource, resource.SchemaResponse) {
	t.Helper()
	r := newResource()
	var cr resource.ConfigureResponse
	r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: client}, &cr)
	if cr.Diagnostics.HasError() {
		t.Fatalf("Configure: %v", cr.Diagnostics)
	}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	return r, sr
}

// TestCredentialPathEveryResource runs the CRUD ops of every registered
// credential resource, not only the two in TestCredentialResourcePathEscaping,
// and adds a credential ID that climbs out of the store: unescaped, Read,
// Update and Delete of it would reach the system store's "victim".
func TestCredentialPathEveryResource(t *testing.T) {
	ctx := context.Background()
	folderLookup := recordedRequest{method: http.MethodGet, path: "/job/team-a/api/json"}

	cases := []struct {
		name, folder, domain, id string
		// escDomain and escID are the expected path segments; empty means the
		// op must fail before sending any request.
		escDomain, escID string
		errSummary       string
	}{
		{name: "crafted domain", folder: "team-a", domain: craftedCredentialDomain, id: "x", escDomain: escapedCraftedDomain, escID: "x"},
		{name: "climbing id", folder: "team-a", domain: "_", id: climbingCredentialID, escDomain: "_", escID: escapedClimbingID},
		{name: "hash and question mark domain", folder: "team-a", domain: "C# builds?", id: "x", escDomain: "C%23%20builds%3F", escID: "x"},
		{name: "dot domain", folder: "team-a", domain: ".", id: "x"},
		{name: "dot-dot id", folder: "team-a", domain: "_", id: ".."},
		{name: "folder job", folder: "job", domain: "_", id: "x", errSummary: "Invalid Folder"},
		{name: "folder dot-dot", folder: "team-a/..", domain: "_", id: "x", errSummary: "Invalid Folder"},
	}

	for typeName, newResource := range pathTestCredentialResources(t) {
		for _, op := range []string{"create", "read", "update", "delete"} {
			for _, tc := range cases {
				t.Run(typeName+"/"+op+"/"+tc.name, func(t *testing.T) {
					client, requests := credentialPathRecorder(t, func(r *http.Request) (int, string) {
						if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/config.xml/") {
							return http.StatusNotFound, ""
						}
						return http.StatusOK, `{}`
					})
					r, sr := pathTestResource(ctx, t, newResource, client)
					model := pathTestObject(ctx, t, sr.Schema.Type(), map[string]string{
						"folder": tc.folder, "domain": tc.domain, "name": tc.id,
						"scope": "GLOBAL", "description": "d",
					})

					diags := runCredentialOp(ctx, t, r, op, model)
					got := requests()

					if tc.escDomain == "" {
						if !diags.HasError() {
							t.Errorf("want an error, got none")
						} else if tc.errSummary != "" && diags.Errors()[0].Summary() != tc.errSummary {
							t.Errorf("error summary = %q, want %q", diags.Errors()[0].Summary(), tc.errSummary)
						}
						assertRequests(t, got)
						return
					}
					if diags.HasError() {
						t.Fatalf("%s: %v", op, diags)
					}
					want := pathTestOpRequest(op, tc.escDomain, tc.escID)
					if op == "create" {
						assertRequests(t, got, folderLookup, want)
					} else {
						assertRequests(t, got, want)
					}
				})
			}
		}
	}
}

// TestCredentialImportPathGuards imports each credential resource and reads
// it, as `terraform import` does. Import IDs skip plan validation, so the
// runtime checks are all that keep the read inside the folder's store: before
// the fix "job/_/victim" read the system store's "victim" into a resource whose
// destroy would then delete it.
func TestCredentialImportPathGuards(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, id string
		// escDomain and escID are the expected path segments in team-a's store;
		// empty means the read must fail before sending any request.
		escDomain, escID string
		errSummary       string
	}{
		{name: "benign", id: "team-a/_/victim", escDomain: "_", escID: "victim"},
		{name: "question mark domain", id: "team-a/x?/victim", escDomain: "x%3F", escID: "victim"},
		{name: "hash domain", id: "team-a/C# builds/victim", escDomain: "C%23%20builds", escID: "victim"},
		{name: "encoded dot-dot domain", id: "team-a/%2e%2e/victim", escDomain: "%252e%252e", escID: "victim"},
		{name: "dot-dot path parameter domain", id: "team-a/..;/victim", escDomain: "..%3B", escID: "victim"},
		{name: "question mark id", id: "team-a/_/victim?", escDomain: "_", escID: "victim%3F"},
		{name: "encoded slash id", id: "team-a/_/%2F..%2F", escDomain: "_", escID: "%252F..%252F"},
		{name: "dot-dot domain", id: "team-a/../victim"},
		{name: "dot domain", id: "team-a/./victim"},
		{name: "dot-dot id", id: "team-a/_/.."},
		{name: "dot id", id: "team-a/_/."},
		{name: "folder job", id: "job/_/victim", errSummary: "Invalid Folder"},
		{name: "folder slash job", id: "/job//_/victim", errSummary: "Invalid Folder"},
		{name: "folder dot-dot", id: "../_/victim", errSummary: "Invalid Folder"},
		{name: "nested folder dot-dot", id: "team-a/../_/victim", errSummary: "Invalid Folder"},
		{name: "folder question mark", id: "team-a?/_/victim", errSummary: "Invalid Folder"},
		{name: "folder hash", id: "team-a#/_/victim", errSummary: "Invalid Folder"},
	}

	for typeName, newResource := range pathTestCredentialResources(t) {
		for _, tc := range cases {
			t.Run(typeName+"/"+tc.name, func(t *testing.T) {
				// The read gets a 404 so it ends cleanly by dropping the resource.
				client, requests := credentialPathRecorder(t, func(*http.Request) (int, string) {
					return http.StatusNotFound, ""
				})
				r, sr := pathTestResource(ctx, t, newResource, client)
				s := sr.Schema

				imported := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
				r.(resource.ResourceWithImportState).ImportState(ctx, resource.ImportStateRequest{ID: tc.id}, &imported)
				if imported.Diagnostics.HasError() {
					t.Fatalf("ImportState(%q): %v", tc.id, imported.Diagnostics)
				}
				read := resource.ReadResponse{State: imported.State}
				r.Read(ctx, resource.ReadRequest{State: imported.State}, &read)
				got := requests()

				if tc.escDomain == "" {
					if !read.Diagnostics.HasError() {
						t.Errorf("import %q: want an error, got none", tc.id)
					} else if tc.errSummary != "" && read.Diagnostics.Errors()[0].Summary() != tc.errSummary {
						t.Errorf("error summary = %q, want %q", read.Diagnostics.Errors()[0].Summary(), tc.errSummary)
					}
					assertRequests(t, got)
					return
				}
				if read.Diagnostics.HasError() {
					t.Fatalf("import %q: %v", tc.id, read.Diagnostics)
				}
				assertRequests(t, got, pathTestOpRequest("read", tc.escDomain, tc.escID))
			})
		}
	}
}

// TestCredentialDomainImportPathGuards is TestCredentialImportPathGuards for
// jenkins_credential_domain, whose import ID is "[<folder>/]<name>".
func TestCredentialDomainImportPathGuards(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, id string
		// escaped is the expected domain segment in team-a's store; empty means
		// the read must fail before sending any request.
		escaped string
	}{
		{name: "benign", id: "team-a/team", escaped: "team"},
		{name: "hash and question mark", id: "team-a/C# builds?", escaped: "C%23%20builds%3F"},
		{name: "encoded dot-dot", id: "team-a/%2e%2e", escaped: "%252e%252e"},
		{name: "dot-dot path parameter", id: "team-a/..;x", escaped: "..%3Bx"},
		{name: "dot-dot", id: "team-a/.."},
		{name: "dot", id: "team-a/."},
		{name: "system store dot-dot", id: ".."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, requests := credentialPathRecorder(t, func(*http.Request) (int, string) {
				return http.StatusNotFound, ""
			})
			r, sr := pathTestResource(ctx, t, newCredentialDomainResource, client)
			s := sr.Schema

			imported := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
			r.(resource.ResourceWithImportState).ImportState(ctx, resource.ImportStateRequest{ID: tc.id}, &imported)
			if imported.Diagnostics.HasError() {
				t.Fatalf("ImportState(%q): %v", tc.id, imported.Diagnostics)
			}
			read := resource.ReadResponse{State: imported.State}
			r.Read(ctx, resource.ReadRequest{State: imported.State}, &read)
			got := requests()

			if tc.escaped == "" {
				if !read.Diagnostics.HasError() {
					t.Errorf("import %q: want an error, got none", tc.id)
				}
				assertRequests(t, got)
				return
			}
			if read.Diagnostics.HasError() {
				t.Fatalf("import %q: %v", tc.id, read.Diagnostics)
			}
			assertRequests(t, got, recordedRequest{method: http.MethodGet, path: teamAStore + "domain/" + tc.escaped + "/config.xml"})
		})
	}
}

// TestCredentialDataSourcePathEveryKind extends
// TestCredentialDataSourcePathEscaping to every registered credential data
// source, a climbing credential ID, and the folder values that the plan-time
// validator rejects (a data source config can also reach Read with a value
// that was unknown at plan).
func TestCredentialDataSourcePathEveryKind(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, folder, domain, id string
		// escDomain and escID are the expected path segments; empty means the
		// read must fail before sending any request.
		escDomain, escID string
		// idCase marks a case about the credential ID, which the list data
		// source does not have.
		idCase bool
	}{
		{name: "climbing domain", folder: "team-a", domain: climbingCredentialDomain, id: "x", escDomain: escapedClimbingDomain, escID: "x"},
		{name: "crafted domain", folder: "team-a", domain: craftedCredentialDomain, id: "x", escDomain: escapedCraftedDomain, escID: "x"},
		{name: "climbing id", folder: "team-a", domain: "_", id: climbingCredentialID, escDomain: "_", escID: escapedClimbingID, idCase: true},
		{name: "question mark id", folder: "team-a", domain: "_", id: "x?", escDomain: "_", escID: "x%3F", idCase: true},
		{name: "dot domain", folder: "team-a", domain: ".", id: "x"},
		{name: "dot-dot domain", folder: "team-a", domain: "..", id: "x"},
		{name: "dot id", folder: "team-a", domain: "_", id: ".", idCase: true},
		{name: "dot-dot id", folder: "team-a", domain: "_", id: "..", idCase: true},
		{name: "folder job", folder: "job", domain: "_", id: "x"},
		{name: "folder slash", folder: "/", domain: "_", id: "x"},
		{name: "folder job slashes", folder: "/job/", domain: "_", id: "x"},
		{name: "folder dot-dot", folder: "team-a/..", domain: "_", id: "x"},
		{name: "folder leading dot", folder: "./team-a", domain: "_", id: "x"},
		{name: "folder question mark", folder: "team-a?x", domain: "_", id: "x"},
		{name: "folder hash", folder: "team-a#x", domain: "_", id: "x"},
		{name: "folder encoded slash", folder: "team-a%2F..", domain: "_", id: "x"},
		{name: "folder semicolon", folder: "team-a;x", domain: "_", id: "x"},
		{name: "folder backslash", folder: `team-a\..`, domain: "_", id: "x"},
	}

	for typeName, newDataSource := range pathTestCredentialDataSources(t) {
		list := typeName == "jenkins_credentials"
		for _, tc := range cases {
			if list && tc.idCase {
				continue
			}
			t.Run(typeName+"/"+tc.name, func(t *testing.T) {
				client, requests := credentialPathRecorder(t, nil)
				d := newDataSource()
				var sr datasource.SchemaResponse
				d.Schema(ctx, datasource.SchemaRequest{}, &sr)
				vals := map[string]string{"folder": tc.folder, "domain": tc.domain}
				if !list {
					vals["name"] = tc.id
				}
				diags := readCredentialDataSource(ctx, t, d, client, pathTestObject(ctx, t, sr.Schema.Type(), vals))
				got := requests()

				if tc.escDomain == "" {
					if !diags.HasError() {
						t.Errorf("want an error, got none")
					}
					assertRequests(t, got)
					return
				}
				want := recordedRequest{method: http.MethodGet, path: teamAStore + "domain/" + tc.escDomain + "/credential/" + tc.escID + "/config.xml/"}
				if list {
					want = recordedRequest{method: http.MethodGet, path: teamAStore + "domain/" + tc.escDomain + "/api/json/", query: listQuery}
				}
				assertRequests(t, got, want)
			})
		}
	}
}

// TestCredentialFolderEdgeCases checks credentialsFolder, and every credential
// folder validator built on it, against folder values that would leave the
// folder's store: dot-segments in any position, encoded or path-parameter
// forms of them, and values that format to the system store.
func TestCredentialFolderEdgeCases(t *testing.T) {
	resources, dataSources := credentialSchemas(t)
	for _, tc := range []struct {
		name, folder string
		// want is the formatted folder; ok is false when the folder is rejected.
		want string
		ok   bool
	}{
		{name: "unset", folder: "", want: "", ok: true},
		{name: "canonical nested", folder: "/job/team-a/job/sub", want: "team-a/job/sub", ok: true},
		{name: "empty segments", folder: "team-a//sub/", want: "team-a/job/sub", ok: true},
		{name: "double slash", folder: "//"},
		{name: "job job slash", folder: "/job/job/"},
		{name: "dot-dot", folder: ".."},
		{name: "dot", folder: "."},
		{name: "leading dot-dot", folder: "../team-a"},
		{name: "job dot-dot", folder: "job/.."},
		{name: "inner dot", folder: "team-a/./sub"},
		{name: "sibling", folder: "team-a/../team-b"},
		{name: "encoded dot-dot", folder: "team-a/%2e%2e"},
		{name: "encoded upper dot-dot", folder: "team-a/%2E%2E"},
		{name: "encoded slash", folder: "team-a%2F.."},
		{name: "dot-dot path parameter", folder: "team-a/..;"},
		{name: "dot-dot backslash", folder: `team-a\..`},
		{name: "trailing question mark", folder: "team-a?"},
		{name: "trailing hash", folder: "team-a#"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := credentialsFolder(tc.folder)
			if tc.ok {
				if err != nil || got != tc.want {
					t.Errorf("credentialsFolder(%q) = %q, %v; want %q", tc.folder, got, err, tc.want)
				}
			} else if err == nil {
				t.Errorf("credentialsFolder(%q) = %q, want an error", tc.folder, got)
			}

			validators := map[string][]validator.String{}
			for typeName, s := range resources {
				a, ok := s.Attributes["folder"].(rschema.StringAttribute)
				if !ok {
					t.Fatalf("%s.folder is not a string attribute", typeName)
				}
				validators[typeName] = a.Validators
			}
			for typeName, s := range dataSources {
				a, ok := s.Attributes["folder"].(dschema.StringAttribute)
				if !ok {
					t.Fatalf("data %s.folder is not a string attribute", typeName)
				}
				validators["data "+typeName] = a.Validators
			}
			for typeName, vs := range validators {
				if diags := runValidators(vs, tc.folder); diags.HasError() == tc.ok {
					t.Errorf("%s.folder %q: accepted = %v, want %v (%v)", typeName, tc.folder, !diags.HasError(), tc.ok, diags)
				}
			}
		})
	}
}

// TestCredentialIdentifierEncodedForms covers encoded, path-parameter and
// look-alike forms of the dot-segments and separators that a credential ID or
// domain name must not smuggle into a URL. escapePathSegment must leave each
// one literal (a "%" is re-encoded, so Jetty never decodes a "%2e%2e" or
// "%2f" into a dot-segment or separator), and the plan-time validators must
// reject the ones that are not plain names.
func TestCredentialIdentifierEncodedForms(t *testing.T) {
	resources, _ := credentialSchemas(t)
	for _, tc := range []struct {
		name, value, escaped string
		nameOK, domainOK     bool
	}{
		{name: "encoded dot-dot", value: "%2e%2e", escaped: "%252e%252e"},
		{name: "encoded upper dot-dot", value: "%2E%2E", escaped: "%252E%252E"},
		{name: "half encoded dot-dot", value: ".%2e", escaped: ".%252e"},
		{name: "dot-dot encoded slash", value: "..%2f", escaped: "..%252f"},
		{name: "encoded backslash", value: "..%5c", escaped: "..%255c"},
		{name: "dot-dot path parameter", value: "..;", escaped: "..%3B"},
		{name: "dot path parameter", value: ".;", escaped: ".%3B"},
		{name: "dot-dot backslash", value: `..\`, escaped: "..%5C"},
		{name: "dot-dot slash", value: "../", escaped: "..%2F"},
		{name: "slash dot-dot", value: "/..", escaped: "%2F.."},
		{name: "nested climb", value: "a/../../b", escaped: "a%2F..%2F..%2Fb"},
		{name: "bare question mark", value: "?", escaped: "%3F", domainOK: true},
		{name: "bare hash", value: "#", escaped: "%23", domainOK: true},
		{name: "dot-dot question mark", value: "..?", escaped: "..%3F", domainOK: true},
		{name: "fullwidth dots", value: "．．", escaped: "%EF%BC%8E%EF%BC%8E", nameOK: true, domainOK: true},
		{name: "spaced dot-dot", value: " .. ", escaped: "%20..%20", nameOK: true, domainOK: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := escapePathSegment(tc.value)
			if err != nil || got != tc.escaped {
				t.Errorf("escapePathSegment(%q) = %q, %v; want %q", tc.value, got, err, tc.escaped)
			}

			for typeName, s := range resources {
				for attr, wantOK := range map[string]bool{"name": tc.nameOK, "domain": tc.domainOK} {
					if typeName == "jenkins_credential_domain" {
						if attr == "domain" {
							continue
						}
						// The domain resource's name is a domain name.
						wantOK = tc.domainOK
					}
					a, ok := s.Attributes[attr].(rschema.StringAttribute)
					if !ok {
						t.Fatalf("%s.%s is not a string attribute", typeName, attr)
					}
					if diags := runValidators(a.Validators, tc.value); diags.HasError() == wantOK {
						t.Errorf("%s.%s %q: accepted = %v, want %v (%v)", typeName, attr, tc.value, !diags.HasError(), wantOK, diags)
					}
				}
			}
		})
	}
}

// TestCredentialDomainFolderOutsideSystemStore: jenkins_credential_domain
// checks its folder only at plan, so a folder from an import ID or from state
// reaches credentialStoreBase as it is. "job" and "/job/" format to nothing,
// which for a credential selects the system store, and "..", "?" and "#"
// change the URL. Whatever request such a folder produces must stay out of
// the system store once Jetty has removed its dot-segments; a domain import
// or destroy that reached it would act on a system domain of the same name.
// A runtime check that sends no request at all also passes.
func TestCredentialDomainFolderOutsideSystemStore(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, folder string
		// system marks the control: no folder is the system store.
		system bool
	}{
		{name: "unset", folder: "", system: true},
		{name: "job", folder: "job"},
		{name: "job slashes", folder: "/job/"},
		{name: "job job", folder: "job/job"},
		{name: "dot-dot", folder: ".."},
		{name: "job dot-dot", folder: "job/.."},
		{name: "triple dot-dot", folder: "../../.."},
		{name: "leading dot-dot", folder: "../x"},
		{name: "trailing dot-dot", folder: "x/.."},
		{name: "sibling", folder: "team-a/../team-b"},
		{name: "question mark", folder: "a?"},
		{name: "hash", folder: "a#"},
		{name: "climb to system with question mark", folder: "x/../../../credentials/store/system/domain/_?"},
	} {
		for _, op := range []string{"import", "update", "delete"} {
			t.Run(op+"/"+tc.name, func(t *testing.T) {
				client, requests := credentialPathRecorder(t, func(r *http.Request) (int, string) {
					if r.Method == http.MethodGet {
						return http.StatusNotFound, ""
					}
					return http.StatusOK, `{}`
				})
				r, sr := pathTestResource(ctx, t, newCredentialDomainResource, client)
				s := sr.Schema

				if op == "import" {
					id := "team"
					if tc.folder != "" {
						id = tc.folder + "/team"
					}
					imported := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
					r.(resource.ResourceWithImportState).ImportState(ctx, resource.ImportStateRequest{ID: id}, &imported)
					if imported.Diagnostics.HasError() {
						t.Fatalf("ImportState(%q): %v", id, imported.Diagnostics)
					}
					read := resource.ReadResponse{State: imported.State}
					r.Read(ctx, resource.ReadRequest{State: imported.State}, &read)
				} else {
					folder := types.StringNull()
					if tc.folder != "" {
						folder = types.StringValue(tc.folder)
					}
					runCredentialOp(ctx, t, r, op, &credentialDomainResourceModel{
						ID: types.StringValue("x"), Name: types.StringValue("team"), Folder: folder, Description: types.StringValue("d"),
					})
				}

				got := requests()
				if tc.system && len(got) == 0 {
					t.Fatalf("folder %q: no request sent", tc.folder)
				}
				for _, req := range got {
					resolved := path.Clean(req.path)
					if inSystem := strings.Contains(resolved, "/credentials/store/system/"); inSystem != tc.system {
						t.Errorf("folder %q: %v resolves to %s; in the system store = %v, want %v", tc.folder, req, resolved, inSystem, tc.system)
					}
				}
			})
		}
	}
}
