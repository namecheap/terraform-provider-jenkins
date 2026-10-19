# check=skip=SecretsUsedInArgOrEnv
# (JENKINS_PASSWORD below is the fixed admin/admin of the throwaway test Jenkins
# from integration/jenkins.yaml, not a secret.)

# Environment for Anthropic's OSS Scanner (https://red.anthropic.com/oss-scanner/).
#
# The scanner builds this file with the repository root as the build context and
# with network access, then runs the image with no network. Everything an agent
# needs to build the provider and run its tests is fetched here: Go modules, the
# Terraform CLI that acceptance tests drive, and a Jenkins with the plugins the
# tests use. CI builds this image and runs .oss-scanner/selftest.sh in it with
# the network off (.github/workflows/oss-scanner.yml); see "OSS Scanner image" in
# CI.md.

# Go is at least go.mod's toolchain version; Jenkins is the LTS line the
# acceptance tests use, in its "-lts-" tag form so Dependabot stays on LTS.
FROM golang:1.26.9-trixie AS go
FROM hashicorp/terraform:1.15.1 AS terraform
FROM jenkins/jenkins:2.580.1-lts-jdk21

# The scanner adds its own layer with apt and runs as root in /src.
USER root

COPY --from=go /usr/local/go /usr/local/go
COPY --from=terraform /bin/terraform /usr/local/bin/terraform

# GOTOOLCHAIN=local: with no network Go must not try to fetch a toolchain.
# TF_ACC_TERRAFORM_PATH: terraform-plugin-testing uses this CLI instead of
# downloading one. JENKINS_*: what the acceptance tests and start-jenkins.sh use.
ENV PATH=/usr/local/go/bin:/go/bin:$PATH \
    GOPATH=/go \
    GOCACHE=/go/cache \
    GOTOOLCHAIN=local \
    TF_ACC_TERRAFORM_PATH=/usr/local/bin/terraform \
    JENKINS_URL=http://localhost:8080 \
    JENKINS_USERNAME=admin \
    JENKINS_PASSWORD=admin

# Same plugins as integration/Dockerfile; the oss-scanner workflow fails if the
# two lists differ.
RUN jenkins-plugin-cli --plugins \
    azure-credentials hashicorp-vault-plugin cloudbees-folder pipeline-model-definition git matrix-auth aws-credentials github-branch-source \
    role-strategy configuration-as-code mailer job-restrictions

# Scanner contract: the checkout lives at /src.
COPY . /src
WORKDIR /src

# Fetch every module, build the provider and compile the test binaries, so the
# offline image needs nothing else to build or test.
RUN go mod download \
    && go build ./... \
    && go vet ./... \
    && go test -count=1 -run '^$' ./...

# Run the unit tests so the build log shows the image works, without failing
# the build on a test failure.
RUN go test -count=1 ./... || echo "WARNING: go test reported failures"

# Open a shell in /src rather than start Jenkins; start-jenkins.sh does that.
ENTRYPOINT []
CMD ["/bin/bash"]
