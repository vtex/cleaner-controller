# API Reference

## Packages
- [cleaner.vtex.io/v1alpha1](#cleanervtexiov1alpha1)


## cleaner.vtex.io/v1alpha1

Package v1alpha1 contains API Schema definitions for the cleaner v1alpha1 API group.

### Resource Types
- [ConditionalTTL](#conditionalttl)



#### ConditionalTTL



ConditionalTTL allows one to declare a set of conditions under which a set of
resources should be deleted.

The ConditionalTTL's controller will track the statuses of its referenced Targets,
periodically re-evaluating the declared conditions for deletion.



| Field | Description |
| --- | --- |
| `apiVersion` _string_ | `cleaner.vtex.io/v1alpha1`
| `kind` _string_ | `ConditionalTTL`
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |
| `spec` _[ConditionalTTLSpec](#conditionalttlspec)_ |  |


#### ConditionalTTLSpec



ConditionalTTLSpec represents the configuration for a ConditionalTTL object.
A ConditionalTTL's specification is the union of conditions under which
deletion begins and actions to be taken during it.

_Appears in:_
- [ConditionalTTL](#conditionalttl)

| Field | Description |
| --- | --- |
| `ttl` _[Duration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#duration-v1-meta)_ | Duration the controller should wait relative to the ConditionalTTL's CreationTime before starting deletion. |
| `retry` _[RetryConfig](#retryconfig)_ | Specifies how the controller should retry the evaluation of conditions. This field is required when the list of conditions is not empty. |
| `helm` _[HelmConfig](#helmconfig)_ | Optional: Allows a ConditionalTTL to refer to and possibly delete a Helm release, usually the release responsible for creating the targets of the ConditionalTTL. |
| `targets` _[Target](#target) array_ | List of targets the ConditionalTTL is interested in deleting or that are needed for evaluating the conditions under which deletion should take place. |
| `conditions` _string array_ | Optional list of [Common Expression Language](https://github.com/google/cel-spec) conditions which should all evaluate to true before deletion takes place. |
| `cloudEventSink` _string_ | Optional http(s) address the controller should send a [Cloud Event](https://github.com/cloudevents/spec/blob/main/cloudevents/spec.md) to after deletion takes place. |




#### HelmConfig



HelmConfig specifies a Helm release by its name and whether
the release should be deleted.

_Appears in:_
- [ConditionalTTLSpec](#conditionalttlspec)

| Field | Description |
| --- | --- |
| `release` _string_ | The Helm Release name. |
| `delete` _boolean_ | Delete specifies whether the Helm release should be deleted. |


#### RetryConfig



RetryConfig defines how the controller should retry evaluating the
set of conditions.

_Appears in:_
- [ConditionalTTLSpec](#conditionalttlspec)

| Field | Description |
| --- | --- |
| `period` _[Duration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#duration-v1-meta)_ | Period defines how long the controller should wait before retrying the condition. |


#### Target



Target declares how to find one or more resources related to the ConditionalTTL,
whether they should be deleted and whether they are necessary for evaluating the
set of conditions.

_Appears in:_
- [ConditionalTTLSpec](#conditionalttlspec)

| Field | Description |
| --- | --- |
| `name` _string_ | Name identifies this target group and is used to refer to its state when evaluating the set of conditions. The name `time` is invalid and is included by default during evaluation. |
| `delete` _boolean_ | Delete indicates whether this target group should be deleted when the ConditionalTTL is triggered. |
| `includeWhenEvaluating` _boolean_ | IncludeWhenEvaluating indicates whether this target group should be included in the CEL evaluation context. |
| `reference` _[TargetReference](#targetreference)_ | Reference declares how to find either a single object, using its name, or a collection, using a LabelSelector. |


#### TargetReference



TargetReference declares how a target group should be looked up.
A target group can reference either a single Kubernetes resource - in which case
finding it is required in other to evaluate the set of conditions - or
a collection of resources of the same GroupVersionKind. In contrast
with single targets, an empty collection is a valid value when evaluating
the set of conditions.

_Appears in:_
- [Target](#target)

| Field | Description |
| --- | --- |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |
| `name` _string_ | Name matches a single object. If name is specified, LabelSelector is ignored. |
| `labelSelector` _[LabelSelector](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.24/#labelselector-v1-meta)_ | LabelSelector allows more than one object to be included in the target group. If Name is not empty, LabelSelector is ignored. |


