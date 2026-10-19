#!/bin/sh
# Starts the test Jenkins inside the OSS Scanner image, in the background, and
# waits until it answers. It is the same Jenkins as integration/Dockerfile: the
# LTS core, the same plugins, and integration/jenkins.yaml (admin/admin).
# Logs go to /tmp/jenkins.log. Needs no network.
set -eu

export JENKINS_HOME="${JENKINS_HOME:-/tmp/jenkins_home}"
export CASC_JENKINS_CONFIG=/src/integration/jenkins.yaml
export JAVA_OPTS="-Djenkins.install.runSetupWizard=false ${JAVA_OPTS:-}"
mkdir -p "$JENKINS_HOME"

# jenkins.sh copies the plugins from /usr/share/jenkins/ref into JENKINS_HOME.
nohup /usr/local/bin/jenkins.sh >/tmp/jenkins.log 2>&1 &

i=0
until curl -fs -o /dev/null -u "$JENKINS_USERNAME:$JENKINS_PASSWORD" "$JENKINS_URL/api/json"; do
  i=$((i + 1))
  if [ "$i" -ge 120 ]; then
    echo "Jenkins did not start within 240s; last lines of /tmp/jenkins.log:" >&2
    tail -n 50 /tmp/jenkins.log >&2
    exit 1
  fi
  sleep 2
done
echo "Jenkins is up at $JENKINS_URL"
