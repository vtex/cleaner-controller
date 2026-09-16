package controllers

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func buildHardLimitConfiguration(name string, marked bool, owner *unstructured.Unstructured) *unstructured.Unstructured {
	cfg := &unstructured.Unstructured{}
	cfg.SetGroupVersionKind(knativeConfigurationGVK)
	cfg.SetName(name)
	cfg.SetNamespace(ConditionalTTLNamespace)
	if marked {
		cfg.SetAnnotations(map[string]string{
			hardLimitAnnotationKey:    "true",
			hardLimitReasonAnnotation: "tenant_limit_exceeded",
			hardLimitTenantAnnotation: "tenant1",
		})
	}
	if owner != nil {
		isController := true
		cfg.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: "serving.knative.dev/v1",
			Kind:       "Service",
			Name:       owner.GetName(),
			UID:        owner.GetUID(),
			Controller: &isController,
		}})
	}
	return cfg
}

func buildHardLimitService(name string) *unstructured.Unstructured {
	svc := &unstructured.Unstructured{}
	svc.SetGroupVersionKind(knativeServiceGVK)
	svc.SetName(name)
	svc.SetNamespace(ConditionalTTLNamespace)
	return svc
}

func buildHardLimitRoute(name string, configNames ...string) *unstructured.Unstructured {
	targets := make([]interface{}, 0, len(configNames))
	for _, c := range configNames {
		targets = append(targets, map[string]interface{}{"configurationName": c})
	}
	return buildHardLimitRouteWithTargets(name, targets...)
}

// buildHardLimitRouteWithTargets builds a Route from raw spec.traffic
// entries, e.g. {"revisionName": "..."} for a Revision-pinned target
// instead of the plain {"configurationName": "..."} buildHardLimitRoute
// produces.
func buildHardLimitRouteWithTargets(name string, targets ...interface{}) *unstructured.Unstructured {
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(knativeRouteGVK)
	route.SetName(name)
	route.SetNamespace(ConditionalTTLNamespace)

	if err := unstructured.SetNestedSlice(route.Object, targets, "spec", "traffic"); err != nil {
		panic(err)
	}
	return route
}

// buildHardLimitRevision builds a Revision labeled as owned by configName
// -- the label Knative stamps on every Revision, which
// effectiveConfigurationName reads to resolve a Route's revisionName
// target back to its Configuration.
func buildHardLimitRevision(name, configName string) *unstructured.Unstructured {
	rev := &unstructured.Unstructured{}
	rev.SetGroupVersionKind(knativeRevisionGVK)
	rev.SetName(name)
	rev.SetNamespace(ConditionalTTLNamespace)
	rev.SetLabels(map[string]string{knativeConfigurationLabel: configName})
	return rev
}

var _ = Describe("HardLimitCleanup controller", func() {
	It("ignores a Configuration without the hard-limit annotation", func() {
		name := "hard-limit-unmarked"
		cfg := buildHardLimitConfiguration(name, false, nil)
		Expect(k8sClient.Create(ctx, cfg)).To(Succeed())

		Consistently(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeConfigurationGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ConditionalTTLNamespace}, found)
		}, duration, interval).Should(Succeed())
	})

	It("deletes the owning Service for a ksvc-owned marked Configuration", func() {
		name := "hard-limit-ksvc-owned"
		before := testutil.ToFloat64(hardLimitCleanupActionTotal.WithLabelValues("tenant1", actionDeleted, releaseShapeKsvc))

		svc := buildHardLimitService(name)
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())

		cfg := buildHardLimitConfiguration(name, true, svc)
		Expect(k8sClient.Create(ctx, cfg)).To(Succeed())

		Eventually(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeServiceGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ConditionalTTLNamespace}, found)
		}, timeout, interval).ShouldNot(Succeed())

		Expect(testutil.ToFloat64(hardLimitCleanupActionTotal.WithLabelValues("tenant1", actionDeleted, releaseShapeKsvc)) - before).To(Equal(1.0))
	})

	It("deletes a standalone marked Configuration and its exclusive Route", func() {
		configName := "hard-limit-standalone"
		routeName := "hard-limit-standalone-route"
		before := testutil.ToFloat64(hardLimitCleanupActionTotal.WithLabelValues("tenant1", actionDeleted, releaseShapeStandalone))

		route := buildHardLimitRoute(routeName, configName)
		Expect(k8sClient.Create(ctx, route)).To(Succeed())

		// The Route must exist (and have had a moment to reach the
		// manager's cache -- this controller never lists Routes outside
		// this path, so its Route informer starts lazily on first use)
		// before the marked Configuration is created and immediately
		// reconciled, or findExclusiveRoutes can race a cold cache and
		// see zero Routes.
		Eventually(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeRouteGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: routeName, Namespace: ConditionalTTLNamespace}, found)
		}, timeout, interval).Should(Succeed())
		time.Sleep(2 * time.Second)

		cfg := buildHardLimitConfiguration(configName, true, nil)
		Expect(k8sClient.Create(ctx, cfg)).To(Succeed())

		Eventually(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeConfigurationGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: configName, Namespace: ConditionalTTLNamespace}, found)
		}, timeout, interval).ShouldNot(Succeed())

		Eventually(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeRouteGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: routeName, Namespace: ConditionalTTLNamespace}, found)
		}, timeout, interval).ShouldNot(Succeed())

		Expect(testutil.ToFloat64(hardLimitCleanupActionTotal.WithLabelValues("tenant1", actionDeleted, releaseShapeStandalone)) - before).To(Equal(1.0))
	})

	It("never deletes a standalone Configuration whose Route traffic is split with another Configuration", func() {
		configName := "hard-limit-shared-route-victim"
		siblingConfigName := "hard-limit-shared-route-sibling"
		routeName := "hard-limit-shared-route"
		before := testutil.ToFloat64(hardLimitCleanupActionTotal.WithLabelValues("tenant1", actionSkippedSplitRef, releaseShapeStandalone))

		route := buildHardLimitRoute(routeName, configName, siblingConfigName)
		Expect(k8sClient.Create(ctx, route)).To(Succeed())

		Eventually(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeRouteGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: routeName, Namespace: ConditionalTTLNamespace}, found)
		}, timeout, interval).Should(Succeed())
		time.Sleep(2 * time.Second)

		cfg := buildHardLimitConfiguration(configName, true, nil)
		Expect(k8sClient.Create(ctx, cfg)).To(Succeed())

		// Deleting the Configuration would break the sibling's share of
		// this Route's traffic too -- neither the Route nor the
		// Configuration it's still (partly) pointed at may be removed.
		Consistently(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeConfigurationGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: configName, Namespace: ConditionalTTLNamespace}, found)
		}, duration, interval).Should(Succeed())

		Consistently(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeRouteGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: routeName, Namespace: ConditionalTTLNamespace}, found)
		}, duration, interval).Should(Succeed())

		Expect(testutil.ToFloat64(hardLimitCleanupActionTotal.WithLabelValues("tenant1", actionSkippedSplitRef, releaseShapeStandalone)) - before).To(BeNumerically(">=", 1.0))
	})

	// TestHardLimitCleanup_KsvcOwnedNeverDeletedWhileExternallyReferenced
	// reproduces a real incident found live on dr0: a tenant's stable
	// "current production" Route (unrelated to any one release's own
	// ksvc, pointing at whichever Configuration is current) kept
	// referencing a Configuration after this reconciler deleted its
	// owning Service -- deleting the Service only cascades to the Route
	// *it* owns, not to unrelated Routes elsewhere pointing at the same
	// Configuration. The alias Route survived, but broke (its target was
	// gone) -- an outage this reconciler must never cause.
	It("never deletes a ksvc-owned Service whose Configuration is referenced by an external Route", func() {
		name := "hard-limit-ksvc-external-ref"
		aliasRouteName := "hard-limit-ksvc-external-ref-alias"
		before := testutil.ToFloat64(hardLimitCleanupActionTotal.WithLabelValues("tenant1", actionSkippedExternalRef, releaseShapeKsvc))

		svc := buildHardLimitService(name)
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())

		// The alias Route: same shape as a tenant's stable production
		// Route, referencing the Configuration by name but owned by
		// nothing (created independently of the ksvc).
		aliasRoute := buildHardLimitRoute(aliasRouteName, name)
		Expect(k8sClient.Create(ctx, aliasRoute)).To(Succeed())

		Eventually(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeRouteGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: aliasRouteName, Namespace: ConditionalTTLNamespace}, found)
		}, timeout, interval).Should(Succeed())
		time.Sleep(2 * time.Second)

		cfg := buildHardLimitConfiguration(name, true, svc)
		Expect(k8sClient.Create(ctx, cfg)).To(Succeed())

		Consistently(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeServiceGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ConditionalTTLNamespace}, found)
		}, duration, interval).Should(Succeed())

		Consistently(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeRouteGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: aliasRouteName, Namespace: ConditionalTTLNamespace}, found)
		}, duration, interval).Should(Succeed())

		Expect(testutil.ToFloat64(hardLimitCleanupActionTotal.WithLabelValues("tenant1", actionSkippedExternalRef, releaseShapeKsvc)) - before).To(BeNumerically(">=", 1.0))
	})

	// It("never deletes a Configuration referenced only via a Route's
	// revisionName") reproduces a gap found in code review: a Route can
	// pin traffic to a specific Revision by name instead of naming its
	// Configuration (a normal canary/rollback pattern) -- referencesConfig
	// used to only ever look at configurationName, so this kind of
	// reference was invisible to referencingRoutes, which gates every
	// deletion decision in this reconciler. Left unfixed, this is the
	// exact class of outage the route reference guard exists to prevent,
	// just reached through a different field.
	It("never deletes a Configuration referenced only via a Route's revisionName", func() {
		configName := "hard-limit-revision-pinned"
		siblingConfigName := "hard-limit-revision-pinned-sibling"
		revisionName := configName + "-00001"
		routeName := "hard-limit-revision-pinned-route"
		before := testutil.ToFloat64(hardLimitCleanupActionTotal.WithLabelValues("tenant1", actionSkippedSplitRef, releaseShapeStandalone))

		rev := buildHardLimitRevision(revisionName, configName)
		Expect(k8sClient.Create(ctx, rev)).To(Succeed())

		route := buildHardLimitRouteWithTargets(routeName,
			map[string]interface{}{"revisionName": revisionName},
			map[string]interface{}{"configurationName": siblingConfigName},
		)
		Expect(k8sClient.Create(ctx, route)).To(Succeed())

		Eventually(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeRouteGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: routeName, Namespace: ConditionalTTLNamespace}, found)
		}, timeout, interval).Should(Succeed())
		time.Sleep(2 * time.Second)

		cfg := buildHardLimitConfiguration(configName, true, nil)
		Expect(k8sClient.Create(ctx, cfg)).To(Succeed())

		Consistently(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeConfigurationGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: configName, Namespace: ConditionalTTLNamespace}, found)
		}, duration, interval).Should(Succeed())

		Expect(testutil.ToFloat64(hardLimitCleanupActionTotal.WithLabelValues("tenant1", actionSkippedSplitRef, releaseShapeStandalone)) - before).To(BeNumerically(">=", 1.0))
	})

	// It("deletes a Configuration once a blocking Route's traffic no
	// longer references it") reproduces a second code-review gap:
	// SetupWithManager only watched Configuration, so a Configuration
	// skipped because a Route referenced it would never be reconciled
	// again once that Route's traffic changed or was removed --
	// proxy-launcher never re-touches an already-marked Configuration, so
	// nothing else would ever ask Kubernetes to look at it again. This
	// proves the Route watch (mapRouteToConfigurations) re-enqueues it.
	It("deletes a Configuration once a blocking Route's traffic no longer references it", func() {
		configName := "hard-limit-self-heals"
		siblingConfigName := "hard-limit-self-heals-sibling"
		routeName := "hard-limit-self-heals-route"

		route := buildHardLimitRoute(routeName, configName, siblingConfigName)
		Expect(k8sClient.Create(ctx, route)).To(Succeed())

		Eventually(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeRouteGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: routeName, Namespace: ConditionalTTLNamespace}, found)
		}, timeout, interval).Should(Succeed())
		time.Sleep(2 * time.Second)

		cfg := buildHardLimitConfiguration(configName, true, nil)
		Expect(k8sClient.Create(ctx, cfg)).To(Succeed())

		// Blocked while the Route's traffic is still split.
		Consistently(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeConfigurationGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: configName, Namespace: ConditionalTTLNamespace}, found)
		}, duration, interval).Should(Succeed())

		// The cutover finishes: the Route's traffic now points solely at
		// configName. Nothing touches the Configuration itself -- only
		// the Route watch can make this reconcile again.
		Eventually(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeRouteGVK)
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: routeName, Namespace: ConditionalTTLNamespace}, found); err != nil {
				return err
			}
			if err := unstructured.SetNestedSlice(found.Object, []interface{}{
				map[string]interface{}{"configurationName": configName},
			}, "spec", "traffic"); err != nil {
				return err
			}
			return k8sClient.Update(ctx, found)
		}, timeout, interval).Should(Succeed())

		Eventually(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeConfigurationGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: configName, Namespace: ConditionalTTLNamespace}, found)
		}, timeout, interval).ShouldNot(Succeed())
	})

	// It("does not count an already-deleted owning Service as a fresh
	// deletion") reproduces a third code-review gap: apierrors.IsNotFound
	// on the Delete call fell through to the same success path as an
	// actual deletion, misattributing the metric/event whenever the
	// Service was already gone before this reconcile ran.
	It("does not count an already-deleted owning Service as a fresh deletion", func() {
		name := "hard-limit-already-gone"
		before := testutil.ToFloat64(hardLimitCleanupActionTotal.WithLabelValues("tenant1", actionAlreadyGone, releaseShapeKsvc))
		beforeDeleted := testutil.ToFloat64(hardLimitCleanupActionTotal.WithLabelValues("tenant1", actionDeleted, releaseShapeKsvc))

		// owner is never created -- serviceOwner still reports it since
		// the ownerReference names it regardless, but r.Delete(ctx, svc)
		// then hits NotFound. A synthetic UID stands in for the one the
		// API server would normally assign on Create, since ownerReferences
		// require a non-empty uid.
		owner := buildHardLimitService(name)
		owner.SetUID(types.UID("11111111-1111-1111-1111-111111111111"))
		cfg := buildHardLimitConfiguration(name, true, owner)
		Expect(k8sClient.Create(ctx, cfg)).To(Succeed())

		Eventually(func() float64 {
			return testutil.ToFloat64(hardLimitCleanupActionTotal.WithLabelValues("tenant1", actionAlreadyGone, releaseShapeKsvc)) - before
		}, timeout, interval).Should(BeNumerically(">=", 1.0))

		Expect(testutil.ToFloat64(hardLimitCleanupActionTotal.WithLabelValues("tenant1", actionDeleted, releaseShapeKsvc)) - beforeDeleted).To(Equal(0.0))
	})
})
