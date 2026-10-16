# Machine Deletion Remediation (MDR) — Overview

## Purpose

**Machine Deletion Remediation (MDR)** remediates an unhealthy node on **OpenShift** clusters that use the **Machine API** by **deleting the Machine** object that owns the node. The **Machine** controller (and **MachineSet** / **ControlPlaneMachineSet**) is expected to **replace** the machine so capacity returns.

MDR is a **remediator**: **Node Health Check (NHC)** or **Machine Health Check (MHC)** (or an admin) creates a **`MachineDeletionRemediation`**; MDR **deletes the Machine** and **reports status** on that object.

## How it works

1. A **`MachineDeletionRemediation`** exists in a **namespace** (often created by **NHC** with name equal to the **Node** name, or by **MHC** with an **ownerReference** to the **Machine**).
2. MDR **resolves** the target **Machine** (saved annotation, **Machine** owner on the CR, or **`machine.openshift.io/machine`** on the **Node** named like the CR).
3. MDR sets **PermanentNodeDeletionExpected** from **`Machine.spec.providerID`** (baremetal prefix vs other vs missing) to document whether the **node name** is expected to change after replacement.
4. MDR **persists** the Machine’s **namespace/name** and **owner kind/name** on the CR, then **deletes** the **Machine**.
5. MDR waits until the **Machine** is gone (or a **newer** Machine exists) and **`spec.replicas`** on the owning **MachineSet** or **ControlPlaneMachineSet** matches the **count of nodes** whose Machines chain to that owner—then it marks **Succeeded**. If **replicas** is **0**, it skips the count check.
6. **NHC** can stop driving the story via **`remediation.medik8s.io/nhc-timed-out`** (only while the CR is **not** deleting).

## Important details

- API group **`machine-deletion-remediation.medik8s.io`**, **`v1alpha1`**. Resources are **namespaced**; short names **`mdr`**, **`mdrt`**.
- **`MachineDeletionRemediation.spec`** is **empty** (placeholder). **`MachineDeletionRemediationTemplate`** wraps the same empty **spec** for **NHC**; the MDR operator **does not** reconcile templates.
- The **Machine** must have a **controller** **ownerReference** (`controller: true`); otherwise remediation **cannot start**.
- Requires **OpenShift Machine API** types (**`machine.openshift.io`**) and **Nodes**; see **architecture** for RBAC.

## Related pieces

| Piece | Role |
|--------|------|
| **NHC** | Creates **MDR** CRs (name = node); may set **NHC timeout** annotation; removes CR when healthy. |
| **MHC** | May create **MDR** with **Machine** **ownerReference**. |
| **Machine / MachineSet / CPMS** | Machine deletion triggers replacement; replica vs node count gates **Succeeded**. |

## What this file is not

This overview does **not** describe reconcile ordering, annotation formats, or replica counting. Those belong in **architecture**, **failure_modes**, and **runbook**.
