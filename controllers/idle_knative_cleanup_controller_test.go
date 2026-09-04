package controllers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
)

func buildIdleKnativeService(name string, minScaleZero, exclude bool) *unstructured.Unstructured {
	svc := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "serving.knative.dev/v1",
		"kind":       "Service",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": ConditionalTTLNamespace,
		},
	}}
	if exclude {
		svc.SetAnnotations(map[string]string{idleAnnotationExclude: "true"})
	}
	templateAnns := map[string]string{}
	if minScaleZero {
		templateAnns[knativeMinScaleAnnotation] = "0"
	}
	err := unstructured.SetNestedStringMap(svc.Object, templateAnns, "spec", "template", "metadata", "annotations")
	if err != nil {
		panic(err)
	}
	return svc
}

func buildIdleDeployment(name, serviceName string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ConditionalTTLNamespace,
			Labels: map[string]string{
				knativeServiceLabel: serviceName,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: pointer.Int32(replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": name},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{Name: "test-container", Image: "test-image"},
					},
				},
			},
		},
	}
}

func getIdleSinceAnnotation(name string) (string, error) {
	found := &unstructured.Unstructured{}
	found.SetGroupVersionKind(knativeServiceGVK)
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ConditionalTTLNamespace}, found); err != nil {
		return "", err
	}
	return found.GetAnnotations()[idleAnnotationIdleSince], nil
}

var _ = Describe("IdleKnativeCleanup controller", func() {
	It("does not mark a Service without min-scale=0 as idle", func() {
		name := "idle-no-minscale"
		Expect(k8sClient.Create(ctx, buildIdleKnativeService(name, false, false))).To(Succeed())
		Expect(k8sClient.Create(ctx, buildIdleDeployment(name+"-deployment", name, 0))).To(Succeed())

		Consistently(func() (string, error) {
			return getIdleSinceAnnotation(name)
		}, duration, interval).Should(BeEmpty())
	})

	It("marks idle-since when min-scale is 0 and all deployments are scaled to zero", func() {
		name := "idle-candidate"
		Expect(k8sClient.Create(ctx, buildIdleKnativeService(name, true, false))).To(Succeed())
		Expect(k8sClient.Create(ctx, buildIdleDeployment(name+"-deployment", name, 0))).To(Succeed())

		Eventually(func() (string, error) {
			return getIdleSinceAnnotation(name)
		}, timeout, interval).ShouldNot(BeEmpty())
	})

	It("deletes the Service once it has been idle past the threshold", func() {
		name := "idle-expired"
		Expect(k8sClient.Create(ctx, buildIdleKnativeService(name, true, false))).To(Succeed())
		Expect(k8sClient.Create(ctx, buildIdleDeployment(name+"-deployment", name, 0))).To(Succeed())

		Eventually(func() error {
			found := &unstructured.Unstructured{}
			found.SetGroupVersionKind(knativeServiceGVK)
			return k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ConditionalTTLNamespace}, found)
		}, idleThreshold*3, interval).ShouldNot(Succeed())
	})

	It("clears idle-since when the underlying deployment scales back up", func() {
		name := "idle-reactivated"
		depName := name + "-deployment"
		Expect(k8sClient.Create(ctx, buildIdleKnativeService(name, true, false))).To(Succeed())
		Expect(k8sClient.Create(ctx, buildIdleDeployment(depName, name, 0))).To(Succeed())

		Eventually(func() (string, error) {
			return getIdleSinceAnnotation(name)
		}, timeout, interval).ShouldNot(BeEmpty())

		foundDep := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: depName, Namespace: ConditionalTTLNamespace}, foundDep)).To(Succeed())
		foundDep.Status.Replicas = 1
		Expect(k8sClient.Status().Update(ctx, foundDep)).To(Succeed())

		Eventually(func() (string, error) {
			return getIdleSinceAnnotation(name)
		}, timeout, interval).Should(BeEmpty())
	})

	It("never marks an excluded Service as idle", func() {
		name := "idle-excluded"
		Expect(k8sClient.Create(ctx, buildIdleKnativeService(name, true, true))).To(Succeed())
		Expect(k8sClient.Create(ctx, buildIdleDeployment(name+"-deployment", name, 0))).To(Succeed())

		Consistently(func() (string, error) {
			return getIdleSinceAnnotation(name)
		}, duration, interval).Should(BeEmpty())
	})
})
