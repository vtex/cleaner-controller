package controllers

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(knativeRouteGVK)
	route.SetName(name)
	route.SetNamespace(ConditionalTTLNamespace)

	targets := make([]interface{}, 0, len(configNames))
	for _, c := range configNames {
		targets = append(targets, map[string]interface{}{"configurationName": c})
	}
	err := unstructured.SetNestedSlice(route.Object, targets, "spec", "traffic")
	if err != nil {
		panic(err)
	}
	return route
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
		svc := buildHardLimitService(name)
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())

		cfg := buildHardLimitConfiguration(name, true, svc)
		Expect(k8sClient.Create(ctx, cfg)).To(Succeed())

		Eventually(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeServiceGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ConditionalTTLNamespace}, found)
		}, timeout, interval).ShouldNot(Succeed())
	})

	It("deletes a standalone marked Configuration and its exclusive Route", func() {
		configName := "hard-limit-standalone"
		routeName := "hard-limit-standalone-route"

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
	})

	It("never deletes a Route whose traffic is split with another Configuration", func() {
		configName := "hard-limit-shared-route-victim"
		siblingConfigName := "hard-limit-shared-route-sibling"
		routeName := "hard-limit-shared-route"

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

		Eventually(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeConfigurationGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: configName, Namespace: ConditionalTTLNamespace}, found)
		}, timeout, interval).ShouldNot(Succeed())

		Consistently(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeRouteGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: routeName, Namespace: ConditionalTTLNamespace}, found)
		}, duration, interval).Should(Succeed())
	})
})
