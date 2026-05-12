# Cleaner Controller Constitution

> For project structure, CRD schema, reconciler flow, and development commands
> see [`AGENTS.md`](../../AGENTS.md) at the repo root.

**Version**: 1.0.0 | **Ratified**: 2026-05-12

---

## I. Correctness Over Cleverness

This operator deletes production Kubernetes resources. A subtle bug can cause premature or missed deletions with irreversible consequences. Prefer explicit, readable code over concise-but-opaque code. Every reconcile path must have a clear, tested outcome.

## II. Respect the Finalizer Contract

Finalizers MUST be added only after the TTL has expired **and** all CEL conditions are met. Adding them at creation time would cause premature deletion when a user manually removes a `ConditionalTTL` before its TTL fires. This constraint is load-bearing — do not change it without a migration plan.

Finalizers are processed **one per reconcile cycle** (not batched). This prevents double-execution when the reconciler restarts mid-teardown. Do not collapse the finalizer loop.

## III. Trust the Framework

`controller-runtime` handles retries, caching, event watches, and leader election. Do not reimplement these primitives. Follow controller-runtime idioms: return `ctrl.Result{RequeueAfter: d}` to schedule, return `ctrl.Result{}` with `nil` error when done, return a non-nil error only when the platform should back-off-retry.

## IV. CEL Safety

Distinguish error classes — this shapes retry decisions:

| Error class | Retryable |
|---|---|
| Environment / compile error | No |
| Condition result is not boolean | No |
| Runtime evaluation error | Yes |
| Condition evaluates to `false` | Yes |

Never collapse these into a single catch-all. The `custom_cel.EvaluateCELConditions` signature (`conditionsMet bool, retryable bool`) must stay intact.

The variable name `time` is reserved in every CEL context. The kubebuilder regex marker on `Target.Name` enforces this. Do not weaken or remove that marker.

## V. Tests Hit a Real API Server

Controller-layer tests use kubebuilder **envtest** — a real `kube-apiserver` and `etcd` binary, not mocks. Do not introduce mock-based tests for the controller or CEL packages. Mock-only tests have historically masked real API divergence.

## VI. Minimal Footprint

- RBAC markers (`//+kubebuilder:rbac:`) must list only verbs the controller actually uses.
- Helm driver is hard-coded to `"secret"`. Do not change it without a config-migration plan.
- Do not add feature flags or backward-compatibility shims — change the code directly.
- Do not design for hypothetical future requirements (YAGNI).

## VII. Authoring New Features

1. Update `api/v1alpha1/conditionalttl_types.go`
2. Run `make generate manifests`
3. Implement changes in `controllers/` or `custom_cel/`
4. Add or update Ginkgo tests in `controllers/suite_test.go`
5. Run `make gen-docs` to refresh `docs/api-reference.md`
6. Validate locally with `make run` against a real cluster or envtest

## VIII. Governance

This constitution supersedes all other development guidelines. Amendments require:
- A clear rationale tied to a real constraint or incident
- An update to this document's **Version** and **Last Amended** date
- A note in the PR description explaining the change

All AI-assisted suggestions must be checked against these principles before acceptance.
