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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	hardLimitAnnotationKey    = "cleaner.vtex.io/hard-limit-exceeded"
	hardLimitReasonAnnotation = "cleaner.vtex.io/hard-limit-reason"
	hardLimitTenantAnnotation = "cleaner.vtex.io/hard-limit-tenant"
)

var (
	knativeConfigurationGVK = schema.GroupVersionKind{Group: "serving.knative.dev", Version: "v1", Kind: "Configuration"}
	knativeRouteGVK         = schema.GroupVersionKind{Group: "serving.knative.dev", Version: "v1", Kind: "Route"}
	knativeRouteListGVK     = schema.GroupVersionKind{Group: "serving.knative.dev", Version: "v1", Kind: "RouteList"}
)

// HardLimitCleanupReconciler deletes Knative releases that
// faststore-proxy-launcher's release limiter marked as exceeding their
// tenant's active-release cap (cleaner.vtex.io/hard-limit-exceeded=true).
// proxy-launcher only ever annotates -- it never deletes anything itself
// (see its Limiter.Admit doc comment: the goal is to keep tenants near
// their limit, best-effort, never to block a deploy). This reconciler is
// the other half of that cross-repo contract: it watches for the
// annotation and performs the actual deletion.
//
// Two release shapes exist, and they're cleaned up differently:
//
//   - ksvc-owned: the Configuration has an ownerReference to a Knative
//     Service. Deleting the Service cascades to both its Configuration
//     and Route automatically (Knative sets an ownerReference from each
//     onto the Service -- see knative.dev/serving's
//     pkg/reconciler/service/resources/{configuration,route}.go), so this
//     deletes the Service and nothing else.
//   - standalone Configuration+Route pair (no owning Service, e.g.
//     acmecorp): there is no ownerReference tying them together, so this
//     deletes both explicitly. A Route is only deleted if every one of
//     its traffic targets points at this Configuration -- one split
//     across multiple Configurations (e.g. an in-progress canary) is left
//     alone, since deleting it would cut live traffic to a sibling
//     release this reconciler was never asked to remove.
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

	if ownerName, ok := serviceOwner(cfg); ok {
		svc := &unstructured.Unstructured{}
		svc.SetGroupVersionKind(knativeServiceGVK)
		svc.SetName(ownerName)
		svc.SetNamespace(cfg.GetNamespace())

		if err := r.Delete(ctx, svc); err != nil && !apierrors.IsNotFound(err) {
			r.Recorder.Eventf(cfg, corev1.EventTypeWarning, "HardLimitCleanupFailed", "failed to delete owning Service %s: %s", ownerName, err.Error())
			return ctrl.Result{}, err
		}
		r.Recorder.Eventf(cfg, corev1.EventTypeNormal, "HardLimitCleanupDeleted",
			"deleted Service %s (tenant=%s reason=%s), cascading to its Configuration and Route", ownerName, tenant, reason)
		return ctrl.Result{}, nil
	}

	routes, err := r.findExclusiveRoutes(ctx, cfg.GetNamespace(), cfg.GetName())
	if err != nil {
		log.Error(err, "failed to list routes for standalone Configuration cleanup")
		return ctrl.Result{}, err
	}
	for i := range routes {
		if err := r.Delete(ctx, &routes[i]); err != nil && !apierrors.IsNotFound(err) {
			r.Recorder.Eventf(cfg, corev1.EventTypeWarning, "HardLimitCleanupFailed", "failed to delete Route %s: %s", routes[i].GetName(), err.Error())
			return ctrl.Result{}, err
		}
	}

	if err := r.Delete(ctx, cfg); err != nil && !apierrors.IsNotFound(err) {
		r.Recorder.Eventf(cfg, corev1.EventTypeWarning, "HardLimitCleanupFailed", "failed to delete Configuration: %s", err.Error())
		return ctrl.Result{}, err
	}
	r.Recorder.Eventf(cfg, corev1.EventTypeNormal, "HardLimitCleanupDeleted",
		"deleted standalone Configuration and %d associated Route(s) (tenant=%s reason=%s)", len(routes), tenant, reason)
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

// findExclusiveRoutes returns every Route in namespace whose spec.traffic
// points exclusively at configName -- never one split across multiple
// Configurations, since deleting that would cut live traffic to whichever
// sibling release this reconciler wasn't asked to remove.
func (r *HardLimitCleanupReconciler) findExclusiveRoutes(ctx context.Context, namespace, configName string) ([]unstructured.Unstructured, error) {
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
		if !found || !trafficTargetsOnly(targets, configName) {
			continue
		}
		matches = append(matches, route)
	}
	return matches, nil
}

// trafficTargetsOnly reports whether every entry in a Route's
// spec.traffic list names configName as its configurationName.
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

// SetupWithManager sets up the controller with the Manager.
func (r *HardLimitCleanupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	cfg := &unstructured.Unstructured{}
	cfg.SetGroupVersionKind(knativeConfigurationGVK)
	return ctrl.NewControllerManagedBy(mgr).
		For(cfg).
		Complete(r)
}
