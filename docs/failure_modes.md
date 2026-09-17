# Machine Deletion Remediation (MDR) — Failure modes

## Purpose

**Symptom-first** guide for MDR: what you might see when something goes wrong, and what the controller does. Use with **`architecture.md`** and **`runbook.md`**.

Behaviour references **`internal/controller/machinedeletionremediation_controller.go`**, **`api/v1alpha1/machinedeletionremediation_types.go`**, and **medik8s/common** annotations/conditions unless noted.

## Governing constants (selected)

| Name | Value / meaning |
|------|------------------|
| **NHC timeout annotation** | `remediation.medik8s.io/nhc-timed-out` (only honored when CR **`deletionTimestamp` is nil**) |
| **Saved Machine pointer** | `machine-deletion-remediation.medik8s.io/machineNameNamespace` — `namespace/name` |
| **Saved Machine owner** | `machine-deletion-remediation.medik8s.io/machineOwner` — `Kind/name` (e.g. `MachineSet/my-ms`) |
| **Node → Machine** | `machine.openshift.io/machine` — `namespace/name` |
| **Requeue (typical)** | **1s** after status transitions / conflicts; **30s** while waiting for Machine deletion or node-count restoration |

---

## 1. Target **Node** not found

**Symptom:** Conditions **RemediationCannotStartNodeNotFound**; event **GetTargetNodeFailed** (medik8s common).

**Behaviour:** When the CR does not yet have **`machineNameNamespace`**, MDR loads a **Node** named **`mdr.Name`**. **NotFound** maps to this terminal path.

**Ops:** Align CR **name** with **`kubectl get node`**; confirm NHC created the CR for the intended node.

---

## 2. **Machine** not found on first discovery

**Symptom:** **RemediationCannotStartMachineNotFound**; warning event with **`could not get node's machine`**.

**Behaviour:** Node annotation pointed at a Machine name/namespace, or owner reference named a Machine, but **GET** returned **NotFound** while the CR still had **no** saved **`machineNameNamespace`**.

**Ops:** Check **Machine** existence in the **openshift-machine-api** (or your) namespace; fix **MHC** / manual CR wiring.

---

## 3. Bad **`machineNameNamespace`** annotation

**Symptom:** **RemediationFailed**; warning **unrecoverable error**; logs about annotation parse.

**Behaviour:** If **`machineNameNamespace`** is present but **not** exactly **`namespace/name`** (one slash, two segments), **`getMachine`** returns **`unrecoverableError`**.

**Ops:** Fix or remove the annotation (careful: operator may repopulate on next successful path); prefer deleting/recreating the CR if the object is corrupted.

---

## 4. Node missing **`machine.openshift.io/machine`** or malformed value

**Symptom:** **RemediationFailed** (unrecoverable).

**Behaviour:** Node exists but annotation missing, empty, or not **`ns/name`**.

**Ops:** This is a **cluster/node registration** problem for Machine API workflows—not an MDR bug. Fix Machine ↔ Node linking.

---

## 5. **Machine** has no **controller** owner

**Symptom:** **RemediationCannotStartNoControllerOwner**; warning with **`ignoring remediation of the machine: the machine has no controller owner`**.

**Behaviour:** **`hasControllerOwner`** requires an **ownerReference** with **`controller: true`**.

**Ops:** Investigate why the **Machine** has no controller (unusual for normal MachineSet/CPMS-managed machines).

---

## 6. **Machine** has **more than one** owner reference

**Symptom:** Reconcile errors when saving Machine data; logs **`machine has more than one owner`**.

**Behaviour:** **`getMachineOwnerNameKind`** rejects multiple owners; **`saveMachineData`** fails **before** delete.

**Ops:** Resolve **Machine** metadata / ownership to a single owner (operational data issue).

---

## 7. **Machine** owner is not **MachineSet** or **ControlPlaneMachineSet**

**Symptom:** Warning **`unableToVerifyNodesCount`**; remediation does **not** reach **Succeeded**; operator logs show unknown owner **kind**; reconciles **keep repeating**.

**Behaviour:** **`getMachineOwner`** only maps those two kinds; otherwise it returns **`errors.Wrap(unrecoverableError, …)`**. The reconcile loop returns that **error** (wrapped value is **not** pointer-equal to the naked **`unrecoverableError`** sentinel), so **controller-runtime retries** with backoff. **RemediationFailed** is **not** set on this path.

**Ops:** MDR’s success gate is designed for **MachineSet** / **CPMS** replicas. Fix owner/kind or replace the remediation approach for unsupported ownership.

---

## 8. Owner or **`spec.replicas`** missing / not found

**Symptom:** Similar to §7 — **`unableToVerifyNodesCount`**; no **Succeeded**; **retries**.

**Behaviour:** Owner **NotFound** or unreadable **`spec.replicas`** → wrapped **`unrecoverableError`** from the restoration path; same **retry** behaviour as §7 (no **RemediationFailed** condition update).

**Ops:** Confirm **MachineSet** / **CPMS** exists and has **`spec.replicas`**.

---

## 9. Node count never matches **replicas**

**Symptom:** Logs **waiting for the nodes count to be re-provisioned**; slow **30s** requeues; **Succeeded** not reached.

**Behaviour:** After Machine is gone/newer, MDR compares **replica** count to **nodes** that still chain to the saved **owner name** via **Machine** objects. **Capacity** or **Machine** provisioning issues prevent convergence.

**Ops:** Inspect **MachineSet** / **CPMS**, pending **Machines**, cloud / bare-metal provider errors; not an MDR-specific fix beyond ensuring the cluster replaces nodes.

---

## 10. **NHC** timeout

**Symptom:** **`nhc-timed-out`** annotation; **RemediationStoppedByNHC**; **Processing=false**, **Succeeded=false**.

**Behaviour:** Checked **before** **`remediationStarted`** advancement in a given reconcile; no **Machine** delete driven by this pass.

**Ops:** NHC policy / timeout tuning; removing the annotation alone does **not** flip **Processing** back to **true** (see **architecture** invariant).

---

## 11. **API errors** deleting **Machine** or updating **MDR**

**Symptom:** Reconcile errors in operator logs; **Machine** still exists.

**Behaviour:** **`Delete(machine)`** failure returns error (retry). **Status** conflict → defer sets **1s** requeue.

**Ops:** RBAC, admission, or API availability — **`kubectl auth can-i delete machines`** as the operator SA, etc.

---

## 12. **Succeeded** “too early” confusion

**Symptom:** **Succeeded** while the unhealthy **node** still exists (bare metal).

**Behaviour:** **Baremetal** **`providerID`** sets **PermanentNodeDeletionExpected=False** (node name **not** expected to change) while **cloud** expects a **new** node name. **Succeeded** still means **replica count** was restored per **Machine** graph, not “the original Node object disappeared.”

**Ops:** Read **PermanentNodeDeletionExpected** and provider behaviour; use **Node** / **Machine** state outside MDR for infra truth.

---

## Related pieces

- **`architecture.md`** — full ordering.
- **`runbook.md`** — inspection commands.
