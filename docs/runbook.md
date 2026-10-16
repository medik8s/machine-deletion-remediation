# Machine Deletion Remediation (MDR) — Operational runbook

## Purpose

Practical checks for **Machine Deletion Remediation**: install sanity, active remediation, and stuck cases. **`overview.md`**, **`architecture.md`**, and **`failure_modes.md`** explain behaviour; this page focuses on **commands** and **fields**.

## Components

| Piece | Role |
|-------|------|
| **Operator deployment** | Reconciles **`MachineDeletionRemediation`** only (no template controller, no webhooks). |
| **CRDs** | **`machine-deletion-remediation.medik8s.io/v1alpha1`** — **`mdr`**, **`mdrt`**. |

**MDR** and **MDRT** CRs are **namespaced**. **Machines** / **MachineSets** / **ControlPlaneMachineSets** live in their API namespaces (commonly **`openshift-machine-api`** on OpenShift).

## Operator process (observability & HA)

| Topic | Note |
|-------|------|
| **Metrics** | **`cmd/main.go`** default **`:8443`**, secured (TLS + bearer-token authn/authz). |
| **Health / readiness** | **`cmd/main.go`** default **`:8081`**. |
| **HTTP/2** | Disabled on metrics TLS config hook (future-proofing). |
| **Leader election** | Flag **`--leader-elect`**; **`LeaderElectionID`** in **`cmd/main.go`** may differ from **CSV** — compare live **Deployment** args. |
| **Replicas** | Unlike some medik8s operators, **HA** is deployment-defined; if multiple pods run **without** leader election, behaviour may duplicate work—prefer matching **CSV** guidance. |

## Prerequisites

| Requirement | Note |
|-------------|------|
| **OpenShift Machine API** | **Machine**, **MachineSet**, **ControlPlaneMachineSet** available and RBAC granted to the operator. |
| **Nodes** | Listed/watched for replica verification. |
| **NHC / MHC** | If used, templates reference **`MachineDeletionRemediationTemplate`**; NHC creates **`MachineDeletionRemediation`**. |

## Quick health checks

Replace **`<ns>`** with the namespace where **MDR** objects (and often the operator) live.

```bash
kubectl get mdr -A
kubectl get mdrt -A
kubectl describe mdr -n <ns> <name>
kubectl get machines.machine.openshift.io -A
kubectl get machinesets.machine.openshift.io -A
kubectl get controlplanemachinesets.machine.openshift.io -A 2>/dev/null || true
```

On OpenShift, **`oc`** is equivalent.

## What to inspect on **`MachineDeletionRemediation`**

| Area | What to look for |
|------|------------------|
| **`.metadata.name`** | Usually matches **Node** name (NHC pattern). |
| **`.metadata.ownerReferences`** | **MHC** may set **Machine** owner. |
| **Annotations** | **`machine-deletion-remediation.medik8s.io/machineNameNamespace`** — `namespace/name` of target **Machine** (set before delete). |
| **Annotations** | **`machine-deletion-remediation.medik8s.io/machineOwner`** — `MachineSet/name` or `ControlPlaneMachineSet/name`. |
| **Annotations** | **`remediation.medik8s.io/nhc-timed-out`** — NHC asked to **stop** remediation (no delete while present, per controller). |
| **Conditions** | **Processing**, **Succeeded**, **PermanentNodeDeletionExpected** (types from **medik8s/common** / MDR reasons). |
| **Events** | **RemediationStarted**, **RemediationFinished**, **GetTargetNodeFailed**, **RemediationStoppedByNHC**, warnings for cannot-start reasons. |

## What to inspect on the **Node**

| Annotation | Meaning |
|------------|---------|
| **`machine.openshift.io/machine`** | `namespace/name` of the **Machine** — used when MDR resolves via **Node** named like the CR. |

```bash
kubectl get node <node-name> -o jsonpath='{.metadata.annotations.machine\.openshift\.io/machine}{"\n"}'
```

## What to inspect on **`MachineDeletionRemediationTemplate`**

| Area | Note |
|------|------|
| **`spec.template.spec`** | **Empty** — placeholder for NHC; **no** operator reconciliation. |
| **Existence** | Required for NHC **remediation template** references; debugging is mostly **NHC** + **MDR** CRs. |

## Common condition outcomes (operations)

| Reason (examples) | Typical meaning |
|--------------------|-----------------|
| **RemediationStarted** | **Processing** path entered. |
| **MachineDeleted** | **Succeeded** — replica / restoration gate passed. |
| **RemediationStoppedByNHC** | **nhc-timed-out** annotation seen. |
| **RemediationCannotStartNodeNotFound** | No **Node** **`mdr.Name`**. |
| **RemediationCannotStartMachineNotFound** | Machine missing on first discovery. |
| **RemediationCannotStartNoControllerOwner** | **Machine** lacks **controller** owner ref. |
| **RemediationFailed** | Unrecoverable annotation/owner/kind issues. |

## **`PermanentNodeDeletionExpected`** (ops meaning)

| Status | Typical interpretation |
|--------|-------------------------|
| **True** | **`providerID`** does **not** look like baremetal — **new node name** expected after replacement. |
| **False** | **`baremetal…`** prefix — **same node name** expectation. |
| **Unknown** | Missing/empty **`providerID`**. |

## Debugging checklist

1. **Node** name matches **MDR** name (NHC path)?  
2. **`machine.openshift.io/machine`** present and valid on that node?  
3. **Machine** exists, has **controller** owner, **`deletionTimestamp`** progressing if delete issued?  
4. **MachineSet** / **CPMS** **`spec.replicas`** vs healthy **Machines** / **Nodes** for that owner?  
5. **`nhc-timed-out`** present — intentional?  
6. Operator logs at **DEBUG** / **INFO** for **requeue** reasons (**30s** vs **1s**).

## Related pieces

- **`code_map.md`** — upstream file paths.
- **`failure_modes.md`** — expanded symptom list.
