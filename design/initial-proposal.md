# DX Cleaner Controller

## Problem

With the generalization of `proxy-launcher`'s API, its usefulness has increased and new resources are soon to be created by it. However, all of the resources planned to be released through its API are ephemeral in nature (e.g: jamstack `knative-services` and tekton `pipeline-run`). Currently there are specialized _cleaners_ and _curators_ to deal with these resources. Evidently, most of the logic is repeated across these implementations and each new type of resource requires a brand new implementation. In order to facilitate this ordeal and ensure the timely deletion of ephemeral resources created through `proxy-launcher` API, we propose the creation of a _cleaner_ kubernetes controller.

## API Proposal

```yaml
apiVersion: cleaner.vtex.dev/v1alpha
kind: Cleaner
metadata:
  name: sfj-commit--account
  namespace: faststore-proxy
spec:
  ttl: 360h # supports up to hours (time.Duration limitation)
  retry:
    # other than updates to targets, how often the condition should be evaluated
    period: 5h
    # -- possible optimization --
    # if the user asserts the condition is monotonic on `time`
    # the controller can binary search when in the future the condition becomes
    # `true` and only retry then. If no time is found, fallbacks to periodic retries
    monotonic: true
  helm:
    # support multiple releases?
    # otherwise we need (and already want) to turn faststore releases into a single one
    release: sfj-commit--account
    delete: true
  # objects to be deleted / watched to decide when to actuate the deletion
  targets:
  # nginx and nodejs targets aren't necessary for faststore use case
  # they are included here as an example
  - name: nginx
    reference:
      apiGroup: serving.knative.dev
      version: v1
      kind: service
      name: sfj-commit--account
    delete: false # will be deleted with helm release
    includeWhenEvaluating: false
  - name: nodejs
    reference:
      apiGroup: serving.knative.dev
      version: v1
      kind: service
      name: sfj-commit--account-node
    includeWhenEvaluating: false
    delete: false
  - name: revisions # name used to pass to CEL context
    reference:
      apiGroup: serving.knative.dev
      version: v1
      kind: revision
      matchLabels:
        vtex.io/proxy: <nginx-proxy-id>
    includeWhenEvaluating: true
    delete: false # will be deleted when ksvc is deleted
  conditions:
  # conditions can be compiled by a validating webhook and if any fails (i.e syntax errors) we can reject the creation of the cleaner
  - >
    !revisions.items.exists(r,
      r.metadata.annotations["serving.knative.dev/routes"]
        .split(",")
        .exists(route, !route.startsWith("sfj-") && !route.startsWith("preview-"))
      ||
      r.status.conditions
        .filter(c, c.type == "Active")
        .exists(c, c.status == "True" || time - timestamp(c.lastTransitionTime) < duration("360h"))
    )
status:
  # this list is indexed so we can quickly find this cleaner resource
  # when a target is updated
  resolvedTargets:
  - sfj-commit--account-00001.revisions.serving.knative.dev/v1
  - sfj-commit--account-00002.revisions.serving.knative.dev/v1
  - sfj-commit--account.service.serving.knative.dev/v1
  - sfj-commit--account-node.service.serving.knative.dev/v1
  nextScheduledEvaluation: "<timestamp>"
```

## High level implementation details

The controller shall be built using the [operator SDK](https://sdk.operatorframework.io/), which has scaffolding and code generation; it uses `Kubebuilder` under the hood which in turn uses the `sigs.k8s.io/controller-runtime` library. The `controller-runtime` library provides us with a lot of machinery that makes implementing an efficient controller fairly straightforward.

The meat of our logic resides in the `reconcile` routine we need to implement. `reconcile` is called whenever our managed CRD (in this case, the `Cleaner`) is created, updated or deleted. 

---

_I'm fairly certain I read somewhere that `reconcile` is also called when the controller starts up (i.e a restart), but I can't find it again. This is required for our design so we need to test it and if it isn't automatically done, we can easily list all CRDs and enqueue reconcile requests._

---


### Reconcile logic overview

* `GET cleaner object`
  * If not found: discard reconcile request
* If TTL hasn't expired: schedule reconcile request for the time when it will be expired `(creationTimestamp + ttl)`
* Otherwise:
  * Resolve targets with `includeWhenEvaluating = true` and, if we aren't yet, start watching their kinds with a [EnqueueRequestsFromMapFunc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/handler#EnqueueRequestsFromMapFunc). This means that whenever there is a update to these objects, we will lookup our index of `.status.resolvedTargets` and enqueue a reconcile request for any `Cleaner` object that references the updated object. 
  * Evaluate conditions using [cel-go](https://github.com/vtex/cleaner-controller/blob/rfc/initial-proposal/design/initial-proposal.md):
    * If all are true: trigger deletion of targets marked for deletion; the helm release, if present; and the cleaner object itself
    * Otherwise:
      * (Optional): if `.spec.retry.monotonic`, binary search when the condition might become true and schedule reconcile request for that time
      * Otherwise, schedule reconcile request for `.spec.retry.period` time in the future.

### Scheduling reconcile requests

Scheduling isn't a feature found on `controller-runtime`, at least not that I am aware of. However the controller library does allow us to enqueue reconcile requests procedurally by pushing them to a `go` channel. So the proposed solution is to have a binary heap (a.k.a `priority queue`) in memory sorted by `time`, which will allow us to schedule up to millions of entries efficiently. We then peek the heap periodically and trigger all events with `time` in the past.

If we ever reach the tens and hundreds of millions, the problem will most likely be memory and we'd need to have a database.



