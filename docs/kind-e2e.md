# Kind E2E with simulated Machine replacement

This workflow tests the standard MDR operator through OLM on a disposable Kind
cluster, which can be shared with other operators. A host-side Machine API
simulator deletes the victim's node container and Kubernetes Node, then
provisions and joins a fresh worker container. It verifies MDR's actual
Machine deletion request and status transitions. It does not validate cloud
provisioning, physical fencing, or NHC integration. The separate system-tests
suite is outside this workflow.

## Run locally

Requirements: Podman or Docker, Kind, kubectl, Python 3, and Go matching `go.mod`.
CI uses the runner's preinstalled Kind. The simulator follows Kind v0.33.0's
worker provisioning, which the tools Machine API smoke workflow checks.
Set `TOOLS_DIR` to use a specific shared-tools checkout. Podman is the default;
use `CONTAINER_TOOL=docker` for Docker. Each prepared fixture supports one MDR
replacement. The standard operator image is built
for Linux amd64; running locally on arm64 requires amd64 emulation in the runtime.
The Kind overlay keeps the operator's non-root security settings and uses a
256 MiB memory limit to allow emulated execution on arm64 hosts.

Like SBR's local runner, this runner uses a local `../tools` checkout (including
uncommitted changes), or lets the shared dev targets download tools into `.tools`
when no local checkout exists. It uses the same setup, OLM deployment, readiness,
test, and teardown targets as CI. Both start the watcher script directly in the
background, check its status if it has already exited, and stop it and its child
processes when tests finish. No separate test compilation is needed.
`make e2e-test` is available for running only the Go tests against an already
prepared cluster. To run:

```bash
CONTAINER_TOOL=podman bash hack/local-run.sh all
```

The runner follows the shared tools defaults: cluster `medik8s-dev`, registry
`kind-registry` on host port 5000, and the default three-worker configuration.
For OLM, it uses operator namespace `openshift-workload-availability`; artifacts
go to a unique output directory `.tests/kind-e2e.XXXXXX`, printed by setup.
Override these with `MEDIK8S_CLUSTER_NAME`,
`MEDIK8S_REGISTRY_NAME`, `MEDIK8S_REGISTRY_PORT`, `OPERATOR_NAMESPACE` (or `OPERATOR_NS`), and
`E2E_KIND_OUTPUT_DIR` (absolute path). It uses an isolated kubeconfig in the output
directory. If both namespace variables are set, they must match. Setup reuses an
existing Kind cluster and registry. A fresh output
directory is created automatically for each run; an existing MDR fixture is rejected. The fixture
requires one Ready control plane and at least two Ready workers. Changing only
the output directory does not reset an already-used fixture; another MDR run
requires a fresh cluster. Reuse across other operators is supported.

If you explicitly set `E2E_KIND_OUTPUT_DIR`, setup requires that path to be new.
A rejected output directory is left untouched, and that invocation does not
collect diagnostics or tear down a cluster. If setup fails before creating the
cluster, collection skips Kind log export; check the first setup error.

The all-in-one command collects diagnostics and uses `make dev-teardown` to
remove a cluster it created on exit. It preserves a pre-existing registry with
`KEEP_REGISTRY=true` and leaves a pre-existing cluster intact. To keep a newly created cluster
for other operators, run the phases separately and omit teardown. Keep the same
environment overrides for every phase, including an explicit output directory:

```bash
export E2E_KIND_OUTPUT_DIR="$PWD/.tests/my-kind-run"
bash hack/local-run.sh setup
bash hack/local-run.sh test
bash hack/local-run.sh collect
```

If setup chose the directory automatically, export the printed
`E2E_KIND_OUTPUT_DIR` before running any later phase.
Run `bash hack/local-run.sh teardown` when a cluster created by setup is no longer
needed. Teardown leaves a pre-existing cluster intact.

Inspect the printed output directory before teardown. It contains watcher/test
logs, JUnit, fixture identities in the watcher log, network-probe logs,
Machines/MachineSet/MDR/Node/event and OLM dumps, operator logs, container
listings and inspections. It also holds the isolated
kubeconfig and ownership markers for a cluster or registry created by setup;
these are not watcher coordination files. Generated images and bundles use the
same build targets and checkout files as CI.


## Lifecycle and assertions

Setup uses the shared Kind configuration: one control plane and three active
workers. Each worker is linked to an OpenShift Machine owned by a MachineSet
whose replica count matches the workers present.
The vendored Machine and MachineSet CRDs are installed without their controllers.
No spare container, Node, or bootstrap state is reserved.

The existing E2E creates an MDR directly, without stopping kubelet. The simulator
only acts after MDR requests deletion of a fixture Machine. Its finalizer keeps
the Machine present until the exact recorded container and Node have been removed.
The Node delete includes a UID precondition; only the simulator's finalizer is removed.

The watcher then creates a differently named Machine and a fresh container from
the cluster's Kind node image, with new storage. It generates a short-lived
kubeadm token on the control plane and joins the worker to the existing cluster.
It waits for Ready and kindnet configuration, verifies pod DNS/API connectivity,
and publishes the Machine annotation and worker label together. These steps run
independently of the test; there is no replacement-release file or test handshake.
The provisioning follows Kind's container and kubeadm setup for the pinned Kind
version, using the default IPv4 Kind network.

The E2E assertions are unchanged: Machine deletion, intermediate Processing and
PermanentNodeDeletionExpected conditions, a new Node created after the MDR, a
new Machine UID and creation time, and final Processing=False/Succeeded=True.
The only suite adaptations are `OPERATOR_NS` and bypassing the OpenShift
Infrastructure lookup when `E2E_KIND=true`. The `kind://` provider ID exercises
the existing non-BareMetal assertion.

## CI and deployment

[The GitHub workflow](../.github/workflows/kind-e2e.yaml) runs for pull requests,
main/release branch pushes, and manual dispatch on Ubuntu 24.04. Go comes from
`go.mod`. CI and the local runner invoke the same Make targets:

1. `make dev-olm-operator-sdk` installs the SDK used for OLM setup and deployment.
2. `SETUP_MDR_MOCK=true make dev-setup` creates the cluster, registry, and mock
   Machine API fixtures, then installs cert-manager and OLM. `MDR_CRD_DIR` points
   to this repo's vendored Machine API CRDs. Setup calls `kind_mdr.py prepare`,
   which applies both CRDs and waits for them to be established before creating
   the MachineSet and Machine fixtures.
3. `make dev-olm-deploy` builds and deploys MDR through the shared OLM targets.
4. `make dev-wait` checks deployment readiness.
5. Both start `kind-reboot-watcher.sh --mode mdr --once` directly in the
   background in a separate process group before invoking `make e2e-test`
   with `E2E_KIND=true`. The watcher waits indefinitely for a deletion request;
   replacement still has a fifteen-minute deadline. When tests finish, the caller
   checks the status of an already-exited watcher and stops any remaining
   watcher and child processes. `e2e-test` only runs Go tests; `TEST_OPS`
   accepts additional Go test or Ginkgo options.
6. `make dev-ci-debug` and resource/log dumps collect diagnostics, then
   `make dev-teardown` cleans up. CI uploads diagnostics before teardown;
   local `all` keeps them in the output directory and checks resource ownership
   before teardown.

CI sets `E2E_KIND_OUTPUT_DIR` to `.tests/kind-e2e` and keeps its kubeconfig there.
Diagnostics collection and upload run even on failure, before cleanup. The
uploaded artifact excludes the kubeconfig.
`OPERATOR_NAMESPACE` selects the OLM namespace; `OPERATOR_NS` selects the suite's
namespace. Both use `openshift-workload-availability` in CI.

The [Kind overlay](../config/kind-e2e/kustomization.yaml) places MDR on the control
plane with its existing non-root security settings and a 256 MiB memory limit.
Both paths build and push the operator and bundle using their Kind registry tags.
They select the overlay with
`MANIFESTS_DIR=config/kind-e2e`; the shared OLM build invokes
MDR's normal bundle targets. This regenerates the checkout's bundle files.
`make bundle` still defaults to `MANIFESTS_DIR=config/manifests`.

Outside Kind, `make e2e-test` retains its Machine API/Infrastructure lookup.
`OPERATOR_NS` can override the existing `openshift-operators` namespace default.
