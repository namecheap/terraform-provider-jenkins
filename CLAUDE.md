# terraform-provider-jenkins

## Git

- Never add "Co-Authored-By: Claude" trailers, "Generated with Claude Code" lines, or any other AI-attribution markers to commit messages, PR descriptions, or code.
- Commit messages follow Conventional Commits (e.g. `chore(deps): ...`, `test: ...`, `docs: ...`).

## Local development loop

Everything CI checks can be run locally. Run the relevant commands before
pushing — CI failures on formatting, lint or stale docs are avoidable.

| Task | Command | Notes |
|---|---|---|
| Build | `go build ./...` | |
| Unit tests | `make test` | `go test -cover ./...`, a few seconds |
| Unit tests as CI runs them | `go test -race -covermode=atomic ./...` | the race detector is on in CI; run it before pushing concurrency-adjacent changes |
| Lint | `make lint` | `golangci-lint run ./...`, must report `0 issues.` |
| Auto-fix formatting | `make fmt` | `golangci-lint fmt ./...` |
| Acceptance tests | `make testacc` | needs Docker; see below |
| Regenerate docs | `make generate` | needs `terraform` on `PATH`; see below |
| Install a dev build | `make build` | prints the `~/.terraformrc` `dev_overrides` snippet |

### Acceptance tests

`make testacc` builds and starts the Jenkins container from `integration/`,
waits for its healthcheck, runs the suite, and tears the container down. The
full suite takes roughly 3–4 minutes.

To iterate without losing the container between runs, start it once and drive
`go test` yourself:

```bash
docker compose -f integration/docker-compose.yml up -d --force-recreate jenkins
# wait for: docker inspect jenkins-provider-acc --format '{{ .State.Health.Status }}' == healthy

TF_ACC=1 JENKINS_URL="http://localhost:8080" \
  JENKINS_USERNAME="admin" JENKINS_PASSWORD="admin" \
  go test ./jenkins/ -run 'TestAccJenkinsFolder' -v
```

Without `TF_ACC=1` every acceptance test is skipped, so a green `go test ./...`
proves much less than it looks like. Run the acceptance suite for any change to
a resource's CRUD or schema.

The container is also the place to settle questions about what Jenkins actually
does, rather than assuming. Apply a configuration through a throwaway
acceptance test and read the resulting `config.xml` back from the `template`
attribute — that is how the zero-permission `AuthorizationMatrixProperty`
question in #193 was answered.

### Docs

`docs/` is generated. CI runs `make generate` and fails on any diff, so
regenerate and commit after changing anything in `templates/` or any schema
`MarkdownDescription`. Note that `templates/resources/folder.md.tmpl` and
`job.md.tmpl` are hand-written pages with no `{{ .SchemaMarkdown }}`: changing
those schemas produces no docs diff, and the prose has to be updated by hand
(see #225).

## Testing conventions

- Unit tests use the fakes in `jenkins/config_test.go` (`mockJenkinsClient`,
  with a `mockGetJob`/`mockGetRole`/... hook per method) and
  `covcLiveJob` in `jenkins/coverage_helpers_test.go`, which returns a
  `*jenkins.Job` wired to an `httptest` server so `job.GetConfig` runs its real
  path against canned XML.
- Prefer table-driven tests with `t.Run` subtests; that is the prevailing style
  in `jenkins/`.
- A regression test must be shown to fail without the fix. Revert the fix, watch
  the new test fail for the stated reason, restore it. A test that passes
  against the bug documents nothing.
- Acceptance tests that assert a round trip should re-apply the same
  configuration in a second step, so a plan/refresh cycle has to agree with the
  server too.

## Verification before claiming something works

State outcomes with the command and its output, not from expectation. Before
saying a change is done, run the checks that apply to it (`make lint`,
`make test`, the acceptance suite for resource changes, `make generate` for
schema or template changes) and report what they printed — including failures.

## CI gates

`main` requires these checks to pass: `Lint`, `Unit Tests`, `Acceptance Tests`,
`Integration Tests`, `Docs`, `Conventional PR Title`, `CI OK`, `CodeQL (go)`,
plus one approving review. `CI.md` explains the pipeline, the `changes` gate
that skips test jobs on docs-only and release PRs, and why `CI OK` exists.

The unit job additionally enforces a 5% total-coverage floor — a guard against
deleting unit tests wholesale, not a coverage target; this provider gets most of
its coverage from acceptance tests.

PR titles are enforced as Conventional Commits, because the squash-merge subject
is what release-please reads. A change that alters an attribute's type or any
other observable schema detail needs a `!` or a `BREAKING CHANGE:` footer, so
the release automation cuts a minor rather than a patch — see the v1.2.2 entry
in `CHANGELOG.md` for what happens otherwise.
