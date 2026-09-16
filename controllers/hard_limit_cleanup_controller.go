/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	hardLimitAnnotationKey    = "cleaner.vtex.io/hard-limit-exceeded"
	hardLimitReasonAnnotation = "cleaner.vtex.io/hard-limit-reason"
	hardLimitTenantAnnotation = "cleaner.vtex.io/hard-limit-tenant"

	releaseShapeKsvc       = "ksvc"
	releaseShapeStandalone = "standalone"

	actionDeleted            = "deleted"
	actionAlreadyGone        = "already_gone"
	actionFailed             = "failed"
	actionSkippedExternalRef = "skipped_external_reference"
	actionSkippedSplitRef    = "skipped_split_reference"

	// knativeConfigurationLabel is the label Knative stamps on every
	// Revision naming its owning Configuration -- serving.ConfigurationLabelKey
	// in knative.dev/serving, spelled out here since only the API types
	// are vendored, not that reconciler-internal constants package.
	knativeConfigurationLabel = "serving.knative.dev/configuration"

	// serviceKnativeDev is the Knative Serving API group, shared by every
	// GVK below plus idle_knative_cleanup_controller.go's own
	// knativeServiceGVK (same package, so it's already in scope there
	// too). SonarQube (go:S1192) flags "serving.knative.dev" as a
	// duplicated literal once it appears 3+ times in a file -- this
	// constant is that fix, not a claim that every instance of the
	// string in the package is now gone: RBAC marker comments
	// (//+kubebuilder:rbac:groups=serving.knative.dev,...) still spell it
	// out literally, since controller-gen parses those as plain text and
	// can't dereference a Go identifier.
	serviceKnativeDev = "serving.knative.dev"
)

var (
	knativeConfigurationGVK = schema.GroupVersionKind{Group: serviceKnativeDev, Version: "v1", Kind: "Configuration"}
	knativeRouteGVK         = schema.GroupVersionKind{Group: serviceKnativeDev, Version: "v1", Kind: "Route"}
	knativeRouteListGVK     = schema.GroupVersionKind{Group: serviceKnativeDev, Version: "v1", Kind: "RouteList"}
	knativeRevisionGVK      = schema.GroupVersionKind{Group: serviceKnativeDev, Version: "v1", Kind: "Revision"}
)

// hardLimitCleanupActionTotal counts every hard-limit cleanup reconcile
// outcome, per tenant, action, and release shape -- the only way to see
// this reconciler's behavior in Prometheus/Grafana, since it otherwise
// only emits Kubernetes Events (visible via `kubectl describe`/`get
// events`, not scraped by Prometheus). action is one of: deleted,
// skipped_external_reference (a Route outside its own ksvc still
// references it), skipped_split_reference (a standalone Route's traffic
// is split with a sibling Configuration), failed (the delete call itself
// errored). release_shape is ksvc or standalone -- see this reconciler's
// own doc comment for what that distinction means.
var hardLimitCleanupActionTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "hard_limit_cleanup_action_total",
	Help: "Count of hard-limit cleanup reconcile outcomes, per tenant, action, and release shape.",
}, []string{"tenant", "action", "release_shape"})

func init() {
	metrics.Registry.MustRegister(hardLimitCleanupActionTotal)
}

// HardLimitCleanupReconciler deletes Knative releases that
// faststore-proxy-launcher's release limiter marked as exceeding their
// tenant's active-release cap (cleaner.vtex.io/hard-limit-exceeded=true).
// proxy-launcher only ever annotates -- it never deletes anything itself
// (see its Limiter.Admit doc comment: the goal is to keep tenants near
// their limit, best-effort, never to block a deploy). This reconciler is
// the other half of that cross-repo contract: it watches for the
// annotation and performs the actual deletion.
//
// The one invariant that overrides everything else: never delete a
// Configuration that any Route still sends live traffic to, regardless of
// who owns that Route. Found live on dr0 -- a tenant's stable "current
// production" Route (unrelated to any single release's own ksvc, created
// separately to alias whichever Configuration is current) kept pointing
// at a Configuration after this reconciler deleted it, since deleting the
// owning Service only cascades to the Route *that Service itself owns*.
// The alias Route survived, but broke (Ready: False, "Configuration ...
// not found") -- an outage that eviction was never supposed to cause; the
// entire point of proxy-launcher's limiter is to stay best-effort and
// never break something live (see its Admit doc comment).
//
// So before deleting anything, this always lists every Route in the
// namespace and checks which ones reference the marked Configuration.
// Two release shapes exist, cleaned up differently once that's clear:
//
//   - ksvc-owned: the Configuration has an ownerReference to a Knative
//     Service. Deleting the Service cascades to both its Configuration
//     and its own Route automatically (Knative sets an ownerReference
//     from each onto the Service -- see knative.dev/serving's
//     pkg/reconciler/service/resources/{configuration,route}.go). Safe to
//     delete only if no *other* Route (one the Service doesn't own)
//     references it too.
//   - standalone Configuration+Route pair (no owning Service, e.g.
//     acmecorp): there is no ownerReference tying them together, so this
//     deletes both explicitly -- but only the Routes whose traffic points
//     *exclusively* at this Configuration. If any referencing Route
//     splits traffic with another Configuration (e.g. an in-progress
//     canary), nothing is deleted at all: deleting the Configuration
//     would break that Route's traffic to a sibling release this
//     reconciler was never asked to remove.
type HardLimitCleanupReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=serving.knative.dev,resources=configurations,verbs=get;list;watch;delete
//+kubebuilder:rbac:groups=serving.knative.dev,resources=routes,verbs=get;list;watch;delete
//+kubebuilder:rbac:groups=serving.knative.dev,resources=services,verbs=get;list;watch;delete
//+kubebuilder:rbac:groups=serving.knative.dev,resources=revisions,verbs=get
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *HardLimitCleanupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	cfg := &unstructured.Unstructured{}
	cfg.SetGroupVersionKind(knativeConfigurationGVK)
	if err := r.Get(ctx, req.NamespacedName, cfg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if cfg.GetAnnotations()[hardLimitAnnotationKey] != "true" {
		return ctrl.Result{}, nil
	}

	refs, err := r.referencingRoutes(ctx, cfg.GetNamespace(), cfg.GetName())
	if err != nil {
		log.Error(err, "failed to resolve routes referencing the marked Configuration")
		return ctrl.Result{}, err
	}

	if ownerName, ok := serviceOwner(cfg); ok {
		return r.reconcileKsvcOwned(ctx, cfg, ownerName, refs)
	}
	return r.reconcileStandalone(ctx, cfg, refs)
}

// reconcileKsvcOwned handles a Configuration owned by a Knative Service:
// safe to delete only if no Route *other than the one the Service itself
// owns* (same name, by Knative's own convention -- dies in the cascade
// below regardless) still references it.
func (r *HardLimitCleanupReconciler) reconcileKsvcOwned(ctx context.Context, cfg *unstructured.Unstructured, ownerName string, refs []unstructured.Unstructured) (ctrl.Result, error) {
	tenant := cfg.GetAnnotations()[hardLimitTenantAnnotation]
	reason := cfg.GetAnnotations()[hardLimitReasonAnnotation]

	external := excludeRouteNamed(refs, ownerName)
	if len(external) > 0 {
		hardLimitCleanupActionTotal.WithLabelValues(tenant, actionSkippedExternalRef, releaseShapeKsvc).Inc()
		r.Recorder.Eventf(cfg, corev1.EventTypeWarning, "HardLimitCleanupSkipped",
			"not deleting Service %s: still referenced by route(s) %v outside its own ksvc -- deleting would break their live traffic",
			ownerName, routeNames(external))
		return ctrl.Result{}, nil
	}

	svc := &unstructured.Unstructured{}
	svc.SetGroupVersionKind(knativeServiceGVK)
	svc.SetName(ownerName)
	svc.SetNamespace(cfg.GetNamespace())

	if err := r.Delete(ctx, svc); err != nil {
		if apierrors.IsNotFound(err) {
			// Already gone -- a prior reconcile (or something external)
			// deleted it. Nothing to do, and this isn't a "deleted"
			// outcome for metrics/events: no delete happened just now.
			hardLimitCleanupActionTotal.WithLabelValues(tenant, actionAlreadyGone, releaseShapeKsvc).Inc()
			return ctrl.Result{}, nil
		}
		hardLimitCleanupActionTotal.WithLabelValues(tenant, actionFailed, releaseShapeKsvc).Inc()
		r.Recorder.Eventf(cfg, corev1.EventTypeWarning, "HardLimitCleanupFailed", "failed to delete owning Service %s: %s", ownerName, err.Error())
		return ctrl.Result{}, err
	}
	hardLimitCleanupActionTotal.WithLabelValues(tenant, actionDeleted, releaseShapeKsvc).Inc()
	r.Recorder.Eventf(cfg, corev1.EventTypeNormal, "HardLimitCleanupDeleted",
		"deleted Service %s (tenant=%s reason=%s), cascading to its Configuration and Route", ownerName, tenant, reason)
	return ctrl.Result{}, nil
}

// reconcileStandalone handles a Configuration with no owning Service:
// splits referencing routes into ones dedicated solely to it (safe to
// delete alongside it) and ones sharing traffic with another
// Configuration too (e.g. a stable alias Route, or an in-progress
// canary). Any of the latter blocks the whole deletion -- not just that
// Route -- since removing the Configuration would break its traffic
// regardless of whether the Route itself is touched.
func (r *HardLimitCleanupReconciler) reconcileStandalone(ctx context.Context, cfg *unstructured.Unstructured, refs []unstructured.Unstructured) (ctrl.Result, error) {
	tenant := cfg.GetAnnotations()[hardLimitTenantAnnotation]
	reason := cfg.GetAnnotations()[hardLimitReasonAnnotation]

	exclusive, shared, err := r.partitionRoutesByExclusivity(ctx, cfg.GetNamespace(), refs, cfg.GetName())
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(shared) > 0 {
		hardLimitCleanupActionTotal.WithLabelValues(tenant, actionSkippedSplitRef, releaseShapeStandalone).Inc()
		r.Recorder.Eventf(cfg, corev1.EventTypeWarning, "HardLimitCleanupSkipped",
			"not deleting: still referenced by route(s) %v with traffic split to another Configuration -- deleting would break their live traffic",
			routeNames(shared))
		return ctrl.Result{}, nil
	}

	for i := range exclusive {
		if err := r.Delete(ctx, &exclusive[i]); err != nil && !apierrors.IsNotFound(err) {
			hardLimitCleanupActionTotal.WithLabelValues(tenant, actionFailed, releaseShapeStandalone).Inc()
			r.Recorder.Eventf(cfg, corev1.EventTypeWarning, "HardLimitCleanupFailed", "failed to delete Route %s: %s", exclusive[i].GetName(), err.Error())
			return ctrl.Result{}, err
		}
	}

	if err := r.Delete(ctx, cfg); err != nil {
		if apierrors.IsNotFound(err) {
			hardLimitCleanupActionTotal.WithLabelValues(tenant, actionAlreadyGone, releaseShapeStandalone).Inc()
			return ctrl.Result{}, nil
		}
		hardLimitCleanupActionTotal.WithLabelValues(tenant, actionFailed, releaseShapeStandalone).Inc()
		r.Recorder.Eventf(cfg, corev1.EventTypeWarning, "HardLimitCleanupFailed", "failed to delete Configuration: %s", err.Error())
		return ctrl.Result{}, err
	}
	hardLimitCleanupActionTotal.WithLabelValues(tenant, actionDeleted, releaseShapeStandalone).Inc()
	r.Recorder.Eventf(cfg, corev1.EventTypeNormal, "HardLimitCleanupDeleted",
		"deleted standalone Configuration and %d associated Route(s) (tenant=%s reason=%s)", len(exclusive), tenant, reason)
	return ctrl.Result{}, nil
}

// partitionRoutesByExclusivity splits refs into routes whose traffic
// points exclusively at configName and routes that share traffic with at
// least one other Configuration too.
func (r *HardLimitCleanupReconciler) partitionRoutesByExclusivity(ctx context.Context, namespace string, refs []unstructured.Unstructured, configName string) (exclusive, shared []unstructured.Unstructured, err error) {
	for _, route := range refs {
		targets, _, err := unstructured.NestedSlice(route.Object, "spec", "traffic")
		if err != nil {
			return nil, nil, fmt.Errorf("reading spec.traffic for route %s: %w", route.GetName(), err)
		}
		only, err := r.trafficTargetsOnly(ctx, namespace, targets, configName)
		if err != nil {
			return nil, nil, err
		}
		if only {
			exclusive = append(exclusive, route)
		} else {
			shared = append(shared, route)
		}
	}
	return exclusive, shared, nil
}

// serviceOwner returns the name of the Knative Service that controls cfg,
// if any -- Knative always names a ksvc's Configuration and Route after
// the Service itself, but this reads the ownerReference directly rather
// than assuming that, since a name match alone wouldn't prove control.
func serviceOwner(cfg *unstructured.Unstructured) (name string, ok bool) {
	for _, ref := range cfg.GetOwnerReferences() {
		if ref.Kind == "Service" && ref.Controller != nil && *ref.Controller {
			return ref.Name, true
		}
	}
	return "", false
}

// referencingRoutes returns every Route in namespace with at least one
// spec.traffic entry resolving to configName -- regardless of who owns
// the Route or whether that traffic is shared with another Configuration.
// Deleting a Configuration any of these still reference would break
// their live traffic.
func (r *HardLimitCleanupReconciler) referencingRoutes(ctx context.Context, namespace, configName string) ([]unstructured.Unstructured, error) {
	var routes unstructured.UnstructuredList
	routes.SetGroupVersionKind(knativeRouteListGVK)
	if err := r.List(ctx, &routes, client.InNamespace(namespace)); err != nil {
		return nil, err
	}

	var matches []unstructured.Unstructured
	for _, route := range routes.Items {
		targets, found, err := unstructured.NestedSlice(route.Object, "spec", "traffic")
		if err != nil {
			return nil, fmt.Errorf("reading spec.traffic for route %s: %w", route.GetName(), err)
		}
		if !found {
			continue
		}
		references, err := r.referencesConfig(ctx, namespace, targets, configName)
		if err != nil {
			return nil, err
		}
		if references {
			matches = append(matches, route)
		}
	}
	return matches, nil
}

// referencesConfig reports whether at least one entry in a Route's
// spec.traffic list resolves to configName -- directly via
// configurationName, or indirectly via revisionName (see
// effectiveConfigurationName).
func (r *HardLimitCleanupReconciler) referencesConfig(ctx context.Context, namespace string, targets []interface{}, configName string) (bool, error) {
	for _, t := range targets {
		m, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		name, err := r.effectiveConfigurationName(ctx, namespace, m)
		if err != nil {
			return false, err
		}
		if name == configName {
			return true, nil
		}
	}
	return false, nil
}

// trafficTargetsOnly reports whether every entry in a Route's
// spec.traffic list resolves to configName -- i.e. none of its traffic is
// shared with a different Configuration.
func (r *HardLimitCleanupReconciler) trafficTargetsOnly(ctx context.Context, namespace string, targets []interface{}, configName string) (bool, error) {
	if len(targets) == 0 {
		return false, nil
	}
	for _, t := range targets {
		m, ok := t.(map[string]interface{})
		if !ok {
			return false, nil
		}
		name, err := r.effectiveConfigurationName(ctx, namespace, m)
		if err != nil {
			return false, err
		}
		if name != configName {
			return false, nil
		}
	}
	return true, nil
}

// effectiveConfigurationName resolves the Configuration a single
// spec.traffic entry sends its traffic to -- directly via
// configurationName, or, when the entry instead pins traffic to a
// specific Revision (a normal Knative pattern for canary/rollback
// pinning), by resolving revisionName back to its owning Configuration
// via the label Knative stamps on every Revision. Returns "" if the
// entry has neither field set or the Revision is already gone.
func (r *HardLimitCleanupReconciler) effectiveConfigurationName(ctx context.Context, namespace string, target map[string]interface{}) (string, error) {
	if name, _, _ := unstructured.NestedString(target, "configurationName"); name != "" {
		return name, nil
	}
	revisionName, _, _ := unstructured.NestedString(target, "revisionName")
	if revisionName == "" {
		return "", nil
	}

	rev := &unstructured.Unstructured{}
	rev.SetGroupVersionKind(knativeRevisionGVK)
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: revisionName}, rev); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("resolving revision %s to its owning configuration: %w", revisionName, err)
	}
	return rev.GetLabels()[knativeConfigurationLabel], nil
}

// excludeRouteNamed returns routes without the one (if any) named name.
func excludeRouteNamed(routes []unstructured.Unstructured, name string) []unstructured.Unstructured {
	out := make([]unstructured.Unstructured, 0, len(routes))
	for _, r := range routes {
		if r.GetName() != name {
			out = append(out, r)
		}
	}
	return out
}

// routeNames returns the names of routes, for logging/events.
func routeNames(routes []unstructured.Unstructured) []string {
	names := make([]string, len(routes))
	for i, r := range routes {
		names[i] = r.GetName()
	}
	return names
}

// SetupWithManager sets up the controller with the Manager.
//
// Also watches Route: without it, a Configuration skipped because a
// Route referenced it (see reconcileKsvcOwned/reconcileStandalone) would
// never be reconciled again once that Route's traffic changes or it's
// deleted, since proxy-launcher never re-touches an already-marked
// Configuration (pkg/limiter's partitionReleases excludes marked ones
// from consideration) -- nothing else would ever ask Kubernetes to
// reconcile it. mapRouteToConfigurations re-enqueues every Configuration
// a changed Route's traffic references (before and after the change,
// since controller-runtime's EnqueueRequestsFromMapFunc maps both
// ObjectOld and ObjectNew on updates), so a resolved block is retried
// promptly instead of leaving the Configuration stuck forever.
func (r *HardLimitCleanupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	cfg := &unstructured.Unstructured{}
	cfg.SetGroupVersionKind(knativeConfigurationGVK)
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(knativeRouteGVK)
	return ctrl.NewControllerManagedBy(mgr).
		For(cfg).
		Watches(route, handler.EnqueueRequestsFromMapFunc(r.mapRouteToConfigurations)).
		Complete(r)
}

// mapRouteToConfigurations enqueues a reconcile request for every
// Configuration a Route's spec.traffic currently references, so changing
// or deleting a Route re-evaluates whatever Configuration it used to (or
// now does) block from deletion.
func (r *HardLimitCleanupReconciler) mapRouteToConfigurations(ctx context.Context, obj client.Object) []reconcile.Request {
	route, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil
	}
	targets, found, err := unstructured.NestedSlice(route.Object, "spec", "traffic")
	if !found || err != nil {
		return nil
	}

	seen := map[string]struct{}{}
	var requests []reconcile.Request
	for _, t := range targets {
		m, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		name, err := r.effectiveConfigurationName(ctx, route.GetNamespace(), m)
		if err != nil || name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: route.GetNamespace()},
		})
	}
	return requests
}
