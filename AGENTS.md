# Cleaner Controller — Agent Context

## Project Overview

**cleaner-controller** is a Kubernetes operator (built with [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) / kubebuilder) that manages resource lifecycle through a custom `ConditionalTTL` CRD.

A `ConditionalTTL` (short name: `cttl`) declares:
- A **TTL** — wait time after creation before deletion is attempted
- A set of **Targets** — Kubernetes resources to delete or observe
- Optional **CEL conditions** that must all be true before deletion proceeds
- Optional **Helm release** to uninstall on deletion
- Optional **CloudEvent sink** to notify after deletion

Module: `github.com/vtex/cleaner-controller`  
Go version: `1.22`  
Image registry: `public.ecr.aws/f8y0w2c4/cleaner-controller`

---

## Repository Layout

```
api/v1alpha1/               CRD types (ConditionalTTL, TargetReference, HelmConfig, RetryConfig)
controllers/                Reconciler (ConditionalTTLReconciler) + suite_test.go
custom_cel/                 CEL helpers — BuildCELOptions, BuildCELContext, EvaluateCELConditions
config/
  crd/                      Generated CRD manifests
  rbac/                     RBAC roles and bindings
  manager/                  Controller Deployment manifest
  default/                  Kustomize overlay for full deploy
  samples/                  Example ConditionalTTL resources
docs/api-reference.md       Auto-generated API reference (make gen-docs)
main.go                     Controller entry point
Makefile                    Build, test, and deploy automation
```

---

## Core CRD: ConditionalTTL

Group/Version: `cleaner.vtex.io/v1alpha1`  
Kind: `ConditionalTTL` (short name: `cttl`)

### Spec fields

| Field | Type | Description |
|---|---|---|
| `ttl` | duration string | Wait after creation before deletion begins |
| `retry.period` | duration string | How often to re-evaluate CEL conditions |
| `targets[]` | Target | Resources to delete or observe |
| `conditions[]` | string | CEL expressions — all must evaluate to `true` |
| `helm.release` | string | Helm release name to uninstall |
| `helm.delete` | bool | Whether to uninstall the Helm release |
| `cloudEventSink` | string URL | HTTP(S) endpoint to receive a CloudEvent after deletion |

### Target fields

| Field | Description |
|---|---|
| `name` | Identifier used as CEL variable name. `time` is reserved and invalid. |
| `delete` | If true, the resource is deleted when the TTL fires |
| `includeWhenEvaluating` | If true, the resource's state is available in CEL as `name` |
| `reference.apiVersion` + `reference.kind` | GVK to look up |
| `reference.name` | Targets a single resource by name |
| `reference.labelSelector` | Targets a collection of resources |

The built-in CEL variable `time` (timestamp) is always injected and holds the current evaluation time.

---

## Reconciler Flow

File: `controllers/conditionalttl_controller.go`

1. Fetch `ConditionalTTL`. If not found → ignore.
2. **If being deleted** (DeletionTimestamp set): run finalizers **one per reconcile cycle**:
   - `cleaner.vtex.io/target-finalizer` — deletes targets with `delete: true`
   - `cleaner.vtex.io/release-finalizer` — uninstalls Helm release if configured
   - `cleaner.vtex.io/cloud-event-finalizer` — sends CloudEvent to configured sink
3. Check if TTL has expired. If not → requeue after remaining duration, set `Ready=Unknown`.
4. Resolve all targets from the cluster.
5. Evaluate CEL conditions:
   - Compile errors / type errors → non-retryable, set `Ready=False`
   - Runtime errors → retryable, requeue after `retry.period`
   - Conditions not met → retryable, requeue after `retry.period`
6. Conditions met → snapshot target states into `status.targets`, add all three finalizers, then self-delete the `ConditionalTTL`.

**Key invariant**: Finalizers are added only when deletion actually fires, not at creation. This prevents premature target deletion if a user manually deletes the `ConditionalTTL` before its TTL expires.

**Key invariant**: Finalizers are processed one at a time (one per reconcile cycle) to avoid double-execution. Do not collapse finalizer handling into a single loop.

---

## CEL Evaluation

Package: `custom_cel/`

- `BuildCELOptions(cTTL)` — builds the CEL env with `ext.Strings()`, `ext.Bindings()`, custom `Lists()` extension, `time` variable, and one variable per target with `includeWhenEvaluating: true`
- `BuildCELContext(targets, time)` — maps target names → their unstructured state + `"time"` → current time
- `EvaluateCELConditions(opts, ctx, conditions, readyCondition)` — compiles and evaluates all conditions in order; returns `(conditionsMet bool, retryable bool)`

Retryability rules:
- Compile error → `(false, false)`
- Runtime eval error → `(false, true)`
- Condition evaluates to `false` → `(false, true)`
- All true → `(true, false)`

---

## Development Commands

```bash
# After editing api/v1alpha1/ types:
make generate manifests

# Run tests (downloads kubebuilder envtest assets automatically):
make test

# Open HTML coverage report:
make coverage

# Run controller locally against current kubeconfig context:
make run

# Install CRDs into cluster:
make install

# Generate Helm chart:
make helm

# Regenerate docs/api-reference.md:
make gen-docs
```

---

## Testing

- Framework: **Ginkgo v2** + **Gomega** + **kubebuilder envtest**
- Tests run against a real API server (no mocks for the controller layer)
- Test entry point: `controllers/suite_test.go`
- Coverage output: `cover.out`

---

## CI/CD

- CI: Drone (`drone-robots.vtex.com`)
- GitHub Actions: `.github/workflows/publish.yaml` (image publish)
- Image: `public.ecr.aws/f8y0w2c4/cleaner-controller:<version>`

---

## Known TODOs (from code comments)

- Admission webhook to enforce `retry != nil` when `conditions` is non-empty
- Helm driver (`"secret"`) should be configurable
- Admission webhook to enforce `name != "time"` on targets (currently a kubebuilder regex marker)
- Pagination: list operations check for a continuation token but do not page — an error is returned if one appears
