# Threat model

`namecheap/terraform-provider-jenkins` is a Terraform plugin-framework provider. It uses the operator's Jenkins admin credentials to manage credentials and credential domains, jobs (including pipeline and multibranch pipeline jobs), folders, views, role-strategy roles, users, nodes, plugins and JCasC sections over Jenkins' HTTP API. All provider code lives in `jenkins/`. HTTP goes through `jenkins/config.go` (the transport stack plus a hand-written adapter) and the gojenkins client.

## What this project does and where untrusted input enters

**Trusted:** the operator running Terraform with Jenkins admin credentials, and config values that pass plan-time validators. Anything an admin-credential holder can already do directly in Jenkins is not a finding.

**Attacker:** controls what Jenkins returns, or influences Terraform inputs that skip validation.
- Controlling Jenkins responses: a compromised controller; a MITM when `insecure=true`; a lower-privileged Jenkins user with Item/Configure, Computer/Configure or folder Credentials/Create; or another JCasC source or plugin.
- Unvalidated Terraform inputs: import IDs, state written by older provider versions, values unknown at plan, and IDs from list data sources (`jenkins_credentials`, `jenkins_jobs`, `jenkins_folders`, `jenkins_nodes`) that `docs/guides/adopting-existing-jenkins.md` recommends feeding into bulk `for_each` imports. The job, folder and node names are server-supplied and flow into the item and node import paths.

**Entry points:**
- **Path identifiers:** credential `name`/`domain`/`folder`; item `name`/`folder`; and view, node, user and plugin names.
  - gojenkins interpolates them raw: `CredentialsManager.fillURL` (Sprintf), `"/job/"+strings.Join(parts,"/job/")`, `"/computer/"+name`, `/pluginManager/plugin/%s/doUninstall`, and `<install plugin="%s@%s" />`.
  - `Requester.Do` then runs `url.Parse(Base+endpoint)`. An unescaped `?` or `#` cuts the path, and Jetty resolves `..`.
  - Reference bug: issue #250. A folder-store credential with domain `../../../../../../credentials/store/system/domain/_/credential/prod-deploy-key/config.xml?` overwrote a system-store credential.
  - `ImportState` in `resource.go` (credentials) and in the domain, job, folder, view, node, plugin, user, pipeline_job and multibranch_pipeline resources only records the ID (or, for job, folder, pipeline_job and multibranch_pipeline, parses it with `parseCanonicalJobID`/`formatFolderID`), so plan validators do not run on import IDs. The role ImportState requires the `<type>/<name>` shape and rejects comma names through `checkRoleName`, but runs no other validation.
- **Server documents copied into state or posted back:**
  - credential config.xml (`GetSingle` -> each spec's `fromAPI`; the certificate `Read` writes server `cred.ID` into `id`)
  - item and folder config.xml (`Job.GetConfig` returns the body of any response the retry layer does not turn into an error: every status except 429 and 5xx, with 501 excepted, so a 501 body is also returned. This includes the `{}` that `jenkinsNotFoundTransport` substitutes)
  - node config.xml (`resource_jenkins_node.go:writeConfig` re-POSTs unmanaged elements verbatim)
  - the JCasC export (`ExportCASC` -> `casc_yaml.go:cascDriftView`)
  - JSON from getRole, user, view, pluginManager and the crumb issuer, and the JNLP document (`GetNodeSecret`)
- **Server status codes and headers:**
  - A 404 drops the resource from state (`util.go:isNotFound`), and the next apply recreates it and re-sends its secrets.
  - A POST answered with 302 becomes 200 with body `{}` (`jenkinsPostRedirectTransport`).
  - Other redirects follow Go's default `http.Client` policy (`newHTTPClient` sets no `CheckRedirect`).
  - An `X-Error` header becomes the error text (gojenkins `Requester.Do`), for any status.
  - `enrichErrorHandler` adds a body excerpt of up to 512 bytes, plus the URL, to diagnostics.
- **Secret material that must never reach plan, state, logs or diagnostics:**
  - `<secret>_wo` values (read only from `req.Config` by `resource.go:readWriteOnly`)
  - values JCasC resolved for `${...}` placeholders
  - the node agent secret (ephemeral resource)
  - the folder credentials store (`FolderCredentialsProperty`) and `{AQAA...}` ciphertext in item config.xml

**Claimed properties.** Each of these has regression tests, so breaking one is a finding:
- **Credential paths:**
  - Domain and ID always reach Jenkins as exactly one path segment: `util.go:escapePathSegment` and `escapeCredentialPath` reject `.` and `..`, PathEscape the value, and re-encode `%`.
  - `credentialsFolder` requires a non-empty folder to name a real folder (`job`, `/` and `/job/` must not select the system store) and rejects folder segments that are `.`/`..` or contain `? # % ; \`.
  - These checks run at runtime in every CRUD path, in `deleteCredential`, in the data sources, and in the Read that follows an import, and they fail before any request is sent.
- **Credential domains:** `config.go:credentialStoreBase` does no validation itself. It relies on `resource_jenkins_credential_domain.go:checkCredentialDomainFolder` in every CRUD method.
- **Write-only and read-back secrets:**
  - `_wo` values never reach plan or state.
  - No `fromAPI`, and not the certificate Read, copies a secret back from Jenkins.
- **Templates:**
  - `config_redact.go:redactJenkinsConfig` keeps the store and ciphertext out of the `jenkins_folder` template and the job/folder data-source templates.
  - `jenkins_job` keeps an unmanaged store out of state, tracked by the private-state key `template_has_folder_credentials`.
  - `jenkins_job` Update splices the live store back in (`withFolderCredentials`) and fails closed through `currentItemConfig`/`checkItemConfig`.
  - `jenkins_folder` Update rebuilds the document from a fresh read and aborts if the root is not a folder (`folder.go:parseFolder`).
- **JCasC:**
  - State `yaml` never holds a resolved `${...}` value or an unmanaged exported key (`driftView`).
  - Import stores only `section` and `id`.
  - Nothing derived from the export is POSTed back.
- **Roles:**
  - No `,` in a role name or permission ID reaches addRole or removeRoles (`config.go:checkRoleName`/`checkPermissionID`), including on import, on destroy and from old state.
  - Create refuses to take over an existing role.
- **Node secret:** it never appears in an error (`config.go:GetNodeSecret`). The document must be at most 1 MiB, have a `<jnlp>` root, and name the requested node as `argument[1]`.
- **Transport:** `isNotFound` recognises a 404 from the provider transport's `statusError` and from gojenkins error text (`notFoundStatusRe`). State-changing POSTs are never retried (`retryRoutingTransport`); TLS verification stays on unless `insecure=true`.

## Components that matter most / least

**Most:**
- **`jenkins/util.go`:**
  - `escapePathSegment`, `escapeCredentialPath` and `credentialsFolder`: the only runtime defence for #250.
  - `parseCanonicalJobID`, `extractFolders` and `formatFolderName`: item paths.
  - `isNotFound`.
- **`jenkins/validations.go` non-credential path validators:** `folderNameValidator` and the item, node and view name validators, together with the URL builders behind them: they decide what reaches Jenkins for item, node and view paths.
- **`jenkins/config.go`:**
  - Transports: `newHTTPClient` (TLS, `ca_cert`, redirects), `retryRoutingTransport`, `jenkinsPostRedirectTransport`, `jenkinsNotFoundTransport`, `enrichErrorHandler`.
  - Adapter: `credentialStoreBase`/`*CredentialDomain`, `AddRole`/`RemoveRole`/`roleEntryPost`, `GetNodeSecret`, `Get`/`UpdateNodeConfig`, `CreateUser`/`GetUser`/`DeleteUser`, `GetView`, `ApplyCASC`/`ExportCASC`.
- **Credential CRUD:** `jenkins/credential_crud.go` and `jenkins/resource.go` (`credentialManager`, `credentialManagerForFolder`, `deleteCredential`, `ImportState`, `addWriteOnlySecret`, `readWriteOnly`, `schemaCredential`).
- **`resource_jenkins_credential_certificate.go`:** hand-written CRUD outside the shared flow. It always re-sends the keystore and password and takes `id` from the server. Audit it separately.
- **Templates and redaction:**
  - `config_redact.go`: redaction regexes, `isSecretCiphertext`, `withFolderCredentials`, `checkItemConfig`, `redactedSecretValidator`.
  - `resource_jenkins_job.go`: `jobTemplateFromServer`, `jobTemplateForUpdate`, `currentItemConfig`.
  - `resource_jenkins_folder.go` together with `folder.go`: `parseFolder`, and the `xmlRawProperty` innerxml round-trip that carries the store.
- **Pipeline and multibranch items:** `resource_jenkins_pipeline_job.go` and `resource_jenkins_multibranch_pipeline.go` (`folder` handling, ImportState through `parseCanonicalJobID`, and the multibranch Update, which posts a full document).
- **JCasC:**
  - `casc_yaml.go`: `driftView`, `yamlSubset`, `hasInterpolation`, `reSecretPlaceholder`.
  - `resource_jenkins_configuration_as_code.go`: Read, ImportState and apply.
- **Roles:** `resource_jenkins_role.go` (authoritative `reconcile`, the Create existence check, rollback).
- **Nodes:** `resource_jenkins_node.go` together with `node_config.go`.

**Medium:**
- `resource_jenkins_credential_domain.go`.
- The per-type `resource_jenkins_credential_*.go` specs: `fromAPI` must never copy a secret, and `build` keys must match `secretFields`.
- Credential data sources: `credential_data_source.go` and `data_source_jenkins_credentials.go`.
- Template data sources: `data_source_jenkins_job.go` and `data_source_jenkins_folder.go`.
- Provider, user and plugin code: `provider_framework.go`, `resource_jenkins_user.go`, `resource_jenkins_plugin.go`.
- Validation: `validations.go:jobXMLValidator`.
- gojenkins `credentials.go` and `request.go`, as the provider calls them.

**Least:**
- `validations.go` credential path validators (`pathSegmentValidators`, `domainNameValidators`, `credentialFolderValidator`, `preEncodedValidator`): plan-time defence in depth only; the runtime credential checks are authoritative.
- `template_type.go`: diff suppression only.
- View resource and data source (except the name handling listed above).
- Read-only list and lookup data sources: `data_source_jenkins_{jobs,folders,nodes,node,plugin}.go`. Their outputs are server-supplied names that users feed into imports.
- Thin wrappers: `data_source_jenkins_credential_*.go`, and the node-secret data source and ephemeral resource.
- `extractSectionYAML` (now used only by tests), the CasC Delete (a no-op), and the `coverage_*` tests.

## How to exercise it

**Unit tests (offline):** run `go test ./jenkins/ -count=1`. For a focused run, add `-run` with a regex such as:
- `'TestEscapePathSegment|TestCredential|TestCovL_'`
- `'TestRedactJenkinsConfig|TestJobResource|TestFolderResource'`
- `'TestCASC|TestConfigurationAsCode'`
- `'TestRole|TestGetNodeSecret|TestNodeResource|TestNewHTTPClient'`

**Credential paths:**
- `credential_path_test.go:credentialPathRecorder(t, serve)` is an httptest Jenkins that records the method, `r.URL.EscapedPath()` and `RawQuery`. Pair it with `runCredentialOp(ctx,t,r,"create|read|update|delete",model)` and `assertRequests(t, got, want...)`.
- In `credential_path_guard_test.go`, `pathTestCredentialResources`/`pathTestCredentialDataSources` iterate every registered type; `pathTestObject`, `pathTestResource` and `pathTestOpRequest` build models and expected requests.
- For imports, call `ImportState` with a null Raw state and then Read, as `TestCredentialImportPathGuards` does.

**Server-shaped credential responses:** `coverage_credlive_test.go` provides `covCredClient(t, credXML)`, `covCredClientCode(t, postCode, getCode)` and `covLCredCRUD`.

**Write-only secrets:** `runCredentialOp` uses the same Raw value for Config and Plan. Build `tfsdk.Config` separately, set `_wo` in config only, vary `_wo_version`, and inspect the recorded POST body.

**Templates:**
- `config_redact_read_test.go:redactFolderServer` serves and accepts config.xml and captures POSTed bodies in `.posted`. It has the knobs `dropGet`, `errorStatus` and `errorPage`, and `.job(t, base)` wires in the real transports.
- Fixtures: `redactFolderWithCredentials(token)` and `redactCipher*`. Leak check: `assertNoFolderSecrets`.
- For job CRUD, call `initPrivateState` first.
- `coverage_helpers_test.go:covcLiveJob(t, base, body)` serves a fixed config body.

**JCasC:**
- `cascTestResource(export)` builds the resource on a mock export; check state with `assertCASCStateOmits` and `cascStateYAML`.
- For the real client, use `cascTestServer(handler)` with `newJenkinsClient(&Config{ServerURL: srv.URL})` and `tflogtest.RootLogger`, and check diagnostics with `assertCASCDiagsOmit`.

**Roles, nodes and users:**
- Roles: `roleRequestLog.server` and `roleStateWithID` (injects state that ImportState would refuse).
- Nodes: `nodeConfigStore`, `nodeXML`, `jnlpDocument(secret, name)` and `fakeNodeSecret`.
- `covBServer`/`covBClient` and `covStatusClient` serve crumb probes and fixed statuses.
- `covMSchema`/`covMNull`/`covMResourceCRUD` build `tfsdk` Plan/State for direct Create/Read/Update/Delete/ImportState calls.

**Mocks:** `config_test.go:mockJenkinsClient` has one hook per client method. It bypasses the adapter's guards, so prove adapter-level claims with the real adapter over httptest.

**Acceptance (offline Jenkins):** run `.oss-scanner/start-jenkins.sh` (http://localhost:8080, admin/admin, CasC from `integration/jenkins.yaml`; the `JENKINS_*` variables are set in the image), then `TF_ACC=1 go test -count=1 -run '^TestAcc...' ./jenkins/`. Useful tests:
- `TestAccJenkinsCredentialPath_*` (`_preValidatorState` writes pre-#250 state through `testAccPreValidatorProviders`)
- `TestAccJenkinsCredential*_writeOnly`
- the tests in `config_redact_acc_test.go`
- `TestAccJenkinsConfigurationAsCode_*NotInState`
- `TestAccJenkinsRole_comma*`
- `TestAccJenkinsNodeSecretEphemeralResource_basic` (needs Terraform >= 1.11)

`TestAccJenkinsJob_folderTemplateUpgradeKeepsCredentials` downloads provider 1.4.0 from the Terraform registry, so it needs network access.

## How you rate severity

- **Critical:** attacker-controlled input makes the provider write to or delete objects outside the intended target, or sends secrets off the controller. Targets include another credential store, the system store, another role, or another item. Examples: a #250-class path escape, or one destroy removing two roles.
- **High:** secrets reach state, plan, saved plans, logs or diagnostics. This covers `_wo` values, secrets read back from Jenkins, folder credential stores, `{AQAA...}` ciphertext, and JCasC-resolved `${...}` values. Also high: a routine apply silently erases stored secrets (for example a folder store dropped on `jenkins_job` Update, or an empty element overwriting a stored secret) or deletes data.
- **Medium:** the same impact with an unusual precondition, such as a proxy returning error pages, `insecure=true`, a non-default setting, or pre-fix state. Also medium: a fail-open that loses data only in rare conditions, and a provider crash or hang caused by a server response (for example a nil dereference or an unbounded read).
- **Low:** defence-in-depth gaps not reachable through Terraform (for example only by calling Go functions directly, past both validators and runtime checks), and exposure of non-secret information.

A fully compromised controller already holds whatever it was sent. A finding must show the provider doing something beyond that: acting on other objects, moving secrets into state or diagnostics, or crashing.

## Anything to leave alone

**Out of scope:**
- Jenkins core and plugins, Terraform core, and third-party Go modules, unless the provider's own use makes them exploitable (gojenkins URL building as the provider calls it is in scope).
- `docs/`, `examples/`, `demo/`, `integration/` fixtures, `tools/`, and `*_test.go` helpers.

**Accepted behaviour (not findings):**
- **Secrets in state by design (marked Sensitive):**
  - Plain, non-`_wo` secret attributes. This includes `jenkins_user.password`, the SSH `passphrase`, the certificate `password` and the Azure `certificate_id`, none of which has a `_wo` variant.
  - The `jenkins_node_secret` data source's value; the ephemeral resource is the documented alternative.
  - Literals written in JCasC `yaml`, which is not Sensitive (users are expected to write `${VAR}`).
- **Secrets not refreshed or managed:**
  - Secrets are not refreshed, so out-of-band changes go undetected.
  - Changing `_wo` without bumping `_wo_version` does nothing.
  - An optional secret left unset is unmanaged.
  - Import never brings secrets in.
- **Name handling:**
  - Domain names may contain `?` and `#`; they are escaped and sent literally.
  - Look-alike dots (`．．`, ` .. `) are accepted as literal names.
  - Credential data sources only warn on `%`.
- **Transport and lifecycle:**
  - A genuine 404 status on refresh removes the resource from state, so the next plan recreates it.
  - A 302 on POST counts as success where the provider confirms the result by reading back. It remains a finding when the 302 body is consumed as data (`ExportCASC`) or when a destructive or state-changing call is not verified.
  - Deleting a domain deletes its credentials.
  - Changing `name`, `domain` or `folder` forces replacement.
  - Diagnostics may carry up to 512 bytes of the response body and the URL. That is a finding only when the text carries a secret: a credential secret, a value JCasC resolved for `${...}`, or ciphertext.
- **Template and redaction limits:**
  - `jenkins_job` state keeps ciphertext outside a store, and keeps the whole store when the applied template contains it.
  - Removing the store from a template that had it deletes the folder's credentials; the plan shows this.
  - Not redacted: legacy unbraced secrets, and prefix-less braced base64 that is not an element's entire text.
  - The `{redacted}` validator matches only whole element values.
  - When the live config has no store, the template is sent unparsed.
  - The `jenkins_folder` security block is authoritative.
- **JCasC:**
  - A drifted plain scalar is copied into state, even if another source resolved it from `${...}`.
  - A literal declared for a Secret-typed field takes the exported ciphertext into state on drift.
  - Placeholder-valued keys never show drift.
  - Nested placeholders and lists the controller reorders give a permanent plan diff.
  - Delete is a no-op, and a missing section means recreate.
- **Roles:**
  - Assignments are authoritative.
  - An item or agent role with no pattern in config is planned as `.*`.
  - Role names containing commas cannot be destroyed or imported by the provider.
  - A JCasC `authorizationStrategy` deletes managed roles.
  - A role whose read-back fails after Create is kept and saved as planned (tainted), not rolled back.
- **Nodes:**
  - Settings the resource does not manage keep their controller values.
  - An uncompilable `job_restriction` regex accepts every job.
  - `num_executors` must be at least 1.
  - Each node-secret fetch through the JNLP file logs a warning on the controller.
- **Provider settings and plugins:**
  - `insecure=true` disables TLS verification by opt-in.
  - There is no default request timeout, and idempotent requests are retried.
  - `jenkins_plugin` destroy keeps the plugin unless `uninstall_on_destroy` is set.

## How to report

- **PoC:** preferably a Go unit test in `jenkins/` built on the existing httptest fakes (`credentialPathRecorder`, `redactFolderServer`, `cascTestServer`, `roleRequestLog`, `nodeConfigStore`, `covCredClient`). It should assert the exact escaped request (`r.URL.EscapedPath()`, `RawQuery`) or POST body, the leaked value in state, diagnostics or `tflogtest` logs, or the crash. Alternatively, write an acceptance test against the offline Jenkins (`TF_ACC=1`).
- **Entry point:** name how the input arrives (config, import ID, old state, value unknown at plan, or server response) and why the plan validators do not stop it.
- **Patch:** keep it minimal, reusing existing guards (`escapePathSegment`, `credentialsFolder`, `redactJenkinsConfig`, `checkItemConfig`, `checkRoleName`) rather than adding new layers.
- **Deduplication:** one report per root cause, with every reachable caller listed. For example, all unescaped item-path builders in gojenkins form one finding, and so do all callers of a single transport rewrite.
- **Classification:** say whether the issue breaks a claimed property above (a regression) or is new.
