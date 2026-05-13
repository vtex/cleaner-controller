# AGENTS.md — cleaner-controller

## Project Overview

cleaner-controller is a Kubernetes operator (controller-runtime / kubebuilder) that manages resource lifecycle via the `ConditionalTTL` CRD. A `ConditionalTTL` waits for a TTL to expire, evaluates optional CEL conditions, then deletes targets, uninstalls a Helm release, and/or sends a CloudEvent.

Tech stack: Go 1.22, controller-runtime v0.19.0, kubebuilder envtest, Ginkgo v2 + Gomega.  
Module: `github.com/vtex/cleaner-controller` | Image: `public.ecr.aws/f8y0w2c4/cleaner-controller`

## Prerequisites

- kubeconfig context only for `make run` / `make install` — tests need no cluster
- envtest assets are downloaded automatically on first `make test`

### Build & Run

```bash
make build              # Compile
make run                # Run against current kubeconfig context
make install            # Install CRDs into cluster
make generate manifests # Regenerate after editing api/v1alpha1/ types — commit generated files in the same PR
make helm               # Regenerate Helm chart
make gen-docs           # Regenerate docs/api-reference.md
make fmt vet            # Format + lint — required before committing
```

### Test Commands

```bash
make test      # All tests (envtest assets downloaded automatically)
make coverage  # Open HTML coverage report
```

### Architecture Boundaries

| Layer | Path | Responsibility |
|---|---|---|
| Entry point | `main.go` | Manager bootstrap, flag/env parsing — no business logic |
| Controllers | `controllers/` | Reconcile loop, finalizer orchestration |
| CEL | `custom_cel/` | Condition compilation + evaluation — no Kubernetes client calls |
| API types | `api/v1alpha1/` | CRD structs, kubebuilder markers — pure types, no I/O |
| Config | `config/` | Generated CRD/RBAC manifests — do not hand-edit (except `config/default/` overlays) |

Rule: `custom_cel/` MUST NOT import `controllers/`. `api/v1alpha1/` MUST NOT import `controllers/` or `custom_cel/`.

### Coding Conventions

- Every I/O function must accept `context.Context` as first argument and honor cancellation.
- Wrap errors with `fmt.Errorf("...: %w", err)` — never log and return `nil`.
- Structured logging via `log.FromContext(ctx)`; every reconcile log line must include resource name/namespace as key-value pairs.
- Use `Eventually` (Gomega) for async assertions — `time.Sleep` is forbidden as a correctness gate.
- Every new exported function in `custom_cel/` must have a unit test.
- Controller-layer tests must use kubebuilder envtest (real API server) — mock-based controller tests are forbidden.

### Reconciler Key Facts

- Finalizer order: `target-finalizer` → `release-finalizer` → `cloud-event-finalizer`; one per reconcile cycle — do not batch.
- Finalizers are added only after TTL expires **and** all CEL conditions are met — never at creation.
- CEL retryability: compile error → `(false, false)`; runtime error or unmet condition → `(false, true)`.
- `time` is a reserved CEL variable (always injected as current timestamp). Target names must not use it.
- `r.List` returns an error if a continuation token appears — do not silently drop it when adding new list calls.

### Safety Guardrails

- NEVER commit secrets, credentials, or `.env` files.
- NEVER hand-edit `config/crd/` or `config/rbac/` — run `make generate manifests` instead.
- NEVER bypass `make test` — envtest catches mock/real divergence that unit mocks miss.
- Do not batch finalizer handling; sequential processing prevents double-execution.
- Do not modify `.specify/memory/constitution.md` without an explicit PR review by a maintainer in CODEOWNERS.
