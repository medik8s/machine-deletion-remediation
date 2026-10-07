# AGENTS.md — Machine Deletion Remediation Operator

> **IMPORTANT — read this first.** Before making any changes in this repository, you MUST
> read the medik8s **common agent guide**, the **OFFICIAL guidance** for all medik8s
> operators: **https://github.com/medik8s/.github/blob/main/AGENTS.md** . It is
> authoritative project guidance and must not be ignored.

## Medik8s context

MDR is a remediation **provider** in the [medik8s](https://medik8s.io) family. The orchestrator is **Node Healthcheck Operator (NHC)**: it detects unhealthy nodes and creates a `MachineDeletionRemediation` CR; MDR then deletes the backing Machine so its owning controller (e.g. MachineSet) reprovisions a replacement node. NHC deletes the CR when the node is healthy again.

MDR requires **Machine API** — it is only applicable on clusters where nodes are backed by Machine objects (primarily OpenShift with machine-api-operator).

## What MDR does

When a `MachineDeletionRemediation` CR is created for a node:
1. Follows the node's annotation to the associated `Machine` object.
2. Verifies the Machine has an owning controller (e.g. MachineSet).
3. Deletes the Machine CR.
4. The owning controller provisions a replacement Machine/Node automatically.

MDR does not power-fence or reboot the node directly — it relies on the cloud/infrastructure provider's Machine lifecycle to destroy and recreate the node.

## Build & test

```bash
# Unit tests (also runs go-verify, manifests, generate, fmt, vet, imports)
make test

# Build operator binary
make manager

# Build container image
make docker-build

# Regenerate CRDs + RBAC after API changes
make manifests generate

# Format + imports
make fmt vet
make fix-imports

# Verify no uncommitted changes (required before PR)
make verify-no-changes

# End-to-end tests (requires a Machine-API cluster, e.g. OpenShift)
make test-e2e
```

> `make test` includes `verify-no-changes` — run it before opening a PR.

## Local development & testing

Follow the shared workflow in the
[common agent guide](https://github.com/medik8s/.github/blob/main/AGENTS.md) — it documents
the standardized `dev-*` make targets (`dev-setup`, `dev-deploy`, `dev-redeploy`,
`dev-undeploy`, `dev-describe`, `dev-help`, …) provided by `medik8s/tools` (`dev/dev.mk`).

**To develop and test against a real OpenShift / Kubernetes cluster**:
`export SKIP_KIND=true` before the `dev-*` targets; images are pushed to `ttl.sh`.

Github CI workflow runs on a Kind cluster. MDR requires the **Machine API**, which Kind
does not provide — on Kind only envtest-level reconciliation (`make test`) and a mocked
Machine-replacement e2e are possible. The real delete-and-reprovision flow needs an
OpenShift / Machine-API cluster.

## Key design constraints

- **Machine API is required**: MDR will not work on plain Kubernetes clusters without machine-api-operator. Do not add fallback paths.
- The node must have a Machine annotation pointing to its backing Machine object. If the annotation is absent, MDR cannot remediate.
- The Machine must have an owning controller (e.g. MachineSet). MDR will not delete a standalone Machine without an owner — deleting it would leave no controller to reprovision.
- MDR provides no power fencing guarantee — the old node may still run until the cloud provider terminates it. For workloads requiring hard fencing, use FAR or SNR instead.

## Security

- Operator requires RBAC to read Nodes/Machines and delete Machine objects.
- No privileged pods; runs with standard controller-manager permissions.
