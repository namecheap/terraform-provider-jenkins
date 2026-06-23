FROM jenkins/jenkins:2.555.3-alpine-jdk21

RUN jenkins-plugin-cli --plugins \
    azure-credentials hashicorp-vault-plugin cloudbees-folder pipeline-model-definition git matrix-auth aws-credentials github-branch-source

RUN apk add --no-cache curl

HEALTHCHECK --interval=2s --start-period=3s --retries=60 CMD [ "curl", "-f", "http://localhost:8080" ]
