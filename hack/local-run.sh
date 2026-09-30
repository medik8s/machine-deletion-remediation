#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT"
TOOLS_DIR=${TOOLS_DIR:-"$ROOT/../tools"}
if [[ -d "$TOOLS_DIR" ]]; then
    export TOOLS_DIR
else
    unset TOOLS_DIR
fi
TOOLS_DIR_RESOLVED=${TOOLS_DIR:-"$ROOT/.tools"}
export MEDIK8S_CLUSTER_NAME=${MEDIK8S_CLUSTER_NAME:-medik8s-dev}
export CONTAINER_TOOL=${CONTAINER_TOOL:-podman}
export KIND_EXPERIMENTAL_PROVIDER=${KIND_EXPERIMENTAL_PROVIDER:-${CONTAINER_TOOL##*/}}
export OPERATOR_NAMESPACE=${OPERATOR_NAMESPACE:-${OPERATOR_NS:-openshift-workload-availability}}
export OPERATOR_NS=${OPERATOR_NS:-$OPERATOR_NAMESPACE}
[[ "$OPERATOR_NS" == "$OPERATOR_NAMESPACE" ]] || { echo "OPERATOR_NS and OPERATOR_NAMESPACE must match." >&2; exit 1; }
export MEDIK8S_REGISTRY_NAME=${MEDIK8S_REGISTRY_NAME:-kind-registry}
export MEDIK8S_REGISTRY_PORT=${MEDIK8S_REGISTRY_PORT:-5000}
export DEV_REGISTRY=${DEV_REGISTRY:-registry}
export DEV_OLM_OPERATOR_SDK=${DEV_OLM_OPERATOR_SDK:-"$ROOT/bin/dev-olm/operator-sdk"}
export PATH="${DEV_OLM_OPERATOR_SDK%/*}:$PATH"
export MANIFESTS_DIR=${MANIFESTS_DIR:-config/kind-e2e}
export MDR_CRD_DIR=${MDR_CRD_DIR:-"$ROOT/vendor/github.com/openshift/api/machine/v1beta1/zz_generated.crd-manifests"}
export E2E_KIND_OUTPUT_DIR=${E2E_KIND_OUTPUT_DIR:-}
case ${1:-all} in
    test|collect|teardown)
        [[ -n "$E2E_KIND_OUTPUT_DIR" ]] || { echo "Set E2E_KIND_OUTPUT_DIR to the directory printed by setup." >&2; exit 1; }
        ;;
esac
if [[ -n "$E2E_KIND_OUTPUT_DIR" ]]; then
    [[ "$E2E_KIND_OUTPUT_DIR" == /* ]] || { echo "Output directory must be absolute" >&2; exit 1; }
    export KUBECONFIG="$E2E_KIND_OUTPUT_DIR/kubeconfig"
fi
export KUBECTL=${KUBECTL:-kubectl}
watcher_pid=""
recorder_pid=""
output_created=false

stop_children() {
    for pid in "$watcher_pid" "$recorder_pid"; do
        if [[ -n "$pid" ]]; then
            kill "$pid" 2>/dev/null || true
            if [[ "$pid" == "$watcher_pid" ]]; then
                kill -- "-$pid" 2>/dev/null || true
            fi
            wait "$pid" 2>/dev/null || true
        fi
    done
}
trap stop_children EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

setup() {
    if [[ -e "$E2E_KIND_OUTPUT_DIR" ]]; then
        echo "Output directory already exists: $E2E_KIND_OUTPUT_DIR" >&2
        echo "Set E2E_KIND_OUTPUT_DIR to a new absolute path for this run." >&2
        return 1
    fi
    for tool in "$CONTAINER_TOOL" "$KUBECTL" kind python3 go; do command -v "$tool" >/dev/null; done
    make dev-olm-operator-sdk
    if [[ -z "$E2E_KIND_OUTPUT_DIR" ]]; then
        mkdir -p "$ROOT/.tests"
        E2E_KIND_OUTPUT_DIR=$(mktemp -d "$ROOT/.tests/kind-e2e.XXXXXX")
    else
        mkdir -p "$E2E_KIND_OUTPUT_DIR"
    fi
    output_created=true
    export KUBECONFIG="$E2E_KIND_OUTPUT_DIR/kubeconfig"
    echo "E2E_KIND_OUTPUT_DIR=$E2E_KIND_OUTPUT_DIR"
    local clusters
    clusters=$(kind get clusters)
    if grep -Fxq "$MEDIK8S_CLUSTER_NAME" <<< "$clusters"; then
        kind get kubeconfig --name "$MEDIK8S_CLUSTER_NAME" > "$KUBECONFIG"
    else
        printf '%s\n' "$MEDIK8S_CLUSTER_NAME" > "$E2E_KIND_OUTPUT_DIR/owned-cluster"
    fi
    if ! "$CONTAINER_TOOL" inspect "$MEDIK8S_REGISTRY_NAME" >/dev/null 2>&1; then
        printf '%s\n' "$MEDIK8S_REGISTRY_NAME" > "$E2E_KIND_OUTPUT_DIR/owned-registry"
    fi
    SETUP_MDR_MOCK=true make dev-setup 2>&1 | tee "$E2E_KIND_OUTPUT_DIR/setup.log"
    build_deploy
}

build_deploy() {
    make dev-olm-deploy OPERATOR_SDK="$DEV_OLM_OPERATOR_SDK"
    make dev-wait
}

test_e2e() {
    [[ -f "$KUBECONFIG" ]] || { echo "Run setup first" >&2; return 1; }
    export E2E_KIND=true
    "$CONTAINER_TOOL" ps -a --no-trunc --filter "label=io.x-k8s.kind.cluster=$MEDIK8S_CLUSTER_NAME" \
        > "$E2E_KIND_OUTPUT_DIR/containers-before.txt"
    python3 -c 'import os, sys; os.setsid(); os.execv(sys.argv[1], sys.argv[1:])' \
        "$TOOLS_DIR_RESOLVED/dev/kind-reboot-watcher.sh" --mode mdr --once \
        --name "$MEDIK8S_CLUSTER_NAME" \
        > "$E2E_KIND_OUTPUT_DIR/watcher.log" 2>&1 &
    watcher_pid=$!
    "$KUBECTL" get machinedeletionremediations -n "$OPERATOR_NS" --watch --output-watch-events -o json \
        > "$E2E_KIND_OUTPUT_DIR/mdr-events.json" 2>&1 &
    recorder_pid=$!
    local result=0
    make e2e-test TEST_OPS="${TEST_OPS:-} -ginkgo.junit-report=$E2E_KIND_OUTPUT_DIR/junit.xml" \
        2>&1 | tee "$E2E_KIND_OUTPUT_DIR/e2e.log" || result=$?
    if [[ "$result" == 0 ]] && ! kill -0 "$watcher_pid" 2>/dev/null; then
        wait "$watcher_pid" || result=$?
    fi
    cat "$E2E_KIND_OUTPUT_DIR/watcher.log"
    return "$result"
}

collect() {
    [[ -d "$E2E_KIND_OUTPUT_DIR" ]] || return 0
    make dev-ci-debug > "$E2E_KIND_OUTPUT_DIR/debug.log" 2>&1 || true
    local resource
    for resource in machines.machine.openshift.io machinesets.machine.openshift.io machinedeletionremediations nodes pods events csv subscriptions installplans catalogsources operatorgroups; do
        "$KUBECTL" --request-timeout=15s get "$resource" -A -o yaml > "$E2E_KIND_OUTPUT_DIR/$resource.yaml" 2>&1 || true
    done
    "$KUBECTL" --request-timeout=15s logs -n "$OPERATOR_NS" -l control-plane=controller-manager --all-containers --tail=1000 \
        > "$E2E_KIND_OUTPUT_DIR/operator.log" 2>&1 || true
    "$KUBECTL" --request-timeout=15s logs -n "$OPERATOR_NS" -l control-plane=controller-manager --all-containers --previous --tail=1000 \
        > "$E2E_KIND_OUTPUT_DIR/operator-previous.log" 2>&1 || true
    "$KUBECTL" --request-timeout=15s logs -n kind-mdr-machines replacement-network > "$E2E_KIND_OUTPUT_DIR/network.log" 2>&1 || true
    "$CONTAINER_TOOL" ps -a --no-trunc --filter "label=io.x-k8s.kind.cluster=$MEDIK8S_CLUSTER_NAME" \
        > "$E2E_KIND_OUTPUT_DIR/containers.txt" 2>&1 || true
    local container_id
    while IFS= read -r container_id; do
        [[ -n "$container_id" ]] || continue
        "$CONTAINER_TOOL" inspect "$container_id" > "$E2E_KIND_OUTPUT_DIR/$container_id-inspect.json" 2>&1 || true
    done < <("$CONTAINER_TOOL" ps -aq --no-trunc --filter "label=io.x-k8s.kind.cluster=$MEDIK8S_CLUSTER_NAME")
}

teardown() {
    [[ -f "$E2E_KIND_OUTPUT_DIR/owned-cluster" ]] || return 0
    [[ "$(cat "$E2E_KIND_OUTPUT_DIR/owned-cluster")" == "$MEDIK8S_CLUSTER_NAME" ]] || return 1
    local keep_registry=true
    if [[ -f "$E2E_KIND_OUTPUT_DIR/owned-registry" ]] && \
        [[ "$(cat "$E2E_KIND_OUTPUT_DIR/owned-registry")" == "$MEDIK8S_REGISTRY_NAME" ]]; then
        keep_registry=false
    fi
    KEEP_REGISTRY="$keep_registry" make dev-teardown
}

case ${1:-all} in
    setup) setup ;;
    test) test_e2e ;;
    collect) collect ;;
    teardown) teardown ;;
    all)
        trap 'stop_children; if [[ "$output_created" == true ]]; then collect; teardown; fi' EXIT
        setup
        test_e2e
        ;;
    *) echo "Usage: $0 [all|setup|test|collect|teardown]" >&2; exit 1 ;;
esac
