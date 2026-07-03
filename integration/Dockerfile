FROM jenkins/jenkins:2.571-alpine-jdk21

RUN jenkins-plugin-cli --plugins \
    azure-credentials hashicorp-vault-plugin cloudbees-folder pipeline-model-definition git matrix-auth aws-credentials github-branch-source \
    role-strategy configuration-as-code mailer

# Apply the Configuration-as-Code file at startup. It enables the Role-Based
# Authorization Strategy (required by the jenkins_role resource) while keeping
# access fully open, so the existing test suite is unaffected.
COPY jenkins.yaml /var/jenkins_home/casc.yaml
ENV CASC_JENKINS_CONFIG=/var/jenkins_home/casc.yaml

HEALTHCHECK --interval=2s --start-period=3s --retries=60 CMD [ "curl", "-f", "http://localhost:8080" ]
