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
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	idleAnnotationExclude     = "cleaner.vtex.io/exclude"
	idleAnnotationIdleSince   = "cleaner.vtex.io/idle-since"
	knativeMinScaleAnnotation = "autoscaling.knative.dev/min-scale"
	knativeServiceLabel       = "serving.knative.dev/service"
)

var knativeServiceGVK = schema.GroupVersionKind{Group: "serving.knative.dev", Version: "v1", Kind: "Service"}

// isExcluded reports whether the Service opted out of idle cleanup via the
// cleaner.vtex.io/exclude annotation.
func isExcluded(svc *unstructured.Unstructured) bool {
	return svc.GetAnnotations()[idleAnnotationExclude] == "true"
}

// hasMinScaleZero reports whether the Service's revision template declares
// autoscaling.knative.dev/min-scale: "0".
func hasMinScaleZero(svc *unstructured.Unstructured) (bool, error) {
	anns, found, err := unstructured.NestedStringMap(svc.Object, "spec", "template", "metadata", "annotations")
	if err != nil {
		return false, fmt.Errorf("reading spec.template.metadata.annotations: %w", err)
	}
	if !found {
		return false, nil
	}
	return anns[knativeMinScaleAnnotation] == "0", nil
}

// readIdleSince reads and parses the cleaner.vtex.io/idle-since annotation.
// ok is false when the annotation is absent.
func readIdleSince(svc *unstructured.Unstructured) (time.Time, bool, error) {
	val, ok := svc.GetAnnotations()[idleAnnotationIdleSince]
	if !ok {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parsing %s annotation %q: %w", idleAnnotationIdleSince, val, err)
	}
	return t, true, nil
}

// IdleKnativeCleanupReconciler deletes Knative Services that declare
// autoscaling.knative.dev/min-scale: "0" and have stayed scaled to zero
// replicas for longer than Threshold.
type IdleKnativeCleanupReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Recorder record.EventRecorder

	// Threshold is how long a Service may stay idle (all owned Deployments
	// at 0 replicas) before it is deleted.
	Threshold time.Duration
}

//+kubebuilder:rbac:groups=serving.knative.dev,resources=services,verbs=get;list;watch;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *IdleKnativeCleanupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	svc := &unstructured.Unstructured{}
	svc.SetGroupVersionKind(knativeServiceGVK)
	if err := r.Get(ctx, req.NamespacedName, svc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if isExcluded(svc) {
		return ctrl.Result{}, nil
	}

	minScaleZero, err := hasMinScaleZero(svc)
	if err != nil {
		log.Error(err, "failed to read min-scale annotation")
		return ctrl.Result{}, err
	}

	candidate := false
	if minScaleZero {
		candidate, err = r.deploymentsScaledToZero(ctx, svc.GetNamespace(), svc.GetName())
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("listing deployments for service %s/%s: %w", svc.GetNamespace(), svc.GetName(), err)
		}
	}

	since, hasSince, err := readIdleSince(svc)
	if err != nil {
		log.Error(err, "invalid idle-since annotation, clearing it")
		hasSince = false
	}

	if !candidate {
		if hasSince {
			if err := r.clearIdleSince(ctx, svc); err != nil {
				return ctrl.Result{}, err
			}
			r.Recorder.Eventf(svc, corev1.EventTypeNormal, "IdleServiceReactivated", "Service is no longer idle, idle-since cleared")
		}
		return ctrl.Result{}, nil
	}

	now := time.Now()
	if !hasSince {
		if err := r.patchIdleSince(ctx, svc, now); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Eventf(svc, corev1.EventTypeNormal, "IdleServiceMarked", "Service marked idle, will be deleted after %s if it stays idle", r.Threshold)
		return ctrl.Result{RequeueAfter: r.Threshold}, nil
	}

	elapsed := now.Sub(since)
	if elapsed < r.Threshold {
		return ctrl.Result{RequeueAfter: r.Threshold - elapsed}, nil
	}

	if err := r.Delete(ctx, svc); err != nil && !apierrors.IsNotFound(err) {
		r.Recorder.Eventf(svc, corev1.EventTypeWarning, "IdleServiceDeleteFailed", "Error deleting idle service: %s", err.Error())
		return ctrl.Result{}, err
	}
	r.Recorder.Eventf(svc, corev1.EventTypeNormal, "IdleServiceDeleted", "Service deleted after being idle for %s", elapsed.Round(time.Second))
	return ctrl.Result{}, nil
}

// deploymentsScaledToZero reports whether every Deployment labeled
// serving.knative.dev/service=<serviceName> in namespace has 0 replicas.
// It returns false if no such Deployment exists yet.
func (r *IdleKnativeCleanupReconciler) deploymentsScaledToZero(ctx context.Context, namespace, serviceName string) (bool, error) {
	var deployments appsv1.DeploymentList
	err := r.List(ctx, &deployments,
		client.InNamespace(namespace),
		client.MatchingLabels{knativeServiceLabel: serviceName},
	)
	if err != nil {
		return false, err
	}
	if deployments.GetContinue() != "" {
		return false, errors.New("r.List: unexpected continuation token")
	}
	if len(deployments.Items) == 0 {
		return false, nil
	}
	for _, d := range deployments.Items {
		if d.Status.Replicas != 0 {
			return false, nil
		}
	}
	return true, nil
}

// patchIdleSince stamps the cleaner.vtex.io/idle-since annotation with t.
func (r *IdleKnativeCleanupReconciler) patchIdleSince(ctx context.Context, svc *unstructured.Unstructured, t time.Time) error {
	patch := client.MergeFrom(svc.DeepCopy())
	anns := svc.GetAnnotations()
	if anns == nil {
		anns = map[string]string{}
	}
	anns[idleAnnotationIdleSince] = t.UTC().Format(time.RFC3339)
	svc.SetAnnotations(anns)
	return r.Patch(ctx, svc, patch)
}

// clearIdleSince removes the cleaner.vtex.io/idle-since annotation.
func (r *IdleKnativeCleanupReconciler) clearIdleSince(ctx context.Context, svc *unstructured.Unstructured) error {
	patch := client.MergeFrom(svc.DeepCopy())
	anns := svc.GetAnnotations()
	delete(anns, idleAnnotationIdleSince)
	svc.SetAnnotations(anns)
	return r.Patch(ctx, svc, patch)
}

// SetupWithManager sets up the controller with the Manager.
func (r *IdleKnativeCleanupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	svc := &unstructured.Unstructured{}
	svc.SetGroupVersionKind(knativeServiceGVK)
	return ctrl.NewControllerManagedBy(mgr).
		For(svc).
		Watches(
			&appsv1.Deployment{},
			handler.EnqueueRequestsFromMapFunc(mapDeploymentToService),
		).
		Complete(r)
}

// mapDeploymentToService enqueues a reconcile for the Knative Service that
// owns obj, identified via the serving.knative.dev/service label.
func mapDeploymentToService(ctx context.Context, obj client.Object) []reconcile.Request {
	name, ok := obj.GetLabels()[knativeServiceLabel]
	if !ok {
		return nil
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: name, Namespace: obj.GetNamespace()}},
	}
}
