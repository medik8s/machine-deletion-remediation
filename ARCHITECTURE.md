# Machine Deletion Remediation (MDR) — Architecture

## Purpose

This document explains **how the MDR operator runs**: **`MachineDeletionRemediation`** reconciliation, **Machine** resolution, **Machine** deletion, and the **replica / node-count** success gate. Read **`overview.md`** first for the product-level picture.

**Code reference:** `github.com/medik8s/machine-deletion-remediation` — primarily **`internal/controller/machinedeletionremediation_controller.go`**, **`api/v1alpha1/*`**, **`cmd/main.go`**.

## API scope

- **Group:** `machine-deletion-remediation.medik8s.io`, **`v1alpha1`**
- **Kinds:** **`MachineDeletionRemediation`** (short **`mdr`**), **`MachineDeletionRemediationTemplate`** (short **`mdrt`**)
- **Scope:** **Namespaced**
- **`MachineDeletionRemediation.spec`:** **empty** struct (no user-facing fields today)
- **`MachineDeletionRemediationTemplate`:** **`spec.template.spec`** embeds the same empty **spec** for **NHC** template wiring; **no** template controller in this repository

## Operator startup (`cmd/main.go`)

The process builds a **controller-runtime** **Manager** with:

- **Scheme:** core Kubernetes + **`api/v1alpha1`** + **`machine.openshift.io`** **v1** and **v1beta1** (**Machine**, **MachineSet**, **ControlPlaneMachineSet**)
- **OpenShift TLS profile:** at startup, **`openshifttls.FetchAPIServerTLSProfile`** probes for the OpenShift `APIServer` config; if absent (non-OpenShift, e.g. `NoMatch`/`NotFound`), falls back to default Go TLS settings. When present, the profile's cipher/version settings feed the metrics server's TLS options, and a **`SecurityProfileWatcher`** runnable is added that **cancels the manager context** (process restart) if the profile changes at runtime.
- **Metrics:** bind **`:8443`**, **secured** (`SecureServing: true`, `FilterProvider: filters.WithAuthenticationAndAuthorization` — Kubernetes bearer-token authn/authz), same pattern used by sibling medik8s operators; **HTTP/2** is unconditionally disabled on the metrics server's TLS config (**CVE hardening**, hardcoded — no `--enable-http2` flag here)
- **Health / readiness:** **`:8081`** (**`healthz`**, **`readyz`** — ping checks)
- **Leader election:** **`--leader-elect`** flag (default **false** in code); **`LeaderElectionID`** is **`285d4098.example.com`** in **`cmd/main.go`** — **verify** the **ClusterServiceVersion** / deployment for what runs in production (FAR-style drift is possible)

Only **`MachineDeletionRemediationReconciler`** is registered. **No admission webhooks.**

## Reconciliation (`internal/controller/machinedeletionremediation_controller.go`)

### End-of-reconcile status

A **defer** calls **`Status().Update`**. On **conflict**, the reconciler sets **`RequeueAfter: 1s`** and does **not** aggregate a hard error for conflicts.

### NHC timeout (early exit)

If **`remediation.medik8s.io/nhc-timed-out`** is present on annotations and **`DeletionTimestamp` is nil**, MDR updates conditions to **RemediationStoppedByNHC** (reason **`RemediationStoppedByNHC`** in code; paired **Processing=false**, **Succeeded=false**), emits **RemediationStoppedByNHC**, and **returns** without deleting a Machine.

### Remediation started

**`updateConditions(remediationStarted)`** sets **Processing=true**, **Succeeded=Unknown**. If this **changed** status, MDR **requeues after 1s** and returns (so the next loop continues with updated status).

**Invariant:** once **Processing** is **False**, the controller **refuses** to set it back to **True** (`remediationStarted` becomes a no-op). So a timed-out or failed remediation does **not** “restart” processing on a later reconcile without CR churn.

### Resolve **Machine** (`getMachine`)

Order of resolution:

1. **`machine-deletion-remediation.medik8s.io/machineNameNamespace`** — value **`namespace/name`**. If the annotation **exists** but is **malformed** (not exactly two `/` segments), **`unrecoverableError`**.
2. If annotation **absent** or empty name: **ownerReference** with **`Kind: Machine`** → name + **MDR namespace**.
3. Else: load **Node** named **`mdr.Name`**; read **`machine.openshift.io/machine`** (`namespace/name`). Node **NotFound** → **`nodeNotFoundError`**. Bad/missing annotation on node → **`unrecoverableError`**.

Then **GET** the **Machine**. If **NotFound**:

- If the Machine identity came from “first discovery” (**not** yet saved in **`machineNameNamespace`**), **`machineNotFoundError`** (cannot start).
- If the identity was from the **saved** annotation, treat as **successful deletion path** → **`machine == nil`**.

### Post-resolve paths when **Machine** is nil or newer than MDR

If **`machine == nil`** **or** **`Machine.CreationTimestamp` is after the MDR**, MDR calls **`isExpectedNodesNumberRestored`**:

- Reads **`machineNameNamespace`** and **`machineOwner`** (`kind/name`) from annotations (both must be valid **`kind/name`** or **`namespace/name`** splits).
- Loads owner as **MachineSet** (**`machine.openshift.io/v1beta1`**) or **ControlPlaneMachineSet** (**`machine.openshift.io/v1`**). Unknown kind or owner **NotFound** / bad **`spec.replicas`** → **`unrecoverableError`** (terminal with **RemediationFailed** when surfaced from **`getMachine`** error path; from **`isExpectedNodesNumberRestored`** alone, see below).
- If **`replicas == 0`**, success **without** counting nodes.
- Else **lists all Nodes**, resolves each node’s Machine via **`machine.openshift.io/machine`**, walks Machine owner to match **owner name**, counts matches; **`len(nodes) == replicas`** → success.

If restoration is **not** yet true: **requeue after 30s**.

If **`isExpectedNodesNumberRestored`** fails: MDR emits **`unableToVerifyNodesCount`** and returns **`Reconcile` error**. In current code those errors are typically **`errors.Wrap(unrecoverableError, …)`**, so the **`err == unrecoverableError`** branch (which would return **nil** and stop retrying) is **not** taken — **controller-runtime keeps retrying** with backoff until the underlying problem is fixed or the CR changes. This path **does not** set **RemediationFailed** (unlike **`unrecoverableError`** returned **without** wrapping from **`getMachine`**, which updates **Failed**).

### Happy path with a live **Machine**

- Optionally logs if **Node** from **`machine.status.nodeRef`** is missing (remediation **continues**).
- **PermanentNodeDeletionExpected:** from **`spec.providerID`**: empty → **Unknown** + undefined reason; **`baremetal`…** prefix → **False**; else → **True**. If condition **changed**, **Normal** event and **requeue after 1s**.
- If **Machine** has **DeletionTimestamp**: log phase; **requeue after 30s**.
- If **no** **ownerReference** with **`controller: true`**: **cannot start** (warning + conditions).
- **`saveMachineData`**: set **`machineNameNamespace`**, **`machineOwner`** (`kind/name` from **sole** owner — **multiple** owners returns error from helper) via **`Update`** on the MDR CR.
- **`Delete(machine)`**; **RemediationStarted** event; **Requeue** immediate.

### Conditions and reasons

Uses **medik8s/common** condition types **Processing**, **Succeeded**, **PermanentNodeDeletionExpected**.

**Processing / Succeeded** reasons include **RemediationStarted**, **MachineDeleted**, **RemediationStoppedByNHC**, **RemediationCannotStartNodeNotFound**, **RemediationCannotStartMachineNotFound**, **RemediationCannotStartNoControllerOwner**, **RemediationFailed**.

**PermanentNodeDeletionExpected** reasons (from **`api/v1alpha1`**): **MachineDeletionOnCloudProviderCausesNewNodeName**, **MachineDeletionOnBareMetalProviderKeepsNodeName**, **MachineDeletionUndefinedNodeNameExpectation**.

## RBAC (kubebuilder markers on reconciler)

- **MDR** CRs: get/list/watch/create/update/patch/delete; status; finalizers (finalizers may be unused by this controller’s logic but are in RBAC)
- **machines:** get/list/watch/create/update/patch/**delete**
- **machinesets**, **controlplanemachinesets:** get/list/watch
- **nodes:** get/list/watch

## Related pieces

- **`overview.md`** — what MDR is for.
- **`failure_modes.md`** — symptoms.
- **`runbook.md`** — commands.
- **`code_map.md`** — file index.

## Scope

This document does **not** replace **NHC** / **MHC** documentation or OLM bundle specifics beyond **`cmd/main.go`** defaults.
