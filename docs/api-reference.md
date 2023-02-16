# API Reference

## Packages
- [cleaner.vtex.io/v1alpha1](#cleanervtexiov1alpha1)


## cleaner.vtex.io/v1alpha1

Package v1alpha1 contains API Schema definitions for the cleaner v1alpha1 API group

### Resource Types
- [ConditionalTTL](#conditionalttl)
- [ConditionalTTLList](#conditionalttllist)



#### ConditionalTTL



ConditionalTTL is the Schema for the conditionalttls API

_Appears in:_
- [ConditionalTTLList](#conditionalttllist)

| Field | Description |
| --- | --- |
| `apiVersion` _string_ | `cleaner.vtex.io/v1alpha1`
| `kind` _string_ | `ConditionalTTL`
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |
| `spec` _[ConditionalTTLSpec](#conditionalttlspec)_ |  |


#### ConditionalTTLList



ConditionalTTLList contains a list of ConditionalTTL



| Field | Description |
| --- | --- |
| `apiVersion` _string_ | `cleaner.vtex.io/v1alpha1`
| `kind` _string_ | `ConditionalTTLList`
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |
| `items` _[ConditionalTTL](#conditionalttl) array_ |  |


#### ConditionalTTLSpec



ConditionalTTLSpec defines the desired state of ConditionalTTL

_Appears in:_
- [ConditionalTTL](#conditionalttl)

| Field | Description |
| --- | --- |
| `ttl` _[Duration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#duration-v1-meta)_ | TTL specifies the minimum duration the target objects will last |
| `retry` _[RetryConfig](#retryconfig)_ |  |
| `helm` _[HelmConfig](#helmconfig)_ |  |
| `targets` _[Target](#target) array_ |  |
| `conditions` _string array_ |  |
| `cloudEventSink` _string_ | TODO: validate https? protocol |




#### HelmConfig



HelmConfig defines the helm release associated with the targetted resources and whether the release should be deleted.

_Appears in:_
- [ConditionalTTLSpec](#conditionalttlspec)

| Field | Description |
| --- | --- |
| `release` _string_ | Release is the Helm release name. |
| `delete` _boolean_ | Delete specifies whether the Helm release should be deleted whenever the ConditionalTTL is triggered. |


#### RetryConfig



RetryConfig defines how the controller will retry the condition.

_Appears in:_
- [ConditionalTTLSpec](#conditionalttlspec)

| Field | Description |
| --- | --- |
| `period` _[Duration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#duration-v1-meta)_ | Period defines how long the controller should wait before retrying the condition. |


#### Target



Target declares how to find one or more resources related to the ConditionalTTL. Targets are watched in order to trigger a reevaluation of the conditions and, when the ConditionalTTl is triggered they might be deleted by the controller.

_Appears in:_
- [ConditionalTTLSpec](#conditionalttlspec)

| Field | Description |
| --- | --- |
| `name` _string_ | Name identifies this target group and identifies the target current state when evaluating the CEL conditions. The name `time` is invalid and is included by default during evaluation. |
| `delete` _boolean_ | Delete specifies whether this target group should be deleted when the ConditionalTTL is triggered. |
| `includeWhenEvaluating` _boolean_ | IncludeWhenEvaluating specifies whether this target group should be included in the CEL evaluation context. |
| `reference` _[TargetReference](#targetreference)_ | Reference declares how to find either a single object, through its name, or a collection, through LabelSelectors. |


#### TargetReference





_Appears in:_
- [Target](#target)

| Field | Description |
| --- | --- |
| `name` _string_ | Name matches a single object. If name is specified, LabelSelector is ignored. |
| `labelSelector` _[LabelSelector](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#labelselector-v1-meta)_ | LabelSelector allows more than one object to be included in the target group. If Name is not empty, LabelSelector is ignored. |


#### TargetStatus





_Appears in:_
- [ConditionalTTLStatus](#conditionalttlstatus)

| Field | Description |
| --- | --- |
| `name` _string_ | Name matches the declared name on Spec.Targets. |
| `delete` _boolean_ |  |
| `includeWhenEvaluating` _boolean_ |  |
| `state` _[Unstructured](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#unstructured-unstructured-v1)_ | State is the observed state of the target on the cluster when deletion began. |


