# Design: `jenkins_configuration_as_code` resource

Status: **spike / proposed** · Tracking issue: [#85](https://github.com/namecheap/terraform-provider-jenkins/issues/85)

This document is the design spike required before implementing a
Configuration-as-Code (JCasC) resource. It records the chosen design
direction and answers the five questions the tracking issue requires to be
settled before any code lands.

## Why

Controller-level settings — security realm, authorization strategy, global
pipeline libraries, clouds, tool installations, proxy, the system message —
are unmanageable by this provider today, yet that is where most of the
"Jenkins as Code" value lives. The
[Configuration-as-Code plugin](https://plugins.jenkins.io/configuration-as-code/)
already models every subsystem as YAML with a stable schema and a documented
HTTP API. Wrapping each subsystem in a bespoke Terraform resource would take
years; delegating to JCasC gets the whole surface at once.

## Chosen direction: one resource instance **per top-level section**

The resource is **not** a single monolithic document applied to the whole
controller. Instead, each resource instance owns exactly **one top-level JCasC
root key** (`jenkins`, `security`, `unclassified`, `tool`, `credentials`, …),
declared via a `section` attribute. The `yaml` attribute carries only that
section's subtree.

```hcl
resource "jenkins_configuration_as_code" "system_message" {
  section = "jenkins"
  yaml    = yamlencode({
    systemMessage = "Managed by Terraform — do not edit in the UI."
  })
}

resource "jenkins_configuration_as_code" "security" {
  section = "security"
  yaml    = file("${path.module}/casc/security.yaml") # security: subtree only
}
```

Rationale for per-section over whole-document:

- **Bounded blast radius.** A single resource can only affect its own subtree,
  which matches the "minimal blast radius" review rule and makes partial-apply
  failures (Q4) easy to reason about.
- **Composability.** Different teams/modules can own different sections without
  fighting over one giant document.
- **Cleaner drift.** Drift comparison is scoped to one subtree instead of
  diffing the entire exported configuration (Q2).
- **Progressive adoption.** Start with `jenkins.systemMessage`, grow to
  `security` and `unclassified`, one reviewable resource at a time.

Trade-off: the JCasC apply endpoint takes a whole document, so each instance
POSTs a **single-key document** (`{<section>: <subtree>}`). JCasC merges it into
the running configuration; sections not present in the posted document are left
untouched (see Q1). `section` is the resource ID; two instances declaring the
same section is a configuration error (see Q1/Q5).

---

## Q1 — Apply semantics: replace vs. merge; multiple instances?

- **Merge at the document level.** JCasC's `apply`/`configure` endpoint applies
  the configurators named in the posted document and leaves unmentioned
  top-level sections alone. Posting `{jenkins: {systemMessage: "..."}}` does not
  wipe `security` or `unclassified`. This is what makes per-section instances
  safe to coexist.
- **Within a section, semantics follow the JCasC configurator.** Scalars are
  replaced; some list-valued configurators replace the whole list, others
  append. We do **not** try to override the plugin's per-configurator behavior.
  The contract we commit to: *the subtree you declare is the desired state for
  the keys it names.*
- **Multiple instances: yes, one per distinct `section`.** `section` is unique
  per instance and is the ID. Two instances with the same `section` is a
  user error — last apply wins and they will fight on every plan. We detect and
  reject this where possible (see Q5) rather than silently thrashing.

## Q2 — Drift detection against an export full of defaults

`GET /configuration-as-code/export` returns the entire configuration, including
every default value. Comparing it verbatim would report constant drift.

- **Subset comparison.** On Read we export, select the managed `section`
  subtree, and compare it against the declared `yaml` as a **deep subset**:
  only keys present in the declared document are compared; unmanaged keys and
  plugin defaults within the section are ignored.
- **Normalization.** Both sides are parsed YAML → generic maps and compared
  semantically: key order, indentation, quoting style, and scalar formatting
  are not drift. This mirrors the semantic-equality approach already used for
  `jenkins_job.template` (`templatesEqual`).
- **State model.** State stores the user's **declared** `yaml` (the plan value),
  exactly as written; Read refreshes a normalized readback only to decide
  whether a diff exists — it does not overwrite the stored document with the
  export's verbose form. Again mirroring the `jenkins_job` template pattern, so
  formatting churn from the server never appears as a plan diff.

## Q3 — Secrets: `${...}` interpolation, never in state

- **Authoring.** Secrets are referenced with JCasC's interpolation syntax
  (`${SECRET_KEY}`, `${SECRET_KEY:-default}`). JCasC resolves them **at apply
  time** from its configured secret sources (environment variables, files,
  Vault, etc.). Raw secret values are never written inline in the YAML.
- **State.** The provider stores the YAML **with the `${...}` placeholders
  intact**. The resolved secret value is never read back into or stored in
  Terraform state.
- **Drift for secret-valued keys.** For any declared key whose value is a
  `${...}` expression, drift comparison compares the **placeholder form**, not a
  resolved value — the export re-emits such keys as their interpolated form or a
  redacted marker, so we compare structurally and never resolve. This prevents
  both false drift and secret leakage.
- **Tested invariant.** An acceptance test asserts that a secret supplied via
  `${...}` does not appear in the resource state.

## Q4 — Failure atomicity

- **Not globally transactional.** JCasC validates the document, then applies
  configurators. Apply is best-effort per configurator and is **not** rolled
  back across configurators if a later one fails. We do not pretend otherwise.
- **Surfaced verbatim.** On failure the JCasC validation/apply error is surfaced
  in the Terraform diagnostic unmodified, so the operator sees exactly which
  key/configurator failed.
- **Bounded by per-section scope.** Because one instance carries one subtree, a
  partial failure is confined to that section — the primary reason we chose
  per-section. We recommend keeping sections small.
- **State on failure.** On apply error we do not write new state: Create leaves
  the resource uncreated; Update leaves prior state in place. The next plan
  re-reads actual server state (Q2) and shows the real diff, so a retry is
  meaningful rather than colliding with a half-written state.

## Q5 — Interaction with typed resources

- **Precedence is last-writer-wins at the Jenkins level** — neither JCasC nor a
  typed resource has intrinsic priority; whichever applied most recently wins.
- **Rule: do not overlap.** A `jenkins_configuration_as_code` instance must not
  manage a section that a typed resource also manages (e.g. don't manage
  `jenkins.nodes` via JCasC if you use a future `jenkins_node`). Overlap causes
  perpetual drift as the two reconcile to different desired states.
- **Guidance.** Use JCasC for subsystems with no typed resource (system
  message, security realm, authorization strategy, global libraries,
  `unclassified`); use typed resources for objects with their own lifecycle
  (jobs, folders, credentials, views). The docs will state this split and the
  no-overlap rule prominently.
- **Enforcement.** `section` uniqueness is validated within a configuration
  where detectable; cross-resource-type overlap cannot be statically detected,
  so it is documented as a constraint rather than enforced.

---

## Progressive-adoption guide (outline for user docs)

1. **System message** — smallest possible `section = "jenkins"` document; safe,
   visible, reversible. Proves the workflow end to end.
2. **Unclassified** — global tool config, global pipeline libraries.
3. **Security realm & authorization** — highest value, highest risk; adopt only
   after 1–2 are comfortable, and keep a break-glass admin path.

## Implementation plan (follow-up PRs, kept small)

1. **This PR** — design doc (this file). *Spike gate.*
2. Resource skeleton: schema (`section`, `yaml`, computed `id`), custom
   semantic-equality string type for `yaml`, Create/Read/Update/Delete against
   the JCasC `apply`/`export` endpoints; unit tests for normalization and subset
   comparison.
3. Drift + secrets: subset/normalized comparison, `${...}` handling, unit tests
   incl. the "secret never in state" assertion.
4. CI + acceptance: the configuration-as-code plugin is already in the CI
   Jenkins image (added with the RBAC work); add an acceptance test and the
   progressive-adoption user guide.

Each step is independently reviewable and behavior-additive; the resource is not
registered in the provider until step 2 lands.
