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
CI uses Kind v0.33.0; the simulator follows that version's worker provisioning.
Set `TOOLS_DIR` to use a specific shared-tools checkout. Podman is the default;
use `CONTAINER_TOOL=docker` for Docker. Each prepared fixture supports one MDR
replacement. The standard operator image is built
for Linux amd64; running locally on arm64 requires amd64 emulation in the runtime.
The Kind overlay keeps the operator's non-root security settings and uses a
256 MiB memory limit to allow emulated execution on arm64 hosts.

Like SBR's local runner, this runner uses a local `../tools` checkout (including
uncommitted changes), or lets `make dev-setup` download tools into `.tools` when
no local checkout exists. It starts the shared watcher directly from the resolved
tools directory, saves its PID, and stops that process on exit. To run:

```bash
CONTAINER_TOOL=podman bash hack/local-run.sh all
```

The runner follows the shared tools defaults: cluster `medik8s-dev`, registry
`kind-registry` on host port 5000, and the default three-worker configuration.
For OLM, it uses operator namespace `openshift-workload-availability`; artifacts
go to a unique output directory `.tests/kind-e2e.XXXXXX`, printed by setup.
Override these with `MEDIK8S_CLUSTER_NAME`,
`MEDIK8S_REGISTRY_NAME`, `MEDIK8S_REGISTRY_PORT`, `OPERATOR_NS`, and
`E2E_KIND_OUTPUT_DIR` (absolute path). It uses an isolated kubeconfig in the output
directory. Setup reuses an existing Kind cluster and registry. A fresh output
directory is created automatically for each run; an existing MDR fixture is rejected. The fixture
requires one Ready control plane and at least two Ready workers. Changing only
the output directory does not reset an already-used fixture; another MDR run
requires a fresh cluster. Reuse across other operators is supported.

If you explicitly set `E2E_KIND_OUTPUT_DIR`, setup requires that path to be new.
A rejected output directory is left untouched, and that invocation does not
collect diagnostics or tear down a cluster. If setup fails before creating the
cluster, collection skips Kind log export; check the first setup error.

The all-in-one command collects diagnostics and removes a cluster it created
on exit. It leaves a pre-existing cluster intact. To keep a newly created cluster
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
kubeconfig and an `owned-cluster` cleanup marker when setup creates the cluster;
these are not watcher coordination files. Generated images/bundles use the standard Dockerfile and an
isolated build directory; release bundle files are not regenerated.


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
`go.mod`. CI calls the same `setup`, `test`, `collect`, and `teardown` phases as local use.
It explicitly sets `E2E_KIND_OUTPUT_DIR` to `.tests/kind-e2e` for all phases.
Diagnostics collection and upload run even on failure, before cluster teardown.
The uploaded artifact excludes the kubeconfig, compiled test binary, and build
directory.

The [Kind overlay](../config/kind-e2e/kustomization.yaml) places MDR on the control
plane with its existing non-root security settings and a 256 MiB memory limit.
The runner generates and validates its Kind bundle under the output directory;
release artifacts remain separate. `make bundle` still defaults to
`MANIFESTS_DIR=config/manifests`, with `MANIFESTS_DIR` available to select an
overlay; use the runner for the isolated Kind output.

Outside Kind, `make test-e2e` retains its Machine API/Infrastructure lookup.
`OPERATOR_NS` can override the existing `openshift-operators` namespace default.
