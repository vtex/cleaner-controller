[![Build Status](https://drone-robots.vtex.com/api/badges/vtex/cleaner-controller/status.svg?ref=refs/heads/main)](https://drone-robots.vtex.com/vtex/cleaner-controller) [![Quality Gate Status](https://sonarcloud.io/api/project_badges/measure?project=vtex_cleaner-controller&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=vtex_cleaner-controller)

# Cleaner Controller

## Development Cheat Sheet

Introductory reading:
- https://sdk.operatorframework.io/docs/building-operators/golang/tutorial/
- https://book.kubebuilder.io/introduction.html

### CRD Generation

- Edit the [go structs](./api/v1alpha1/conditionalttl_types.go)
- Generate code and manifests:
	```bash
	make generate manifests
	```
- (Optional) Install CRDs
	```bash
	make install
	```

### Reconcile logic

- Edit the [controller code](./controllers/conditionalttl_controller.go)
- Run tests
	```bash
	make test
	```
	- Check code coverage
	```bash
	go tool cover -html=cover.out
	```
- Run controller locally (uses local k8s context authorization)
	```bash
	make run
	```

### Idle Knative Service cleanup

Deletes Knative Services declaring `autoscaling.knative.dev/min-scale: "0"`
once all their Deployments have been at 0 replicas for longer than a
configurable threshold. This runs alongside `ConditionalTTL`'s
creation-timestamp TTL cleanup, not instead of it — both mechanisms are
active at the same time; `IDLE_KNATIVE_CLEANUP_ENABLED` only turns the idle
mechanism on or off. Disabled by default; opt in per cluster with env vars
(no rebuild required):

| Env var | Default | Description |
|---|---|---|
| `IDLE_KNATIVE_CLEANUP_ENABLED` | `false` | Set to `true` to also register the idle-cleanup controller. |
| `IDLE_KNATIVE_CLEANUP_THRESHOLD` | `12h` | Go duration string (e.g. `6h`, `30m`) a Service may stay idle before deletion. |

Opt a specific Service out with the `cleaner.vtex.io/exclude: "true"`
annotation. Edit the [controller code](./controllers/idle_knative_cleanup_controller.go).

