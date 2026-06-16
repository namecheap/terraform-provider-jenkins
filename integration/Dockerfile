FROM jenkins/jenkins:2.555.3-alpine-jdk21

RUN jenkins-plugin-cli --plugins \
    azure-credentials hashicorp-vault-plugin cloudbees-folder pipeline-model-definition git matrix-auth aws-credentials github-branch-source

HEALTHCHECK --interval=4s --start-period=5s --retries=30 CMD [ "curl", "-f", "http://localhost:8080" ]
