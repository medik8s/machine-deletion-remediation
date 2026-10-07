# Machine Deletion Remediation (MDR) — Code map

**Repository:** `github.com/medik8s/machine-deletion-remediation`

## Purpose

A **reader’s map** of the MDR codebase: where main ideas live so you can jump from verified memory into the right files. **Not** a line-by-line tour.

## Top-level layout

```
machine-deletion-remediation/
├── cmd/
│   └── main.go                              # Manager: scheme, metrics, probes, leader election, reconciler only
├── api/v1alpha1/
│   ├── machinedeletionremediation_types.go           # MDR CR, empty spec, status conditions doc
│   ├── machinedeletionremediationtemplate_types.go   # MDRT, nested empty spec
│   ├── groupversion_info.go, zz_generated.deepcopy.go
├── internal/
│   └── controller/
│       ├── machinedeletionremediation_controller.go      # All remediation logic
│       └── machinedeletionremediation_controller_test.go
├── version/                             # Build metadata (logged at startup)
├── config/                              # CRD bases, kustomize, bundle wiring
├── bundle/                              # OLM bundle
└── vendor/                              # Third-party (incl. medik8s/common, openshift/api)
```

There is **no** separate top-level **`pkg/`** tree for application runtime logic — code lives under **`internal/controller/`** and **`api/`** (plus **`vendor/`** for third-party packages whose import paths may still contain **`pkg/`**).

## Where to look for what

| Question | Start here |
|----------|------------|
| Full **Reconcile** flow, **Machine** resolution, delete, replica gate | `internal/controller/machinedeletionremediation_controller.go` |
| **MDR** / **MDRT** API types, short names **`mdr`** / **`mdrt`** | `api/v1alpha1/machinedeletionremediation_types.go`, `machinedeletionremediationtemplate_types.go` |
| **PermanentNodeDeletionExpected** reason constants | `api/v1alpha1/machinedeletionremediation_types.go` |
| Manager flags, **LeaderElectionID**, metrics **HTTP/2** disable | `cmd/main.go` |
| Version strings at startup | `version/version.go` |
| **NHC timeout** annotation key | `vendor/github.com/medik8s/common/pkg/annotations/annotations.go` (`NhcTimedOut`) |

## RBAC

Kubebuilder **+kubebuilder:rbac** markers on **`MachineDeletionRemediationReconciler.Reconcile`** in **`machinedeletionremediation_controller.go`** — see **`architecture.md`** summary.

## Tests

**`*_test.go`** in **`internal/controller/`** covers timeout, owner paths, restoration logic, etc.; for **runtime** behaviour start with **`machinedeletionremediation_controller.go`**.

## API short names (CLI)

| Resource | Short name |
|----------|------------|
| `MachineDeletionRemediation` | `mdr` |
| `MachineDeletionRemediationTemplate` | `mdrt` |

## Related pieces

- **`architecture.md`** — runtime behaviour.
- **`runbook.md`** — operational commands.

## Scope

This map does **not** document every **bundle** manifest or **scorecard** file under **`config/`** / **`bundle/`**.
