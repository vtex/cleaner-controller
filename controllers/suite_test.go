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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"

	cloudevents "github.com/cloudevents/sdk-go/v2"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	memcached "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	cleanerv1alpha1 "github.com/vtex/cleaner-controller/api/v1alpha1"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
	helmCfg   *action.Configuration
	server    *httptest.Server
	tap       *tapHandler
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

type tapHandler struct {
	handler   http.Handler
	lastEvent cloudevents.Event
}

func (t *tapHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if t.handler == nil {
		w.WriteHeader(500)
		return
	}

	t.handler.ServeHTTP(w, r)
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = cleanerv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	helmCfg = new(action.Configuration)
	err = helmCfg.Init(&clientWrapper{client: k8sClient, cfg: cfg}, "default", "secret", func(format string, args ...interface{}) {
		logf.Log.Info(fmt.Sprintf(format, args...))
	})
	Expect(err).ToNot(HaveOccurred())

	cec, err := cloudevents.NewClientHTTP()
	Expect(err).ToNot(HaveOccurred())

	err = (&ConditionalTTLReconciler{
		Client:            k8sManager.GetClient(),
		Scheme:            k8sManager.GetScheme(),
		Recorder:          k8sManager.GetEventRecorderFor("cleaner-controller"),
		HelmConfig:        helmCfg,
		CloudEventsClient: cec,
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

const (
	ConditionalTTLName      = "test-conditionalttl"
	ConditionalTTLNamespace = "default"

	TargetPodName      = "test-target-pod"
	TargetPodNamespace = "default"

	LabelSelectorKey   = "myLabel"
	LabelSelectorValue = "myPods"

	timeout  = time.Second * 10
	duration = time.Second * 10
	interval = time.Millisecond * 250
)

var (
	ListedPodNames = []string{"pod-1", "pod-2"}
)

var _ = Describe("ConditionalTTL controller", Ordered, func() {

	BeforeAll(func() {
		By("By creating mock helm release")
		release := release.Mock(&release.MockReleaseOptions{
			Name:      "my-release",
			Namespace: "default",
		})
		install := action.NewInstall(helmCfg)
		install.ReleaseName = release.Name
		install.Namespace = release.Namespace
		_, err := install.Run(release.Chart, nil)
		Expect(err).ToNot(HaveOccurred())

		By("By creating mock cloudevents server")
		tap = &tapHandler{}
		server = httptest.NewServer(tap)

		protocol, err := cloudevents.NewHTTP(
			cloudevents.WithTarget(server.URL),
			cloudevents.WithPort(0),
		)
		Expect(err).ToNot(HaveOccurred())

		tap.handler = protocol

		logf.Log.Info("Mock cloudevents server initializer", "url", server.URL)

		ce, err := cloudevents.NewClient(protocol)
		Expect(err).ToNot(HaveOccurred())

		go func() {
			defer GinkgoRecover()
			err := ce.StartReceiver(ctx, func(e cloudevents.Event) cloudevents.Result {
				tap.lastEvent = e
				return cloudevents.ResultACK
			})
			Expect(err).ToNot(HaveOccurred())
		}()
	})

	AfterAll(func() {
		By("By closing cloudevents server")
		server.Close()
	})

	Context("Before expiring", func() {
		It("Should have no finalizers and have unknown Ready condition", func() {
			By("By creating a new ConditionalTTL")
			cTTL := &cleanerv1alpha1.ConditionalTTL{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cleaner.vtex.io/v1alpha1",
					Kind:       "ConditionalTTL",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      ConditionalTTLName,
					Namespace: ConditionalTTLNamespace,
				},
				Spec: cleanerv1alpha1.ConditionalTTLSpec{
					TTL: &metav1.Duration{Duration: 5 * time.Minute},
				},
			}
			Expect(k8sClient.Create(ctx, cTTL)).Should(Succeed())

			cTTLLookupKey := types.NamespacedName{
				Name:      ConditionalTTLName,
				Namespace: ConditionalTTLNamespace,
			}
			createdCTTL := &cleanerv1alpha1.ConditionalTTL{}
			var readyCondition *metav1.Condition

			Eventually(func() bool {
				err := k8sClient.Get(ctx, cTTLLookupKey, createdCTTL)
				if err != nil {
					return false
				}
				readyCondition = apimeta.FindStatusCondition(createdCTTL.Status.Conditions, cleanerv1alpha1.ConditionTypeReady)
				if readyCondition == nil {
					return false
				}

				return true
			}, timeout, interval).Should(BeTrue())

			Expect(readyCondition.Status).Should(Equal(metav1.ConditionUnknown))
			Expect(readyCondition.Reason).Should(Equal(cleanerv1alpha1.ConditionReasonNotExpired))
			Expect(len(createdCTTL.Finalizers)).Should(Equal(0))

			Expect(k8sClient.Delete(ctx, cTTL)).Should(Succeed())
		})
	})

	Context("After expiring", func() {
		It("Has failed Ready condition if targets are not found", func() {
			By("By creating a new ConditionalTTL with non existent target")
			cTTL := &cleanerv1alpha1.ConditionalTTL{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "cleaner.vtex.io/v1alpha1",
					Kind:       "ConditionalTTL",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      ConditionalTTLName,
					Namespace: ConditionalTTLNamespace,
				},
				Spec: cleanerv1alpha1.ConditionalTTLSpec{
					TTL: &metav1.Duration{Duration: 0},
					Retry: &cleanerv1alpha1.RetryConfig{
						Period: &metav1.Duration{Duration: 1 * time.Second},
					},
					Helm: &cleanerv1alpha1.HelmConfig{
						Release: "my-release",
						Delete:  true,
					},
					CloudEventSink: pointer.String(server.URL),
					Targets: []cleanerv1alpha1.Target{
						{
							Name:                  "pod",
							IncludeWhenEvaluating: true,
							Delete:                true,
							Reference: cleanerv1alpha1.TargetReference{
								TypeMeta: metav1.TypeMeta{
									APIVersion: "v1",
									Kind:       "Pod",
								},
								Name: pointer.String(TargetPodName),
							},
						},
						{
							Name:                  "pods",
							IncludeWhenEvaluating: true,
							Delete:                true,
							Reference: cleanerv1alpha1.TargetReference{
								TypeMeta: metav1.TypeMeta{
									APIVersion: "v1",
									Kind:       "Pod",
								},
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										LabelSelectorKey: LabelSelectorValue,
									},
								},
							},
						},
					},
					Conditions: []string{
						// Test single and list targets are passed correctly
						`has(pod.metadata.annotations) &&
						pod.metadata.annotations.exists(k, k == "shouldDelete") &&
						size(pods.items) == 2
						`,
					},
				},
			}
			Expect(k8sClient.Create(ctx, cTTL)).Should(Succeed())

			cTTLLookupKey := types.NamespacedName{
				Name:      ConditionalTTLName,
				Namespace: ConditionalTTLNamespace,
			}
			createdCTTL := &cleanerv1alpha1.ConditionalTTL{}
			var readyCondition *metav1.Condition

			Eventually(func() bool {
				err := k8sClient.Get(ctx, cTTLLookupKey, createdCTTL)
				if err != nil {
					return false
				}
				readyCondition = apimeta.FindStatusCondition(createdCTTL.Status.Conditions, cleanerv1alpha1.ConditionTypeReady)
				if readyCondition == nil {
					return false
				}

				if readyCondition.Status != metav1.ConditionFalse {
					return false
				}

				return true
			}, timeout, interval).Should(BeTrue())

			Expect(readyCondition.Status).Should(Equal(metav1.ConditionFalse))
			Expect(readyCondition.Reason).Should(Equal(cleanerv1alpha1.ConditionReasonTargetResolveError))
			Expect(len(createdCTTL.Finalizers)).Should(Equal(0))
		})

		// this happens because a target not found is a reconcile error,
		// hence it is retried. In the future we could watch for target
		// changes
		It("Picks up the creation of targets", func() {
			By("By creating single target pod")
			pod := &v1.Pod{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Pod",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      TargetPodName,
					Namespace: TargetPodNamespace,
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "test-container",
							Image: "test-image",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			By("By creating list of target pods")
			for _, name := range ListedPodNames {
				p := buildPod(name)
				Expect(k8sClient.Create(ctx, p)).Should(Succeed())
			}

			By("By verifying Ready Condition")
			cTTLLookupKey := types.NamespacedName{
				Name:      ConditionalTTLName,
				Namespace: ConditionalTTLNamespace,
			}
			createdCTTL := &cleanerv1alpha1.ConditionalTTL{}
			var readyCondition *metav1.Condition

			Eventually(func() bool {
				err := k8sClient.Get(ctx, cTTLLookupKey, createdCTTL)
				if err != nil {
					return false
				}
				readyCondition = apimeta.FindStatusCondition(createdCTTL.Status.Conditions, cleanerv1alpha1.ConditionTypeReady)
				if readyCondition == nil {
					return false
				}

				logf.Log.Info("current cTTL", "cTTL", createdCTTL)
				if readyCondition.Status != metav1.ConditionTrue {
					return false
				}

				return true
			}, timeout, interval).Should(BeTrue())

			Expect(readyCondition.Status).Should(Equal(metav1.ConditionTrue))
			Expect(readyCondition.Reason).Should(Equal(cleanerv1alpha1.ConditionReasonWaitingForConditions))
			Expect(len(createdCTTL.Finalizers)).Should(Equal(0))
		})

		It("Deletes all targets and CTTL when conditions are met", func() {
			By("By verifying single target is deleted")
			podLookupKey := types.NamespacedName{
				Name:      TargetPodName,
				Namespace: TargetPodNamespace,
			}
			pod := &v1.Pod{}
			Expect(k8sClient.Get(ctx, podLookupKey, pod)).Should(Succeed())
			pod.SetAnnotations(map[string]string{"shouldDelete": "true"})
			Expect(k8sClient.Update(ctx, pod)).Should(Succeed())

			Eventually(func() error {
				return k8sClient.Get(ctx, podLookupKey, pod)
			}, timeout, interval).ShouldNot(Succeed())

			By("By verifying list targets are deleted")
			for _, name := range ListedPodNames {
				podLookupKey = types.NamespacedName{
					Name:      name,
					Namespace: TargetPodNamespace,
				}
				Eventually(func() error {
					return k8sClient.Get(ctx, podLookupKey, pod)
				}, timeout, interval).ShouldNot(Succeed())
			}

			cTTLLookupKey := types.NamespacedName{
				Name:      ConditionalTTLName,
				Namespace: ConditionalTTLNamespace,
			}
			foundCTTL := &cleanerv1alpha1.ConditionalTTL{}
			Eventually(func() error {
				return k8sClient.Get(ctx, cTTLLookupKey, foundCTTL)
			}, timeout, interval).ShouldNot(Succeed())
		})

		It("Deletes helm release when conditions are met", func() {
			get := action.NewGet(helmCfg)
			_, err := get.Run("my-release")
			Expect(err).To(Equal(driver.ErrReleaseNotFound))
		})

		It("Delivers cloudevent on deletion", func() {
			Expect(tap.lastEvent).ToNot(BeNil())
			Expect(tap.lastEvent.Type()).To(Equal("conditionalTTL.deleted"))
			Expect(tap.lastEvent.Source()).To(Equal("cleaner.vtex.io/finalizer"))
			Expect(tap.lastEvent.DataContentType()).To(Equal("application/json"))

			data := make(map[string]interface{})
			err := json.Unmarshal(tap.lastEvent.Data(), &data)
			Expect(err).ToNot(HaveOccurred())

			Expect(data["name"]).To(Equal(ConditionalTTLName))
			Expect(data["namespace"]).To(Equal(ConditionalTTLNamespace))
			Expect(data["targets"]).To(HaveLen(2))

			singleTarget := data["targets"].([]interface{})[0].(map[string]interface{})
			Expect(singleTarget["name"]).To(Equal("pod"))
			u := &unstructured.Unstructured{
				Object: singleTarget["state"].(map[string]interface{}),
			}
			Expect(u.GetName()).To(Equal(TargetPodName))
			Expect(u.GetAnnotations()).To(HaveKeyWithValue("shouldDelete", "true"))

			listTarget := data["targets"].([]interface{})[1].(map[string]interface{})
			Expect(listTarget["name"]).To(Equal("pods"))
			ul := &unstructured.UnstructuredList{}
			ul.SetUnstructuredContent(listTarget["state"].(map[string]interface{}))
			Expect(ul.GetKind()).To(Equal("PodList"))

			foundNames := []string{}
			err = ul.EachListItem(func(obj runtime.Object) error {
				item, ok := obj.(*unstructured.Unstructured)
				if !ok {
					return errors.New("list item can't be cast to *unstructured.Unstructured")
				}
				foundNames = append(foundNames, item.GetName())
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(foundNames).To(ConsistOf(ListedPodNames[0], ListedPodNames[1]))
		})
	})

	// In the future could be done by admission webhook
	Context("After expiring with CEL errors", func() {
		tcs := []struct {
			wantedReason string
			condition    string
		}{
			{
				wantedReason: cleanerv1alpha1.ConditionReasonCompileError,
				condition:    "size(invalidTargetName) == 2",
			},
			{
				wantedReason: cleanerv1alpha1.ConditionReasonEvaluationError,
				condition:    "targets.items[0].name == \"MyTarget\"",
			},
			{
				wantedReason: cleanerv1alpha1.ConditionReasonResultNotBoolean,
				condition:    "2",
			},
		}

		for _, tc := range tcs {
			curTc := tc
			It("Reports "+tc.wantedReason, func() {
				By("By creating a cTTL which should have Reason = " + curTc.wantedReason)
				name := strings.ToLower("wanted-reason-" + curTc.wantedReason)
				cTTL := &cleanerv1alpha1.ConditionalTTL{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "cleaner.vtex.io/v1alpha1",
						Kind:       "ConditionalTTL",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: ConditionalTTLNamespace,
					},
					Spec: cleanerv1alpha1.ConditionalTTLSpec{
						TTL: &metav1.Duration{Duration: 0 * time.Second},
						Retry: &cleanerv1alpha1.RetryConfig{
							Period: &metav1.Duration{Duration: 1 * time.Hour},
						},
						Targets: []cleanerv1alpha1.Target{
							{
								Name:                  "targets",
								IncludeWhenEvaluating: true,
								Reference: cleanerv1alpha1.TargetReference{
									TypeMeta: metav1.TypeMeta{
										APIVersion: "v1",
										Kind:       "Pod",
									},
									LabelSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											"foo": "bar",
										},
									},
								},
							},
						},
						Conditions: []string{curTc.condition},
					},
				}
				Expect(k8sClient.Create(ctx, cTTL)).Should(Succeed())

				cTTLLookupKey := types.NamespacedName{
					Name:      name,
					Namespace: ConditionalTTLNamespace,
				}
				createdCTTL := &cleanerv1alpha1.ConditionalTTL{}
				var readyCondition *metav1.Condition

				Eventually(func() bool {
					err := k8sClient.Get(ctx, cTTLLookupKey, createdCTTL)
					if err != nil {
						return false
					}
					readyCondition = apimeta.FindStatusCondition(createdCTTL.Status.Conditions, cleanerv1alpha1.ConditionTypeReady)
					if readyCondition == nil {
						return false
					}

					return true
				}, timeout, interval).Should(BeTrue())

				Expect(readyCondition.Status).Should(Equal(metav1.ConditionFalse))
				Expect(readyCondition.Reason).Should(Equal(curTc.wantedReason))
				Expect(len(createdCTTL.Finalizers)).Should(Equal(0))

				Expect(k8sClient.Delete(ctx, cTTL)).Should(Succeed())
			})
		}
	})
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

func buildPod(name string) *v1.Pod {
	return &v1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: TargetPodNamespace,
			Labels: map[string]string{
				LabelSelectorKey: LabelSelectorValue,
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "test-container",
					Image: "test-image",
				},
			},
		},
	}

}

// wrappers required for the Helm client to work with envtest
var _ genericclioptions.RESTClientGetter = &clientWrapper{}

type clientWrapper struct {
	client client.Client
	cfg    *rest.Config
}

func (cw *clientWrapper) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return &clientCmdWrapper{}
}

func (cw *clientWrapper) ToRESTMapper() (meta.RESTMapper, error) {
	return cw.client.RESTMapper(), nil
}

func (cw *clientWrapper) ToRESTConfig() (*rest.Config, error) {
	return cw.cfg, nil
}

func (cw *clientWrapper) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	cfg, err := cw.ToRESTConfig()
	if err != nil {
		return nil, err
	}
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return memcached.NewMemCacheClient(dc), nil
}

var _ clientcmd.ClientConfig = &clientCmdWrapper{}

type clientCmdWrapper struct{}

func (ccw *clientCmdWrapper) RawConfig() (clientcmdapi.Config, error) {
	return clientcmdapi.Config{}, nil
}

func (ccw *clientCmdWrapper) Namespace() (string, bool, error) {
	return "default", false, nil
}

func (ccw *clientCmdWrapper) ClientConfig() (*rest.Config, error) {
	return nil, nil
}

func (ccw *clientCmdWrapper) ConfigAccess() clientcmd.ConfigAccess {
	return nil
}
