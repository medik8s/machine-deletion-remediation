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
export OPERATOR_NS=${OPERATOR_NS:-openshift-workload-availability}
export MEDIK8S_REGISTRY_NAME=${MEDIK8S_REGISTRY_NAME:-kind-registry}
export MEDIK8S_REGISTRY_PORT=${MEDIK8S_REGISTRY_PORT:-5000}
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
test_pid=""
recorder_pid=""
output_created=false

stop_children() {
    for pid in "$test_pid" "$watcher_pid" "$recorder_pid"; do
        if [[ -n "$pid" ]]; then
            kill "$pid" 2>/dev/null || true
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
    make operator-sdk kustomize
    local sdk_version
    sdk_version=$(make -s --no-print-directory kind-e2e-sdk-version)
    export PATH="$ROOT/bin/operator-sdk/$sdk_version:$PATH"
    if [[ -z "$E2E_KIND_OUTPUT_DIR" ]]; then
        mkdir -p "$ROOT/.tests"
        E2E_KIND_OUTPUT_DIR=$(mktemp -d "$ROOT/.tests/kind-e2e.XXXXXX")
    else
        mkdir -p "$E2E_KIND_OUTPUT_DIR"
    fi
    output_created=true
    export KUBECONFIG="$E2E_KIND_OUTPUT_DIR/kubeconfig"
    echo "E2E_KIND_OUTPUT_DIR=$E2E_KIND_OUTPUT_DIR"
    if kind get clusters | grep -Fxq "$MEDIK8S_CLUSTER_NAME"; then
        kind get kubeconfig --name "$MEDIK8S_CLUSTER_NAME" > "$KUBECONFIG"
    else
        printf '%s\n' "$MEDIK8S_CLUSTER_NAME" > "$E2E_KIND_OUTPUT_DIR/owned-cluster"
    fi
    make dev-setup
    python3 "$TOOLS_DIR_RESOLVED/dev/kind_mdr.py" prepare --name "$MEDIK8S_CLUSTER_NAME" \
        --crd-dir "$ROOT/vendor/github.com/openshift/api/machine/v1beta1/zz_generated.crd-manifests"
    build_deploy
}

build_deploy() {
    make operator-sdk kustomize
    local sdk kustomize build image bundle tag registry
    sdk="$ROOT/bin/operator-sdk/$(make -s --no-print-directory kind-e2e-sdk-version)/operator-sdk"
    kustomize="$ROOT/bin/kustomize"
    build="$E2E_KIND_OUTPUT_DIR/build"
    tag=kind-e2e
    image="localhost/machine-deletion-remediation:$tag"
    bundle="localhost/machine-deletion-remediation-bundle:$tag"
    registry="${MEDIK8S_REGISTRY_NAME}:${MEDIK8S_REGISTRY_PORT}/medik8s"
    local push_command=("$CONTAINER_TOOL" push)
    if [[ "$KIND_EXPERIMENTAL_PROVIDER" == podman ]]; then
        push_command+=(--tls-verify=false)
    fi
    "$CONTAINER_TOOL" build --platform linux/amd64 -t "$image" .
    "$CONTAINER_TOOL" tag "$image" "$registry/machine-deletion-remediation:$tag"
    "${push_command[@]}" "$registry/machine-deletion-remediation:$tag"
    mkdir -p "$build"
    cp -R config "$build/config"
    cp PROJECT "$build/PROJECT"
    (
        cd "$build/config/manager"
        "$kustomize" edit set image "controller=$registry/machine-deletion-remediation:$tag"
    )
    python3 - "$build" "$registry/machine-deletion-remediation:$tag" <<'PY'
import base64
from pathlib import Path
import sys
root = Path(sys.argv[1])
csv = root / 'config/manifests/bases/machine-deletion-remediation.clusterserviceversion.yaml'
value = csv.read_text().replace('containerImage: ""', 'containerImage: ' + sys.argv[2])
value = value.replace('base64EncodedIcon', base64.b64encode((root / 'config/assets/medik8s_blue_icon.png').read_bytes()).decode())
csv.write_text(value)
PY
    (
        cd "$build"
        "$kustomize" build config/kind-e2e | "$sdk" generate bundle -q --overwrite \
            --output-dir bundle --version 0.0.1 --channels stable --default-channel stable
        "$sdk" bundle validate ./bundle --select-optional suite=operatorframework
        "$CONTAINER_TOOL" build -f bundle.Dockerfile -t "$bundle" .
    )
    "$CONTAINER_TOOL" tag "$bundle" "$registry/machine-deletion-remediation-bundle:$tag"
    "${push_command[@]}" "$registry/machine-deletion-remediation-bundle:$tag"
    "$KUBECTL" create namespace "$OPERATOR_NS" --dry-run=client -o yaml | "$KUBECTL" apply -f -
    "$sdk" run bundle -n "$OPERATOR_NS" --use-http --timeout 5m \
        "$registry/machine-deletion-remediation-bundle:$tag"
    "$KUBECTL" rollout status -n "$OPERATOR_NS" deployment/machine-deletion-remediation-controller-manager --timeout=180s
}

test_e2e() {
    [[ -f "$KUBECONFIG" ]] || { echo "Run setup first" >&2; return 1; }
    export E2E_KIND=true
    "$CONTAINER_TOOL" ps -a --no-trunc --filter "label=io.x-k8s.kind.cluster=$MEDIK8S_CLUSTER_NAME" \
        > "$E2E_KIND_OUTPUT_DIR/containers-before.txt"
    # Compile before starting the watcher's five-minute deletion-request deadline.
    go test -c ./e2e -o "$E2E_KIND_OUTPUT_DIR/mdr-e2e.test"
    "$TOOLS_DIR_RESOLVED/dev/kind-reboot-watcher.sh" --mode mdr --once \
        --name "$MEDIK8S_CLUSTER_NAME" \
        > "$E2E_KIND_OUTPUT_DIR/watcher.log" 2>&1 &
    watcher_pid=$!
    "$KUBECTL" get machinedeletionremediations -n "$OPERATOR_NS" --watch --output-watch-events -o json \
        > "$E2E_KIND_OUTPUT_DIR/mdr-events.json" 2>&1 &
    recorder_pid=$!
    "$E2E_KIND_OUTPUT_DIR/mdr-e2e.test" -test.v -test.timeout=25m -ginkgo.vv \
        -ginkgo.junit-report="$E2E_KIND_OUTPUT_DIR/junit.xml" \
        > "$E2E_KIND_OUTPUT_DIR/e2e.log" 2>&1 &
    test_pid=$!
    local result=0
    while kill -0 "$test_pid" 2>/dev/null; do
        if [[ -n "$watcher_pid" ]] && ! kill -0 "$watcher_pid" 2>/dev/null; then
            wait "$watcher_pid" || result=$?
            watcher_pid=""
            if [[ "$result" != 0 ]]; then
                cat "$E2E_KIND_OUTPUT_DIR/watcher.log"
                return "$result"
            fi
        fi
        sleep 2
    done
    wait "$test_pid" || result=$?
    test_pid=""
    cat "$E2E_KIND_OUTPUT_DIR/e2e.log"
    [[ "$result" == 0 ]] || return "$result"
    if [[ -n "$watcher_pid" ]]; then
        wait "$watcher_pid" || result=$?
        watcher_pid=""
    fi
    cat "$E2E_KIND_OUTPUT_DIR/watcher.log"
    return "$result"
}

collect() {
    [[ -d "$E2E_KIND_OUTPUT_DIR" ]] || return 0
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
    kind delete cluster --name "$MEDIK8S_CLUSTER_NAME"
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
