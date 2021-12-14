package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/cluster"
)

var _ = Describe("Node Health Check CR", func() {
	Context("Defaults", func() {
		var underTest *v1alpha1.NodeHealthCheck

		BeforeEach(func() {
			underTest = &v1alpha1.NodeHealthCheck{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: v1alpha1.NodeHealthCheckSpec{
					Selector: metav1.LabelSelector{},
					RemediationTemplate: &v1.ObjectReference{
						Kind:      "InfrastructureRemediationTemplate",
						Namespace: "default",
						Name:      "template",
					},
				},
			}
			err := k8sClient.Create(context.Background(), underTest)
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			err := k8sClient.Delete(context.Background(), underTest)
			Expect(err).NotTo(HaveOccurred())
		})

		When("creating a resource", func() {
			It("CR is cluster scoped", func() {
				Expect(underTest.Namespace).To(BeEmpty())
			})

			It("sets a default conditions", func() {
				Expect(underTest.Spec.UnhealthyConditions).To(HaveLen(2))
				Expect(underTest.Spec.UnhealthyConditions[0].Type).To(Equal(v1.NodeReady))
				Expect(underTest.Spec.UnhealthyConditions[0].Status).To(Equal(v1.ConditionFalse))
				Expect(underTest.Spec.UnhealthyConditions[0].Duration).To(Equal(metav1.Duration{Duration: time.Minute * 5}))
				Expect(underTest.Spec.UnhealthyConditions[1].Type).To(Equal(v1.NodeReady))
				Expect(underTest.Spec.UnhealthyConditions[1].Status).To(Equal(v1.ConditionUnknown))
				Expect(underTest.Spec.UnhealthyConditions[1].Duration).To(Equal(metav1.Duration{Duration: time.Minute * 5}))
			})

			It("sets min healthy to 51%", func() {
				Expect(underTest.Spec.MinHealthy.StrVal).To(Equal(intstr.FromString("51%").StrVal))
			})

			It("sets an empty selector to select all nodes", func() {
				Expect(underTest.Spec.Selector.MatchLabels).To(BeEmpty())
				Expect(underTest.Spec.Selector.MatchExpressions).To(BeEmpty())
			})
		})

		When("updating status", func() {
			It("succeeds updating only part of the fields", func() {
				Expect(underTest.Status).ToNot(BeNil())
				Expect(underTest.Status.HealthyNodes).To(Equal(0))
				patch := client.MergeFrom(underTest.DeepCopy())
				underTest.Status.HealthyNodes = 1
				underTest.Status.ObservedNodes = 6
				err := k8sClient.Status().Patch(context.Background(), underTest, patch)
				Expect(err).NotTo(HaveOccurred())
				Expect(underTest.Status.HealthyNodes).To(Equal(1))
				Expect(underTest.Status.ObservedNodes).To(Equal(6))
				Expect(underTest.Status.InFlightRemediations).To(BeNil())
			})
		})

	})

	Context("Validation", func() {
		var underTest *v1alpha1.NodeHealthCheck

		BeforeEach(func() {
			underTest = &v1alpha1.NodeHealthCheck{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: v1alpha1.NodeHealthCheckSpec{
					RemediationTemplate: &v1.ObjectReference{
						Kind:      "InfrastructureRemediationTemplate",
						Namespace: "default",
						Name:      "template",
					},
				},
			}
		})

		AfterEach(func() {
			_ = k8sClient.Delete(context.Background(), underTest)
		})

		When("specifying an external remediation template", func() {
			It("should fail creation if empty", func() {
				underTest.Spec.RemediationTemplate = nil
				err := k8sClient.Create(context.Background(), underTest)
				Expect(err).To(HaveOccurred())
			})

			It("should succeed creation if a template CR doesn't exists", func() {
				err := k8sClient.Create(context.Background(), underTest)
				Expect(err).NotTo(HaveOccurred())
			})
		})
		When("specifying min healthy", func() {
			It("fails creation on percentage > 100%", func() {
				invalidPercentage := intstr.FromString("150%")
				underTest.Spec.MinHealthy = &invalidPercentage
				err := k8sClient.Create(context.Background(), underTest)
				Expect(errors.IsInvalid(err)).To(BeTrue())
			})

			It("fails creation on negative number", func() {
				Skip("TODO find out how to validate a minimum for IntOrString")
			})

			It("succeeds creation on percentage between 0%-100%", func() {
				validPercentage := intstr.FromString("30%")
				underTest.Spec.MinHealthy = &validPercentage
				err := k8sClient.Create(context.Background(), underTest)
				Expect(errors.IsInvalid(err)).To(BeFalse())
			})
		})
	})

	Context("Reconciliation", func() {
		var (
			underTest       *v1alpha1.NodeHealthCheck
			objects         []runtime.Object
			reconciler      NodeHealthCheckReconciler
			upgradeChecker  fakeClusterUpgradeChecker
			reconcileError  error
			reconcileResult controllerruntime.Result
			getNHCError     error
		)

		var setupObjects = func(unhealthy int, healthy int) {
			objects = newNodes(unhealthy, healthy)
			underTest = newNodeHealthCheck()
			remediationTemplate := newRemediationTemplate()
			objects = append(objects, underTest, remediationTemplate)
		}

		JustBeforeEach(func() {
			client := fake.NewClientBuilder().WithRuntimeObjects(objects...).Build()
			dynamicClient := newDynamicClient()
			reconciler = NodeHealthCheckReconciler{
				Client:                      client,
				DynamicClient:               dynamicClient,
				Log:                         controllerruntime.Log,
				Scheme:                      scheme.Scheme,
				clusterUpgradeStatusChecker: &upgradeChecker,
				recorder:                    record.NewFakeRecorder(3),
			}
			reconcileResult, reconcileError = reconciler.Reconcile(
				context.Background(),
				controllerruntime.Request{NamespacedName: types.NamespacedName{Name: underTest.Name}})
			getNHCError = reconciler.Get(
				context.Background(),
				ctrlruntimeclient.ObjectKey{Namespace: underTest.Namespace, Name: underTest.Name},
				underTest)
		})

		When("few nodes are unhealthy and healthy nodes meet min healthy", func() {
			BeforeEach(func() {
				setupObjects(1, 2)
			})

			It("create a remediation CR for each unhealthy node", func() {
				Expect(reconcileError).NotTo(HaveOccurred())
				cr := newRemediationCR("unhealthy-node-1")
				o, err := reconciler.DynamicClient.Resource(crToResource(cr)).
					Namespace(cr.GetNamespace()).
					Get(context.Background(), cr.GetName(), metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				Expect(o.Object).To(ContainElement(map[string]interface{}{"size": "foo"}))
				Expect(o.GetOwnerReferences()).
					To(ContainElement(metav1.OwnerReference{
						Kind:       underTest.Kind,
						APIVersion: underTest.APIVersion,
						Name:       underTest.Name,
						Controller: pointer.BoolPtr(false),
					}))
				Expect(o.GetAnnotations()[oldRemediationCRAnnotationKey]).To(BeEmpty())
			})

			It("updates the NHC status with number of healthy nodes", func() {
				Expect(reconcileError).NotTo(HaveOccurred())
				Expect(getNHCError).NotTo(HaveOccurred())
				Expect(underTest.Status.HealthyNodes).To(Equal(2))
			})

			It("updates the NHC status with number of observed nodes", func() {
				Expect(reconcileError).NotTo(HaveOccurred())
				Expect(getNHCError).NotTo(HaveOccurred())
				Expect(underTest.Status.ObservedNodes).To(Equal(3))
			})

			It("updates the NHC status with in-flight remediations", func() {
				Expect(reconcileError).NotTo(HaveOccurred())
				Expect(getNHCError).NotTo(HaveOccurred())
				Expect(underTest.Status.InFlightRemediations).NotTo(BeEmpty())
			})
		})

		When("few nodes are unhealthy and healthy nodes above min healthy", func() {
			BeforeEach(func() {
				setupObjects(4, 3)
			})

			It("skips remediation - CR is not created", func() {
				Expect(reconcileError).NotTo(HaveOccurred())
				o := newRemediationCR("unhealthy-node-1")
				err := reconciler.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: o.GetNamespace(),
					Name: o.GetName()}, &o)
				Expect(errors.IsNotFound(err)).To(BeTrue())
			})

			It("updates the NHC status with number of healthy nodes", func() {
				Expect(reconcileError).NotTo(HaveOccurred())
				Expect(getNHCError).NotTo(HaveOccurred())
				Expect(underTest.Status.HealthyNodes).To(Equal(3))
			})

			It("updates the NHC status with number of observed nodes", func() {
				Expect(reconcileError).NotTo(HaveOccurred())
				Expect(getNHCError).NotTo(HaveOccurred())
				Expect(underTest.Status.ObservedNodes).To(Equal(7))
			})
		})

		When("few nodes become healthy", func() {
			BeforeEach(func() {
				setupObjects(1, 2)
				remediationCR := newRemediationCR("healthy-node-2")
				objects = append(objects, remediationCR.DeepCopyObject())
			})

			It("deletes an existing remediation CR", func() {
				Expect(reconcileError).NotTo(HaveOccurred())
				Expect(getNHCError).NotTo(HaveOccurred())

				cr := newRemediationCR("unhealthy-node-1")
				_, err := reconciler.DynamicClient.
					Resource(crToResource(cr)).
					Namespace(cr.GetNamespace()).
					Get(context.Background(), cr.GetName(), metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())

				cr = newRemediationCR("unhealthy-node-2")
				_, err = reconciler.DynamicClient.
					Resource(crToResource(cr)).
					Namespace(cr.GetNamespace()).
					Get(context.Background(), cr.GetName(), metav1.GetOptions{})
				Expect(errors.IsNotFound(err)).To(BeTrue())
			})

			It("updates the NHC status with number of healthy nodes", func() {
				Expect(reconcileError).NotTo(HaveOccurred())
				Expect(getNHCError).NotTo(HaveOccurred())
				Expect(underTest.Status.HealthyNodes).To(Equal(2))
			})
		})

		When("an old remediation cr exist", func() {
			BeforeEach(func() {
				setupObjects(1, 2)
				remediationCR := newRemediationCR("unhealthy-node-1")
				remediationCR.SetCreationTimestamp(metav1.Time{Time: time.Now().Add(-remediationCRAlertTimeout - 2*time.Minute)})
				objects = append(objects, remediationCR.DeepCopyObject())
			})

			It("an alert flag is set on remediation cr", func() {
				Expect(reconcileError).NotTo(HaveOccurred())
				Expect(getNHCError).NotTo(HaveOccurred())

				actualRemediationCR := new(unstructured.Unstructured)
				actualRemediationCR.SetKind(strings.TrimSuffix(underTest.Spec.RemediationTemplate.Kind, templateSuffix))
				actualRemediationCR.SetAPIVersion(underTest.Spec.RemediationTemplate.APIVersion)
				key := client.ObjectKey{Name: "unhealthy-node-1", Namespace: "default"}
				err := reconciler.Client.Get(context.Background(), key, actualRemediationCR)
				Expect(err).NotTo(HaveOccurred())
				Expect(actualRemediationCR.GetAnnotations()[oldRemediationCRAnnotationKey]).To(Equal("flagon"))
			})
		})

		When("remediation is needed but pauseRequests exists", func() {
			BeforeEach(func() {
				setupObjects(1, 2)
				underTest.Spec.PauseRequests = []string{"I'm an admin, asking you to stop remediating this group of nodes"}
			})

			It("should reconcile successfully", func() {
				Expect(reconcileError).ShouldNot(HaveOccurred())
			})

			It("skips remediation - CR is not created", func() {
				o := newRemediationCR("unhealthy-node-1")
				err := reconciler.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: o.GetNamespace(),
					Name: o.GetName()}, &o)
				Expect(errors.IsNotFound(err)).To(BeTrue())
			})

			It("updates the NHC status", func() {
				Expect(getNHCError).NotTo(HaveOccurred())
				Expect(underTest.Status.HealthyNodes).To(Equal(2))
				Expect(underTest.Status.ObservedNodes).To(Equal(3))
			})
		})

		When("Nodes are candidates for remediation and cluster is upgrading", func() {
			BeforeEach(func() {
				objects = newNodes(1, 2)
				underTest = newNodeHealthCheck()
				upgradeChecker = fakeClusterUpgradeChecker{upgrading: true}
				remediationTemplate := newRemediationTemplate()
				remediationCR := newRemediationCR("unhealthy-node-1")
				objects = append(objects, underTest, remediationTemplate, remediationCR.DeepCopyObject())
			})

			It("requeues reconciliation to 1 minute from now", func() {
				Expect(reconcileError).NotTo(HaveOccurred())
				Expect(reconcileResult.RequeueAfter).To(Equal(1 * time.Minute))
			})

			It("does not remediate any node", func() {
				Expect(underTest.Status.InFlightRemediations).To(HaveLen(0))
			})

			It("still updates the status", func() {
				// we have 1 unhealthy and 2 healthy here
				Expect(underTest.Status.HealthyNodes).To(Equal(2))
				Expect(underTest.Status.ObservedNodes).To(Equal(3))
			})
		})
	})

	Context("Controller Watches", func() {
		var (
			underTest1 *v1alpha1.NodeHealthCheck
			underTest2 *v1alpha1.NodeHealthCheck
			objects    []runtime.Object
			client     ctrlruntimeclient.Client
		)

		JustBeforeEach(func() {
			client = fake.NewClientBuilder().WithRuntimeObjects(objects...).Build()
		})

		When("a node changes status and is selectable by one NHC selector", func() {
			BeforeEach(func() {
				objects = newNodes(3, 10)
				underTest1 = newNodeHealthCheck()
				underTest2 = newNodeHealthCheck()
				underTest2.Name = "test-2"
				emptySelector, _ := metav1.ParseToLabelSelector("fooLabel=bar")
				underTest2.Spec.Selector = *emptySelector
				objects = append(objects, underTest1, underTest2)
			})

			It("creates a reconcile request", func() {
				handler := nhcByNodeMapperFunc(client, controllerruntime.Log)
				updatedNode := v1.Node{
					ObjectMeta: controllerruntime.ObjectMeta{Name: "healthy-node-1"},
				}
				requests := handler(&updatedNode)
				Expect(len(requests)).To(Equal(1))
				Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: underTest1.GetName()}}))
			})
		})

		When("a node changes status and is selectable by the more 2 NHC selector", func() {
			BeforeEach(func() {
				objects = newNodes(3, 10)
				underTest1 = newNodeHealthCheck()
				underTest2 = newNodeHealthCheck()
				underTest2.Name = "test-2"
				objects = append(objects, underTest1, underTest2)
			})

			It("creates 2 reconcile requests", func() {
				handler := nhcByNodeMapperFunc(client, controllerruntime.Log)
				updatedNode := v1.Node{
					ObjectMeta: controllerruntime.ObjectMeta{Name: "healthy-node-1"},
				}
				requests := handler(&updatedNode)
				Expect(len(requests)).To(Equal(2))
				Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: underTest1.GetName()}}))
				Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: underTest2.GetName()}}))
			})
		})
		When("a node changes status and is and there are no NHC objects", func() {
			BeforeEach(func() {
				objects = newNodes(3, 10)
			})

			It("doesn't create reconcile requests", func() {
				handler := nhcByNodeMapperFunc(client, controllerruntime.Log)
				updatedNode := v1.Node{
					ObjectMeta: controllerruntime.ObjectMeta{Name: "healthy-node-1"},
				}
				requests := handler(&updatedNode)
				Expect(requests).To(BeEmpty())
			})
		})
	})
})

func newRemediationCR(nodeName string) unstructured.Unstructured {
	cr := unstructured.Unstructured{}
	cr.SetName(nodeName)
	cr.SetNamespace("default")
	cr.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   TestRemediationCRD.Spec.Group,
		Version: TestRemediationCRD.Spec.Versions[0].Name,
		Kind:    TestRemediationCRD.Spec.Names.Kind,
	})
	return cr
}

func newRemediationTemplate() runtime.Object {
	r := map[string]interface{}{
		"kind":       "InfrastructureRemediation",
		"apiVersion": "medik8s.io/v1alpha1",
		"metadata":   map[string]interface{}{},
		"spec": map[string]interface{}{
			"size": "foo",
		},
	}
	template := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"template": r,
			},
		},
	}
	template.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "medik8s.io",
		Version: "v1alpha1",
		Kind:    "InfrastructureRemediationTemplate",
	})
	template.SetGenerateName("remediation-template-name-")
	template.SetNamespace("default")
	template.SetName("template")
	return template.DeepCopyObject()
}

func newNodeHealthCheck() *v1alpha1.NodeHealthCheck {
	unhealthy := intstr.FromString("51%")
	return &v1alpha1.NodeHealthCheck{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NodeHealthCheck",
			APIVersion: "medik8s.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: v1alpha1.NodeHealthCheckSpec{
			Selector:   metav1.LabelSelector{},
			MinHealthy: &unhealthy,
			UnhealthyConditions: []v1alpha1.UnhealthyCondition{
				{
					Type:     v1.NodeReady,
					Status:   v1.ConditionFalse,
					Duration: metav1.Duration{Duration: time.Second * 300},
				},
			},
			RemediationTemplate: &v1.ObjectReference{
				Kind:       "InfrastructureRemediationTemplate",
				APIVersion: "medik8s.io/v1alpha1",
				Namespace:  "default",
				Name:       "template",
			},
		},
	}
}

func newNodes(unhealthy int, healthy int) []runtime.Object {
	o := make([]runtime.Object, 0, healthy+unhealthy)
	for i := unhealthy; i > 0; i-- {
		node := newNode(fmt.Sprintf("unhealthy-node-%d", i), v1.NodeReady, v1.ConditionFalse, time.Minute*10)
		o = append(o, node)
	}
	for i := healthy; i > 0; i-- {
		o = append(o, newNode(fmt.Sprintf("healthy-node-%d", i), v1.NodeReady, v1.ConditionTrue, time.Minute*10))
	}
	return o
}

func newNode(name string, t v1.NodeConditionType, s v1.ConditionStatus, d time.Duration) runtime.Object {
	return runtime.Object(
		&v1.Node{
			TypeMeta:   metav1.TypeMeta{Kind: "Node"},
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status: v1.NodeStatus{
				Conditions: []v1.NodeCondition{
					{
						Type:               t,
						Status:             s,
						LastTransitionTime: metav1.Time{Time: time.Now().Add(-d)},
					},
				},
			},
		})

}

var TestRemediationCRD = &apiextensions.CustomResourceDefinition{
	TypeMeta: metav1.TypeMeta{
		APIVersion: apiextensions.SchemeGroupVersion.String(),
		Kind:       "CustomResourceDefinition",
	},
	ObjectMeta: metav1.ObjectMeta{
		Name: "infrastructureremediations.medik8s.io",
	},
	Spec: apiextensions.CustomResourceDefinitionSpec{
		Group: "medik8s.io",
		Scope: apiextensions.NamespaceScoped,
		Names: apiextensions.CustomResourceDefinitionNames{
			Kind:   "InfrastructureRemediation",
			Plural: "infrastructureremediations",
		},
		Versions: []apiextensions.CustomResourceDefinitionVersion{
			{
				Name:    "v1alpha1",
				Served:  true,
				Storage: true,
				Subresources: &apiextensions.CustomResourceSubresources{
					Status: &apiextensions.CustomResourceSubresourceStatus{},
				},
				Schema: &apiextensions.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensions.JSONSchemaProps{
						Type: "object",
						Properties: map[string]apiextensions.JSONSchemaProps{
							"spec": {
								Type:                   "object",
								XPreserveUnknownFields: pointer.BoolPtr(true),
							},
							"status": {
								Type:                   "object",
								XPreserveUnknownFields: pointer.BoolPtr(true),
							},
						},
					},
				},
			},
		},
	},
}

func newDynamicClient() *dynamicfake.FakeDynamicClient {
	gvrToListKind := make(map[schema.GroupVersionResource]string)
	gvrToListKind[schema.GroupVersionResource{
		Group:    TestRemediationCRD.Spec.Group,
		Version:  TestRemediationCRD.Spec.Versions[0].Name,
		Resource: TestRemediationCRD.Spec.Names.Plural,
	}] = TestRemediationCRD.Spec.Names.Kind + "List"
	// updated fake client needs mapping ahead of time - see https://github.com/kubernetes/client-go/issues/949#issuecomment-811154420
	c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme.Scheme, gvrToListKind)
	c.PrependReactor("create", "*", func(action testing.Action) (handled bool, ret runtime.Object, err error) {
		createAction := action.(testing.CreateAction)
		object := createAction.GetObject()
		accessor, err := meta.Accessor(object)
		if err != nil {
			return false, object, err
		}
		accessor.SetCreationTimestamp(metav1.Now())
		accessor.SetUID(types.UID(fmt.Sprintf("FAKE-UID-%s", accessor.GetCreationTimestamp())))
		return false, object, nil
	})
	return c
}

type fakeClusterUpgradeChecker struct {
	upgrading bool
	err       error
}

// force implementation of interface
var _ cluster.UpgradeChecker = fakeClusterUpgradeChecker{}

func (c fakeClusterUpgradeChecker) Check() (bool, error) {
	return c.upgrading, c.err
}
