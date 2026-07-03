FROM jenkins/jenkins:2.571-alpine-jdk21

RUN jenkins-plugin-cli --plugins \
    azure-credentials hashicorp-vault-plugin cloudbees-folder pipeline-model-definition git matrix-auth aws-credentials github-branch-source

HEALTHCHECK --interval=2s --start-period=3s --retries=60 CMD [ "curl", "-f", "http://localhost:8080" ]
