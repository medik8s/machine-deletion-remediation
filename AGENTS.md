# AGENTS.md — Machine Deletion Remediation Operator

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

## Repository layout

```
api/v1alpha1/           CRD types: MachineDeletionRemediation, MachineDeletionRemediationTemplate
internal/controller/    MDR reconciler
cmd/                    Operator main package
e2e/                    Ginkgo e2e suite
hack/                   Dev scripts
config/                 Kustomize bases (operator, rbac, bundle)
bundle/                 Generated OLM bundle (manifests, metadata, tests)
version/                Operator version package
```

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

## Local development & deployment

Deploying to a dev cluster is standardized across all medik8s operators via the
shared dev environment in [`medik8s/tools`](https://github.com/medik8s/tools)
(`dev/dev.mk`). The Makefile pulls these targets in automatically: it uses a
sibling `../tools` checkout if present, otherwise shallow-clones the repo into
`.tools/` on first `make dev-*` use.

```bash
make dev-setup       # Create a Kind cluster (1 control-plane + 3 workers) with deps
make dev-deploy      # Build image, load it, install CRDs, deploy the operator
make dev-describe    # Summarize nodes, pods, CRs, leases, and events
make dev-redeploy    # Rebuild and restart pods (fast iteration)
make dev-undeploy    # Remove the operator
make dev-teardown    # Destroy the Kind cluster
make dev-help        # List all dev-* targets
```

Deploy to an existing cluster (OCP, etc.) with `SKIP_KIND=true`; images are
pushed to the ephemeral `ttl.sh` registry:

```bash
export KUBECONFIG=~/.kube/my-cluster
SKIP_KIND=true make dev-setup dev-deploy
```

In Kind there is no Machine API, so reconciliation is exercised via envtest
(`make test`); for the full delete-and-reprovision flow use an OpenShift /
Machine-API cluster. See
[`dev/README.md`](https://github.com/medik8s/tools/blob/main/dev/README.md) in
`medik8s/tools` for prerequisites, all targets, and per-operator coverage.

## Code style

- Go, Kubebuilder v4, controller-runtime; follows standard medik8s patterns.
- Imports must be sorted (`make fix-imports`).
- No direct commits to `main`; open a PR.

## Key design constraints

- **Machine API is required**: MDR will not work on plain Kubernetes clusters without machine-api-operator. Do not add fallback paths.
- The node must have a Machine annotation pointing to its backing Machine object. If the annotation is absent, MDR cannot remediate.
- The Machine must have an owning controller (e.g. MachineSet). MDR will not delete a standalone Machine without an owner — deleting it would leave no controller to reprovision.
- MDR provides no power fencing guarantee — the old node may still run until the cloud provider terminates it. For workloads requiring hard fencing, use FAR or SNR instead.

## Security

- Operator requires RBAC to read Nodes/Machines and delete Machine objects.
- No privileged pods; runs with standard controller-manager permissions.
- Never widen RBAC beyond the generated `config/rbac/` manifests without review.

## Keeping the docs current

If your changes affect anything described here — build commands, repo layout, remediation flow, Machine API assumptions — or any other existing documentation (`README.md`, `CONTRIBUTING.md`, anything under `docs/`, inline command or usage references), update all of it so the docs never drift from the code.

## Commit conventions

- One-line commit message; sign off with `-s`.
- Reference the relevant issue or PR number when applicable.
- Use WIP in title when creating draft PRs to save CI resources.
