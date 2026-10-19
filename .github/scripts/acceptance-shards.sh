#!/usr/bin/env bash
# Runs the acceptance suite in shards for the Acceptance Tests job, each shard
# against its own Jenkins container, so the tests split across Jenkins
# instances instead of running one after another against a single one.
# See "Acceptance test shards" in CI.md.
#
#   acceptance-shards.sh start   start ACC_SHARDS Jenkins containers, detached
#   acceptance-shards.sh run     wait for them, run one shard per container,
#                                and merge coverage into coverage.txt
#
# ACC_SHARDS (default 3) sets the number of shards; shard i's Jenkins listens
# on port 8080+i. TF_ACC, JENKINS_USERNAME and JENKINS_PASSWORD come from the
# caller; each shard gets its own JENKINS_URL.
set -euo pipefail

shards="${ACC_SHARDS:-3}"
image="${ACC_IMAGE:-terraform-provider-jenkins-acc:latest}"
base_port=8080

start() {
  for ((i = 0; i < shards; i++)); do
    docker run -d --name "jenkins-acc-$i" -p "$((base_port + i)):8080" \
      -e JAVA_OPTS=-Djenkins.install.runSetupWizard=false "$image" >/dev/null
  done
}

wait_healthy() {
  local name=$1 status
  for ((t = 0; t < 90; t++)); do
    status=$(docker inspect "$name" --format '{{ .State.Health.Status }}')
    if [ "$status" = healthy ]; then
      return 0
    fi
    sleep 2
  done
  echo "::error::$name did not become healthy (status: $status)"
  docker logs --tail 50 "$name"
  return 1
}

run() {
  local tests=() test i j regex
  while IFS= read -r test; do
    tests+=("$test")
  done < <(go test -list '^TestAcc' ./jenkins/ | grep '^TestAcc' | sort)
  if [ "${#tests[@]}" -eq 0 ]; then
    echo "::error::no acceptance tests found"
    return 1
  fi
  echo "${#tests[@]} acceptance tests in $shards shards"

  for ((i = 0; i < shards; i++)); do
    # Deal the sorted tests round-robin, so each shard gets a similar mix.
    regex=""
    for ((j = i; j < ${#tests[@]}; j += shards)); do
      regex+="${regex:+|}${tests[j]}"
    done
    wait_healthy "jenkins-acc-$i"
    # -count=1: the job restores its Go build cache, which also holds test
    # results; without it an unchanged test binary would report the cached
    # results of an earlier run instead of testing this Jenkins.
    (
      export JENKINS_URL="http://localhost:$((base_port + i))"
      go test -count=1 -v -covermode=atomic -coverprofile="coverage-$i.txt" \
        -run "^(${regex})\$" ./... >"acceptance-$i.log" 2>&1
    ) &
  done

  local failed=0 pid
  for pid in $(jobs -p); do
    wait "$pid" || failed=1
  done

  for ((i = 0; i < shards; i++)); do
    echo "::group::Shard $i (JENKINS_URL=http://localhost:$((base_port + i)))"
    cat "acceptance-$i.log"
    echo "::endgroup::"
  done

  # Each shard's profile covers the same packages; Codecov and `go tool cover`
  # add up the counts of blocks that appear more than once.
  echo "mode: atomic" >coverage.txt
  for ((i = 0; i < shards; i++)); do
    tail -n +2 "coverage-$i.txt" >>coverage.txt
  done

  if [ "$failed" -ne 0 ]; then
    grep -hE '^(--- FAIL|FAIL|panic:)' acceptance-*.log || true
    echo "::error::acceptance tests failed; see the shard logs above"
    return 1
  fi
}

case "${1:-}" in
  start) start ;;
  run) run ;;
  *)
    echo "usage: $0 start|run" >&2
    exit 2
    ;;
esac
