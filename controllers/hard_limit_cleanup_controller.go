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
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	hardLimitAnnotationKey    = "cleaner.vtex.io/hard-limit-exceeded"
	hardLimitReasonAnnotation = "cleaner.vtex.io/hard-limit-reason"
	hardLimitTenantAnnotation = "cleaner.vtex.io/hard-limit-tenant"

	releaseShapeKsvc       = "ksvc"
	releaseShapeStandalone = "standalone"

	actionDeleted            = "deleted"
	actionFailed             = "failed"
	actionSkippedExternalRef = "skipped_external_reference"
	actionSkippedSplitRef    = "skipped_split_reference"
)

var (
	knativeConfigurationGVK = schema.GroupVersionKind{Group: "serving.knative.dev", Version: "v1", Kind: "Configuration"}
	knativeRouteGVK         = schema.GroupVersionKind{Group: "serving.knative.dev", Version: "v1", Kind: "Route"}
	knativeRouteListGVK     = schema.GroupVersionKind{Group: "serving.knative.dev", Version: "v1", Kind: "RouteList"}
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

	tenant := cfg.GetAnnotations()[hardLimitTenantAnnotation]
	reason := cfg.GetAnnotations()[hardLimitReasonAnnotation]

	refs, err := r.referencingRoutes(ctx, cfg.GetNamespace(), cfg.GetName())
	if err != nil {
		log.Error(err, "failed to list routes referencing the marked Configuration")
		return ctrl.Result{}, err
	}

	if ownerName, ok := serviceOwner(cfg); ok {
		// The Service's own Route shares its name (Knative's own naming
		// convention) and dies with it in the cascade below -- it's not
		// an external dependency. Anything else referencing this
		// Configuration is.
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

		if err := r.Delete(ctx, svc); err != nil && !apierrors.IsNotFound(err) {
			hardLimitCleanupActionTotal.WithLabelValues(tenant, actionFailed, releaseShapeKsvc).Inc()
			r.Recorder.Eventf(cfg, corev1.EventTypeWarning, "HardLimitCleanupFailed", "failed to delete owning Service %s: %s", ownerName, err.Error())
			return ctrl.Result{}, err
		}
		hardLimitCleanupActionTotal.WithLabelValues(tenant, actionDeleted, releaseShapeKsvc).Inc()
		r.Recorder.Eventf(cfg, corev1.EventTypeNormal, "HardLimitCleanupDeleted",
			"deleted Service %s (tenant=%s reason=%s), cascading to its Configuration and Route", ownerName, tenant, reason)
		return ctrl.Result{}, nil
	}

	// Standalone: split referencing routes into ones dedicated solely to
	// this Configuration (safe to delete alongside it) and ones sharing
	// traffic with another Configuration too (e.g. a stable alias Route,
	// or an in-progress canary). Any of the latter blocks the whole
	// deletion -- not just that Route -- since removing the Configuration
	// would break its traffic regardless of whether the Route itself is
	// touched.
	var exclusive, shared []unstructured.Unstructured
	for _, route := range refs {
		targets, _, err := unstructured.NestedSlice(route.Object, "spec", "traffic")
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("reading spec.traffic for route %s: %w", route.GetName(), err)
		}
		if trafficTargetsOnly(targets, cfg.GetName()) {
			exclusive = append(exclusive, route)
		} else {
			shared = append(shared, route)
		}
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

	if err := r.Delete(ctx, cfg); err != nil && !apierrors.IsNotFound(err) {
		hardLimitCleanupActionTotal.WithLabelValues(tenant, actionFailed, releaseShapeStandalone).Inc()
		r.Recorder.Eventf(cfg, corev1.EventTypeWarning, "HardLimitCleanupFailed", "failed to delete Configuration: %s", err.Error())
		return ctrl.Result{}, err
	}
	hardLimitCleanupActionTotal.WithLabelValues(tenant, actionDeleted, releaseShapeStandalone).Inc()
	r.Recorder.Eventf(cfg, corev1.EventTypeNormal, "HardLimitCleanupDeleted",
		"deleted standalone Configuration and %d associated Route(s) (tenant=%s reason=%s)", len(exclusive), tenant, reason)
	return ctrl.Result{}, nil
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
// spec.traffic entry naming configName as its configurationName --
// regardless of who owns the Route or whether that traffic is shared with
// another Configuration. Deleting a Configuration any of these still
// reference would break their live traffic.
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
		if !found || !referencesConfig(targets, configName) {
			continue
		}
		matches = append(matches, route)
	}
	return matches, nil
}

// referencesConfig reports whether at least one entry in a Route's
// spec.traffic list names configName as its configurationName.
func referencesConfig(targets []interface{}, configName string) bool {
	for _, t := range targets {
		m, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		if name, _, _ := unstructured.NestedString(m, "configurationName"); name == configName {
			return true
		}
	}
	return false
}

// trafficTargetsOnly reports whether every entry in a Route's
// spec.traffic list names configName as its configurationName -- i.e.
// none of its traffic is shared with a different Configuration.
func trafficTargetsOnly(targets []interface{}, configName string) bool {
	if len(targets) == 0 {
		return false
	}
	for _, t := range targets {
		m, ok := t.(map[string]interface{})
		if !ok {
			return false
		}
		name, _, _ := unstructured.NestedString(m, "configurationName")
		if name != configName {
			return false
		}
	}
	return true
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
func (r *HardLimitCleanupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	cfg := &unstructured.Unstructured{}
	cfg.SetGroupVersionKind(knativeConfigurationGVK)
	return ctrl.NewControllerManagedBy(mgr).
		For(cfg).
		Complete(r)
}
