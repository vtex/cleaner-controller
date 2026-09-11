# Architecture Diagram (C4 / LikeC4)

A [C4 model](https://c4model.com/) of **cleaner-controller** — the Kubernetes
operator that manages resource lifecycle via `ConditionalTTL` CRs and, when
enabled, idle Knative Service cleanup. Written in [LikeC4](https://likec4.dev/)
DSL (`model.c4`, `views.c4`).

## Views

| View | What it shows |
|---|---|
| **`index`** | System context: both cleanup mechanisms and external dependencies |
| **`whatGetsDeleted`** | Side-by-side comparison of what each mechanism removes |
| **`conditionalTTLComponents`** | ConditionalTTL reconciler: CEL, target/release/cloud-event finalizers |
| **`idleCleanupComponents`** | Idle cleanup: detector, `idle-since` annotator, deleter |
| **`idleTimerReset`** | Sequence: scale-up at 3h **resets** the clock; deletion only after 12h of a **new** continuous idle period |
| **`idleContinuousDeletion`** | Sequence: uninterrupted 12h at zero → delete |

## Viewing it

From the repo root:

```bash
make diagram    # http://localhost:5173
```

Or directly:

```bash
cd docs/architecture/likec4
npm install
npx likec4 start
```

Or open the `.c4` files with the
[LikeC4 VS Code extension](https://marketplace.visualstudio.com/items?itemName=likec4.likec4-vscode).

To export static images:

```bash
npx playwright install   # one-time, needed for image export
npx likec4 export png -o images
```

## Idle timer FAQ

**Q: Service scaled to zero, stayed idle 3h, then scaled to 1. Is it deleted after 12h from the first zero?**

**No.** Scale-up clears `cleaner.vtex.io/idle-since`. Deletion only happens after
`IDLE_KNATIVE_CLEANUP_THRESHOLD` (default `12h`) of **continuous** idle time
starting from the **latest** moment all Deployments returned to
`spec.replicas=0` and `status.replicas=0`.

## Keeping this up to date

Update `model.c4` / `views.c4` in the same PR as architectural changes to the
controllers or cleanup semantics.
