#!/bin/sh
# Checks that the OSS Scanner image works with no network, the way the scanner
# runs it: build, unit tests, then a Jenkins and a few acceptance tests against
# it. CI runs this with `docker run --network none` (.github/workflows/oss-scanner.yml).
set -eu
cd /src

go build ./...
go test -count=1 ./...

.oss-scanner/start-jenkins.sh
TF_ACC=1 go test -count=1 -v \
  -run '^(TestAccJenkinsFolder_basic|TestAccJenkinsCredentialSecretText_folder|TestAccJenkinsFolder_templateKeepsSecretsOut)$' \
  ./jenkins/
