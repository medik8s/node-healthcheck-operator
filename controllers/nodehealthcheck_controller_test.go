package controllers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

var _ = Describe("Node Health Check CR", func() {
	var (
		reconciler NodeHealthCheckReconciler
		client     client.Client
	)
	BeforeEach(func() {
		client = k8sClient
		reconciler = NodeHealthCheckReconciler{
			Client: client,
			Log:    controllerruntime.Log,
			Scheme: scheme.Scheme,
		}
	})
	Context("Defaults", func() {
		var underTest *v1alpha1.NodeHealthCheck
		BeforeEach(func() {
			underTest = &v1alpha1.NodeHealthCheck{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: v1alpha1.NodeHealthCheckSpec{
					Selector: nil,
					ExternalRemediationTemplate: &v1.ObjectReference{
						Kind:      "some.kind.kindTemplate",
						Namespace: "ns",
						Name:      "testtemplate",
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
				Expect(underTest.Spec.Selector).To(BeNil())
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
						Kind:      "some.kind.kindTemplate",
						Namespace: "ns",
						Name:      "testtemplate",
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
				invalidPercenteage := intstr.FromString("150%")
				underTest.Spec.MaxUnhealthy = &invalidPercenteage
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
				Expect(errors.IsInvalid(err)).NotTo(BeTrue())
			})
		})
	})
	Context("Reconciliation", func() {
			nodes := []v1.nodes{
				ObjectMeta: metav1.ObjectMeta{Name: "worker-0"},
				Status:     v1.NodeStatus{
					Conditions:      []v1.NodeCondition{
						{
							Type:               v1.NodeReady,
							Status:             v1.ConditionFalse,
							LastTransitionTime: metav1.Time{Time: time.Now().Add(-time.Minute * 10)},
							Reason:             "",
							Message:            "",
					},
				},
			},
		})
		var underTest *v1alpha1.NodeHealthCheck
		BeforeEach(func() {
			underTest = &v1alpha1.NodeHealthCheck{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: v1alpha1.NodeHealthCheckSpec{
					Selector: nil,
					ExternalRemediationTemplate: &v1.ObjectReference{
						Kind:      "some.kind.kindTemplate",
						Namespace: "ns",
						Name:      "testtemplate",
					},
				},
			}
		})
		AfterEach(func() {
			err := k8sClient.Delete(context.Background(), underTest)
			Expect(err).NotTo(HaveOccurred())
		})
		When("few nodes are unhealthy and below max unhealthy", func() {
			It("create a remediation CR for each unhealthy node", func() {

			})
			It("updates the NHC status with number of healthy nodes", func() {})
			It("updates the NHC status with number of observed nodes", func() {})
		})
		When("few nodes are unhealthy and above max unhealthy", func() {
			It("skips remediation - no CR is created", func() {

			})
			It("deletes a remediation CR for if a node gone healthy", func() {})
			It("updates the NHC status with number of healthy nodes", func() {})
			It("updates the NHC status with number of observed nodes", func() {})
		})
		When("few nodes become healthy", func() {
			It("deletes an existing remediation CR", func() {})
			It("updates the NHC status with number of healthy nodes", func() {})
		})
	})

})
