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

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/storage/driver"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	cleanerv1alpha1 "github.com/vtex/cleaner-controller/api/v1alpha1"
)

var finalizers = []struct {
	name    string
	handler func(*ConditionalTTLReconciler, context.Context, *cleanerv1alpha1.ConditionalTTL) error
}{
	{name: "cleaner.vtex.io/target-finalizer", handler: (*ConditionalTTLReconciler).targetFinalizer},
	{name: "cleaner.vtex.io/release-finalizer", handler: (*ConditionalTTLReconciler).helmReleaseFinalizer},
	{name: "cleaner.vtex.io/cloud-event-finalizer", handler: (*ConditionalTTLReconciler).cloudEventFinalizer},
}

// ConditionalTTLReconciler reconciles a ConditionalTTL object
type ConditionalTTLReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Config *rest.Config

	CloudEventsClient cloudevents.Client
	Recorder          record.EventRecorder

	// HelmConfig is a pre-initialized Helm client. This is
	// a hack to make tests work.
	HelmConfig *action.Configuration
}

//+kubebuilder:rbac:groups=cleaner.vtex.io,resources=conditionalttls,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cleaner.vtex.io,resources=conditionalttls/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cleaner.vtex.io,resources=conditionalttls/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *ConditionalTTLReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	cTTL := &cleanerv1alpha1.ConditionalTTL{}
	if err := r.Get(ctx, req.NamespacedName, cTTL); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// object is being deleted
	if !cTTL.DeletionTimestamp.IsZero() {
		for _, finalizer := range finalizers {
			if !controllerutil.ContainsFinalizer(cTTL, finalizer.name) {
				continue
			}
			if err := finalizer.handler(r, ctx, cTTL); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(cTTL, finalizer.name)
			if err := r.Update(ctx, cTTL); err != nil {
				return ctrl.Result{}, err
			}
			// wait for next reconcile due to update above
			// to continue handling finalizers, otherwise
			// the reconcile after deletion throws an error
			// and the last finalizer is run twice
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil
	}

	t := time.Now()
	expiresAt := cTTL.CreationTimestamp.Add(cTTL.Spec.TTL.Duration)
	if !t.After(expiresAt) {
		readyCondition := metav1.Condition{
			Status:             metav1.ConditionUnknown,
			Reason:             cleanerv1alpha1.ConditionReasonNotExpired,
			Message:            "Waiting for resource to expire",
			Type:               cleanerv1alpha1.ConditionTypeReady,
			ObservedGeneration: cTTL.GetGeneration(),
		}
		apimeta.SetStatusCondition(&cTTL.Status.Conditions, readyCondition)
		if err := r.Status().Update(ctx, cTTL); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: expiresAt.Sub(t)}, nil
	}

	ts, err := r.resolveTargets(ctx, cTTL)
	if err != nil {
		log.Error(err, "Failed to resolve target")
		readyCondition := metav1.Condition{
			Status:             metav1.ConditionFalse,
			Reason:             cleanerv1alpha1.ConditionReasonTargetResolveError,
			Message:            "Error resolving targets: " + err.Error(),
			Type:               cleanerv1alpha1.ConditionTypeReady,
			ObservedGeneration: cTTL.GetGeneration(),
		}
		apimeta.SetStatusCondition(&cTTL.Status.Conditions, readyCondition)
		if err := r.Status().Update(ctx, cTTL); err != nil {
			return ctrl.Result{}, err
		}

		// TODO: maybe we can carry on with deletion of the CRD
		// if everything that should be deleted is NotFound after the TTL
		return ctrl.Result{}, err
	}

	celCtx := buildCELContext(ts, t)
	celOpts := buildCELOptions(cTTL)

	readyCondition := metav1.Condition{
		ObservedGeneration: cTTL.GetGeneration(),
	}
	condsMet, retryable := evaluateCELConditions(celOpts, celCtx, cTTL.Spec.Conditions, &readyCondition)
	apimeta.SetStatusCondition(&cTTL.Status.Conditions, readyCondition)

	if !condsMet {
		if err := r.Status().Update(ctx, cTTL); err != nil {
			return ctrl.Result{}, err
		}
		if retryable && cTTL.Spec.Retry != nil {
			// TODO: admission webhook should verify Retry is not nil
			// when conditions are used or we can set a default retry period
			return ctrl.Result{RequeueAfter: cTTL.Spec.Retry.Period.Duration}, nil
		}
		return ctrl.Result{}, nil
	}

	// preserve targets' state when conditions were met
	// to include in the cloudevent
	cTTL.Status.Targets = ts
	cTTL.Status.EvaluationTime = &metav1.Time{Time: t}
	if err := r.Status().Update(ctx, cTTL); err != nil {
		return ctrl.Result{}, err
	}

	// ensure all finalizers are present.
	// finalizers are only added once the cTTL and its targets
	// should be deleted so that a manual deletion of cTTL
	// does not cause the premature deletion of its targets / helm release
	{
		needsUpdate := false
		for _, finalizer := range finalizers {
			if controllerutil.ContainsFinalizer(cTTL, finalizer.name) {
				continue
			}
			needsUpdate = true
			controllerutil.AddFinalizer(cTTL, finalizer.name)
		}
		if needsUpdate {
			if err := r.Update(ctx, cTTL); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	if err := r.Delete(ctx, cTTL); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// resolveTarget resolves either a single target given its name or a List kind
// given a labelSelector.
func (r *ConditionalTTLReconciler) resolveTarget(ctx context.Context, namespace string, t *cleanerv1alpha1.Target) (runtime.Unstructured, error) {
	log := log.FromContext(ctx)
	gvk := schema.FromAPIVersionAndKind(t.Reference.APIVersion, t.Reference.Kind)
	if t.Reference.Name != nil {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(gvk)
		err := r.Get(ctx, types.NamespacedName{Name: *t.Reference.Name, Namespace: namespace}, u)
		if err != nil {
			return nil, err
		}
		return u, nil
	}
	// TODO: remove when we add admission webhook
	if t.Reference.LabelSelector == nil {
		return nil, fmt.Errorf("Target %q reference Name and LabelSelector can't both be nil", t.Name)
	}
	ul := &unstructured.UnstructuredList{}
	ul.SetGroupVersionKind(gvk)
	ls, err := metav1.LabelSelectorAsSelector(t.Reference.LabelSelector)
	if err != nil {
		return nil, err
	}
	err = r.List(ctx, ul, &client.ListOptions{
		LabelSelector: ls,
		Namespace:     namespace,
	})
	if err != nil {
		return nil, err
	}
	// sanity check
	if ul.GetContinue() != "" {
		err = errors.New("r.List: unexpected continuation token")
		log.Error(err, "", "gvk", gvk, "labelSelector", ls)
		return nil, err
	}
	return ul, nil
}

// resolveTargets resolves a list of cleanerv1alpha1.TargetStatus given
// the cTTL spec.
func (r *ConditionalTTLReconciler) resolveTargets(ctx context.Context, cTTL *cleanerv1alpha1.ConditionalTTL) ([]cleanerv1alpha1.TargetStatus, error) {
	ts := make([]cleanerv1alpha1.TargetStatus, len(cTTL.Spec.Targets))
	for i, t := range cTTL.Spec.Targets {
		ui, err := r.resolveTarget(ctx, cTTL.GetNamespace(), &t)
		if err != nil {
			return nil, fmt.Errorf("Error resolving target %q: %w", t.Name, err)
		}
		ts[i] = cleanerv1alpha1.TargetStatus{
			Name:                  t.Name,
			Delete:                t.Delete,
			IncludeWhenEvaluating: t.IncludeWhenEvaluating,
			State: &unstructured.Unstructured{
				Object: ui.UnstructuredContent(),
			},
		}
	}
	return ts, nil
}

// deleteTarget deletes a target and publishes events regarding what was done
// or any errors encountered.
func (r *ConditionalTTLReconciler) deleteTarget(ctx context.Context, cTTL *cleanerv1alpha1.ConditionalTTL, target *unstructured.Unstructured) error {
	err := r.Delete(ctx, target)
	if err == nil {
		r.Recorder.Eventf(cTTL, corev1.EventTypeNormal, "TargetDeleted", "Target %s/%s deleted", target.GetKind(), target.GetName())
		return nil
	}
	if apierrors.IsNotFound(err) {
		return nil
	}
	r.Recorder.Eventf(cTTL, corev1.EventTypeWarning, "DeleteTargetFailed", "Error deleting target %s/%s: %s", target.GetKind(), target.GetName(), err.Error())
	return err
}

// targetFinalizer handles cleaner.vtex.io/target-finalizer by either deleting
// a single target given its Name, or listing targets using a labelSelector
// and deleting the individual items. NotFound errors are ignored.
func (r *ConditionalTTLReconciler) targetFinalizer(ctx context.Context, cTTL *cleanerv1alpha1.ConditionalTTL) error {
	for _, t := range cTTL.Spec.Targets {
		if !t.Delete {
			continue
		}
		ui, err := r.resolveTarget(ctx, cTTL.GetNamespace(), &t)
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return err
		}
		switch u := ui.(type) {
		case *unstructured.UnstructuredList:
			err = u.EachListItem(func(o runtime.Object) error {
				item := o.(*unstructured.Unstructured)
				return r.deleteTarget(ctx, cTTL, item)
			})
		case *unstructured.Unstructured:
			err = r.deleteTarget(ctx, cTTL, u)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// helmReleaseFinalizer handles cleaner.vtex.io/release-finalizer by deleting
// the Helm Release declared on the cTTL spec. NotFound errors are ignored.
func (r *ConditionalTTLReconciler) helmReleaseFinalizer(ctx context.Context, cTTL *cleanerv1alpha1.ConditionalTTL) error {
	if cTTL.Spec.Helm == nil || !cTTL.Spec.Helm.Delete {
		return nil
	}
	log := log.FromContext(ctx)
	cfg := r.HelmConfig
	if cfg == nil {
		// HelmConfig should only be non-nil during tests
		cfg = new(action.Configuration)
		// TODO: helm driver (i.e "secret") should be configurable
		err := cfg.Init(r.clientForNamespace(cTTL.ObjectMeta.Namespace), cTTL.ObjectMeta.Namespace, "secret", func(format string, args ...interface{}) {
			log.V(1).Info(fmt.Sprintf(format, args...))
		})
		if err != nil {
			r.Recorder.Eventf(cTTL, corev1.EventTypeWarning, "HelmSetupFailed", "Error initializing Helm client: %s", err.Error())
			return err
		}
	}
	uninstall := action.NewUninstall(cfg)
	// TODO: support custom options for uninstall such as Wait and DisableHooks?
	_, err := uninstall.Run(cTTL.Spec.Helm.Release)
	if err != nil {
		if errors.Is(err, driver.ErrReleaseNotFound) {
			return nil
		}
		r.Recorder.Eventf(cTTL, corev1.EventTypeWarning, "HelmUninstallFailed", "Error uninstalling Helm release %q: %s", cTTL.Spec.Helm.Release, err.Error())
		return err
	}
	r.Recorder.Eventf(cTTL, corev1.EventTypeNormal, "HelmReleaseUninstalled", "Helm release %q uninstalled", cTTL.Spec.Helm.Release)
	return nil
}

// cloudEventFinalizer handles cleaner.vtex.io/cloud-event-finalizer by sending
// a CloudEvent of type conditionalTTL.deleted, from source cleaner.vtex.io/finalizer
// to the sink configured on the cTTL spec.
func (r *ConditionalTTLReconciler) cloudEventFinalizer(ctx context.Context, cTTL *cleanerv1alpha1.ConditionalTTL) error {
	if cTTL.Spec.CloudEventSink == nil {
		return nil
	}
	e := cloudevents.NewEvent()
	e.SetSource("cleaner.vtex.io/finalizer")
	e.SetType("conditionalTTL.deleted")
	e.SetTime(cTTL.Status.EvaluationTime.Time)
	e.SetData(cloudevents.ApplicationJSON, map[string]interface{}{
		"name":      cTTL.GetName(),
		"namespace": cTTL.GetNamespace(),
		"targets":   cTTL.Status.Targets,
	})

	ectx := cloudevents.ContextWithTarget(ctx, *cTTL.Spec.CloudEventSink)
	var res cloudevents.Result
	// the condition should probably be cloudevents.IsUndelivered
	// but there is an open issue https://github.com/cloudevents/sdk-go/issues/815
	if res = r.CloudEventsClient.Send(ectx, e); !cloudevents.IsACK(res) {
		r.Recorder.Eventf(cTTL, corev1.EventTypeWarning, "EventDeliveryFailed", "Error delivering deletion cloud event: %s", res.Error())
		return res
	}
	r.Recorder.Eventf(cTTL, corev1.EventTypeNormal, "EventDelivered", "Event delivered to %q", *cTTL.Spec.CloudEventSink)
	return nil
}

// clientForNamespace builds a genericclioptions.RESTClientGetter required by
// the Helm API
func (r *ConditionalTTLReconciler) clientForNamespace(namespace string) *genericclioptions.ConfigFlags {
	configFlags := genericclioptions.NewConfigFlags(false)
	configFlags.APIServer = &r.Config.Host
	configFlags.BearerToken = &r.Config.BearerToken
	configFlags.CAFile = &r.Config.CAFile
	configFlags.CertFile = &r.Config.CertFile
	configFlags.KeyFile = &r.Config.KeyFile
	configFlags.Insecure = &r.Config.Insecure
	configFlags.Namespace = &namespace
	return configFlags
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConditionalTTLReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cleanerv1alpha1.ConditionalTTL{}).
		Complete(r)
}
