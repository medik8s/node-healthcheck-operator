package controllers

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/pointer"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

var _ = Describe("Node Health Check CR", func() {
	var (
		reconciler NodeHealthCheckReconciler
		client     ctrlruntimeclient.Client
	)
	Context("Defaults", func() {
		var underTest *v1alpha1.NodeHealthCheck
		client = k8sClient
		reconciler = NodeHealthCheckReconciler{
			Client: client,
			Log:    controllerruntime.Log,
			Scheme: scheme.Scheme,
		}
		BeforeEach(func() {
			underTest = &v1alpha1.NodeHealthCheck{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: v1alpha1.NodeHealthCheckSpec{
					Selector: metav1.LabelSelector{},
					ExternalRemediationTemplate: &v1.ObjectReference{
						Kind:      "InfrastructureRemediationTemplate",
						Namespace: "default",
						Name:      "template",
					},
				},
			}
			err := k8sClient.Create(context.Background(), underTest)
			reconciler.Log.Info("created resource NHC")
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
			It("sets a default condition Unready 300s", func() {
				Expect(underTest.Spec.UnhealthyConditions).To(HaveLen(1))
				Expect(underTest.Spec.UnhealthyConditions[0].Type).To(Equal(v1.NodeReady))
				Expect(underTest.Spec.UnhealthyConditions[0].Status).To(Equal(v1.ConditionFalse))
				Expect(underTest.Spec.UnhealthyConditions[0].Duration).To(Equal(metav1.Duration{Duration: time.Minute * 5}))

			})
			It("sets max unhealthy to 49%", func() {
				Expect(underTest.Spec.MaxUnhealthy.StrVal).To(Equal(intstr.FromString("49%").StrVal))
			})
			It("sets an empty selector to select all nodes", func() {
				Expect(underTest.Spec.Selector.MatchLabels).To(BeEmpty())
				Expect(underTest.Spec.Selector.MatchExpressions).To(BeEmpty())
			})
		})
	})
	Context("Validation", func() {
		var underTest *v1alpha1.NodeHealthCheck
		BeforeEach(func() {
			underTest = &v1alpha1.NodeHealthCheck{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: v1alpha1.NodeHealthCheckSpec{
					ExternalRemediationTemplate: &v1.ObjectReference{
						Kind:      "InfrastructureRemediationTemplate",
						Namespace: "default",
						Name:      "template",
					},
				},
			}
		})
		AfterEach(func() {
			k8sClient.Delete(context.Background(), underTest)
		})
		When("specifying an external remediation template", func() {
			It("should fail creation if empty", func() {
				underTest.Spec.ExternalRemediationTemplate = nil
				err := k8sClient.Create(context.Background(), underTest)
				Expect(err).To(HaveOccurred())
			})
			It("should succeed creation with with non existent template", func() {
				err := k8sClient.Create(context.Background(), underTest)
				Expect(err).NotTo(HaveOccurred())
			})
		})
		When("specifying max unhealthy", func() {
			It("fails creation on percentage > 100%", func() {
				invalidPercentage := intstr.FromString("150%")
				underTest.Spec.MaxUnhealthy = &invalidPercentage
				err := k8sClient.Create(context.Background(), underTest)
				Expect(errors.IsInvalid(err)).To(BeTrue())
			})
			It("fails creation on negative number", func() {
				Skip("TODO how to make a minimum validation for IntOrString")
			})
			It("succeeds creation on percentage between 0%-100%", func() {
				validPercentage := intstr.FromString("30%")
				underTest.Spec.MaxUnhealthy = &validPercentage
				err := k8sClient.Create(context.Background(), underTest)
				Expect(errors.IsInvalid(err)).To(BeFalse())
			})
		})
	})
	Context("Reconciliation", func() {
		var (
			underTest *v1alpha1.NodeHealthCheck
			objects   []runtime.Object
		)

		JustBeforeEach(func() {
			client := fake.NewClientBuilder().WithRuntimeObjects(objects...).Build()
			reconciler = NodeHealthCheckReconciler{Client: client, Log: controllerruntime.Log, Scheme: scheme.Scheme}
			_, err := reconciler.Reconcile(
				context.Background(),
				controllerruntime.Request{NamespacedName: types.NamespacedName{Name: underTest.Name}})
			Expect(err).NotTo(HaveOccurred())
		})

		When("few nodes are unhealthy and below max unhealthy", func() {
			BeforeEach(func() {
				objects = newNodes(1, 2)
				underTest = newNodeHealthCheck()
				remediationTemplate := newRemediationTemplate()
				objects = append(objects, underTest, remediationTemplate)
			})

			It("create a remediation CR for each unhealthy node", func() {
				o := newRemediationCR()
				err := reconciler.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: o.GetNamespace(),
					Name: o.GetName()}, &o)
				Expect(err).NotTo(HaveOccurred())
				Expect(o.Object).To(ContainElement(map[string]interface{}{"size": "foo"}))
			})

			It("updates the NHC status with number of healthy nodes", func() {
				updatedNHC := v1alpha1.NodeHealthCheck{}
				reconciler.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: underTest.Namespace, Name: underTest.Name}, &updatedNHC)
				Expect(updatedNHC.Status.HealthyNodes).To(Equal(2))
			})

			It("updates the NHC status with number of observed nodes", func() {
				updatedNHC := v1alpha1.NodeHealthCheck{}
				reconciler.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: underTest.Namespace, Name: underTest.Name}, &updatedNHC)
				Expect(updatedNHC.Status.ObservedNodes).To(Equal(3))
			})

		})

		When("few nodes are unhealthy and above max unhealthy", func() {
			BeforeEach(func() {
				objects = newNodes(4, 3)
				underTest = newNodeHealthCheck()
				remediationTemplate := newRemediationTemplate()
				objects = append(objects, underTest, remediationTemplate)
			})

			It("skips remediation - CR is not created", func() {
				o := newRemediationCR()
				err := reconciler.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: o.GetNamespace(),
					Name: o.GetName()}, &o)
				Expect(errors.IsNotFound(err)).To(BeTrue())
			})

			It("updates the NHC status with number of healthy nodes", func() {
				updatedNHC := v1alpha1.NodeHealthCheck{}
				reconciler.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: underTest.Namespace, Name: underTest.Name}, &updatedNHC)
				Expect(updatedNHC.Status.HealthyNodes).To(Equal(3))
			})

			It("updates the NHC status with number of observed nodes", func() {
				updatedNHC := v1alpha1.NodeHealthCheck{}
				reconciler.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: underTest.Namespace, Name: underTest.Name}, &updatedNHC)
				Expect(updatedNHC.Status.ObservedNodes).To(Equal(7))
			})
		})

		When("few nodes become healthy", func() {
			It("deletes an existing remediation CR", func() {})
			It("updates the NHC status with number of healthy nodes", func() {})
		})
	})

})

func newRemediationCR() unstructured.Unstructured {
	cr := unstructured.Unstructured{}
	cr.SetName("unhealthy-node-1")
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
	unhealthy := intstr.FromString("49%")
	return &v1alpha1.NodeHealthCheck{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NodeHealthCheck",
			APIVersion: "medik8s.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: v1alpha1.NodeHealthCheckSpec{
			Selector:     metav1.LabelSelector{},
			MaxUnhealthy: &unhealthy,
			UnhealthyConditions: []v1alpha1.UnhealthyCondition{
				{
					Type:     v1.NodeReady,
					Status:   v1.ConditionFalse,
					Duration: metav1.Duration{Duration: time.Second * 300},
				},
			},
			ExternalRemediationTemplate: &v1.ObjectReference{
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

var TestRemediationTemplateCRD = &apiextensions.CustomResourceDefinition{
	TypeMeta: metav1.TypeMeta{
		APIVersion: apiextensions.SchemeGroupVersion.String(),
		Kind:       "CustomResourceDefinition",
	},
	ObjectMeta: metav1.ObjectMeta{
		Name: "infrastructureremediationtemplates.medik8s.io",
	},
	Spec: apiextensions.CustomResourceDefinitionSpec{
		Group: "medik8s.io",
		Scope: apiextensions.NamespaceScoped,
		Names: apiextensions.CustomResourceDefinitionNames{
			Kind:   "InfrastructureRemediationTemplate",
			Plural: "infrastructureremediationtemplates",
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
