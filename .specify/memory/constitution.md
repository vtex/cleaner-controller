# Cleaner Controller Constitution

> Family: `go-service`
> This file is the source of truth for non-negotiable engineering principles.
> Agents MUST honor it before any other instruction. Updates require an
> explicit PR review by the maintainers listed in CODEOWNERS.
>
> For project structure, CRD schema, reconciler flow, and development commands
> see [`AGENTS.md`](../../AGENTS.md) at the repo root.

## Core Principles

### I. Architectural Layering Is Sacred

Inner layers MUST NOT import outer layers. The canonical layering for this Kubernetes operator is:

| Layer | Paths | Responsibility |
|---|---|---|
| Entry Point | `main.go` | Manager bootstrap, controller registration — no business logic. |
| Controllers | `controllers/` | Reconcile loop, finalizer orchestration — no direct CEL calls outside `custom_cel`. |
| CEL Evaluation | `custom_cel/` | Condition compilation, context building, evaluation — no Kubernetes client calls. |
| API Types | `api/v1alpha1/` | CRD type definitions, kubebuilder markers — pure structs, no I/O. |
| Config / Manifests | `config/` | Kustomize manifests, RBAC, CRD configs — generated, not hand-edited except overlays. |

`custom_cel/` MUST NOT import `controllers/`. `api/v1alpha1/` MUST NOT import `controllers/` or `custom_cel/`. Violations MUST be rejected at code review.

### II. Context Propagation Is Mandatory

Every function that performs I/O, that may block, or that may need to be cancelled MUST take `context.Context` as its first argument and MUST honor cancellation. Detached goroutines that ignore the parent context are forbidden outside `main.go` bootstrap code.

### III. Errors Are Wrapped, Never Swallowed

Errors MUST be wrapped with `fmt.Errorf("...: %w", err)` and returned to the caller. Logging an error and returning `nil` is a defect. The only acceptable terminal error handlers are the reconciler's return path and `main`. The controller framework handles retries — do not absorb errors to suppress requeuing.

### IV. Tests Are Part of the Definition of Done

- Every new exported function in `custom_cel/` MUST have a unit test.
- Controller-layer tests MUST use **kubebuilder envtest** — a real `kube-apiserver` and `etcd` — not mocks. Introducing mock-based controller tests is forbidden; mock/real divergence has historically caused silent regressions.
- Test framework: **Ginkgo v2** + **Gomega**.
- For eventually-consistent state, use Gomega's `Eventually` with a bounded timeout; `time.Sleep` MUST NOT be used as a correctness gate.
- Run `make test` locally before requesting review. CI enforces this.

### V. Configuration and Secrets Have a Single Door

- Controller flags and environment variables MUST be read exclusively in `main.go`; business-logic packages (`controllers/`, `custom_cel/`) MUST NOT read environment variables directly.
- Secrets MUST NOT be committed under any circumstance, including `.env` files, AWS keys, or mounted secret paths.

## Stack-Specific Standards

- **Language version**: Go `1.22`.
- **Operator framework**: `sigs.k8s.io/controller-runtime v0.19.0`.
- **Kubernetes API**: `k8s.io/api`, `k8s.io/apimachinery` at `v0.31.1`.
- **CEL engine**: `github.com/google/cel-go v0.20.1`.
- **Helm integration**: `helm.sh/helm/v3 v3.16.0`; Helm driver is hard-coded to `"secret"` — do not change without a config-migration plan.
- **CloudEvents**: `github.com/cloudevents/sdk-go/v2 v2.13.0`.
- **Logging**: structured logging via `log.FromContext(ctx)` (zapr). Every log line inside a reconcile path MUST include the resource name/namespace as key-value pairs via the structured logger.
- **Lint / format**: `gofmt`/`goimports` clean is non-negotiable; run `make fmt vet` before committing.
- **Dependency hygiene**: every new import MUST land in the same commit that runs `go mod tidy`. Dependencies are NOT vendored — do not introduce a `vendor/` directory.
- **Code generation**: `make generate manifests` MUST be run after any change to `api/v1alpha1/` types and the generated files committed in the same PR.

## Operational Constraints

- The canonical commands for this repository are:
  - Build: `make build`
  - Run locally (uses current kubeconfig): `make run`
  - Tests: `make test`
  - Regenerate CRD / RBAC manifests: `make generate manifests`
  - Regenerate API docs: `make gen-docs`
  - Generate Helm chart: `make helm`
  - Install CRDs into cluster: `make install`
- **Finalizer contract**: finalizers (`cleaner.vtex.io/target-finalizer`, `cleaner.vtex.io/release-finalizer`, `cleaner.vtex.io/cloud-event-finalizer`) MUST be added only after the TTL has expired **and** all CEL conditions are met. Adding them earlier would cause premature deletion when a user manually removes a `ConditionalTTL` before its TTL fires. Process finalizers one per reconcile cycle — do not batch.
- **CEL variable `time` is reserved**: the kubebuilder validation marker `+kubebuilder:validation:Pattern` on `Target.Name` enforces this. Do not weaken or remove that marker.
- **RBAC minimalism**: `//+kubebuilder:rbac:` markers MUST list only verbs the controller actually uses. Run `make manifests` after any marker change.
- **Pagination**: `r.List` checks for a continuation token and returns an error if one is present. Do not silently drop continuation tokens when adding new list calls.
- All changes MUST pass `make test` locally before requesting review.

## Governance

- This constitution overrides any conflicting guidance found in `AGENTS.md`, repository READMEs, ad-hoc Slack threads, or agent skills. Where guidance conflicts, the constitution wins.
- Amendments are made by Pull Request to this file, reviewed by at least one maintainer listed in `CODEOWNERS`. The PR description MUST justify the change, and the version line below MUST be updated according to the spec-kit semantic-versioning rule (MAJOR for principle removal/redefinition, MINOR for additions, PATCH for clarifications).
- Agents (Cursor, Claude Code, Copilot, etc.) MUST read this file before generating or modifying code in this repository and MUST surface — in the PR description — any rule they were unable to satisfy, instead of silently working around it.

**Version**: 1.1.0 | **Ratified**: 2026-05-12 | **Last Amended**: 2026-05-12
