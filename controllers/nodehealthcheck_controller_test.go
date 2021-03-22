package controllers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
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
				Spec:       v1alpha1.NodeHealthCheckSpec{Selector: nil},
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
				Expect(underTest.Spec.Selector).To(BeEmpty())
			})
		})
	})
	Context("Validation", func() {
		When("specifying an external remediation template", func() {
			It("should fail creation if empty", func() {

			})
			It("should fail creation malformed", func() {

			})
			It("should succeed creation with valid object format", func() {

			})
			It("should succeed creation even with a non-existing template", func() {

			})
		})
		When("specifying a selector", func() {
			It("fails creation on wrong selector input", func() {

			})
			It("succeeds creation on valid selector input", func() {

			})
		})
		When("specifying unhealthy conditions", func() {
			It("fails creation on wrong unhealthy conditions input", func() {

			})
			It("succeeds creation on valid unhealthy condition input", func() {

			})
		})
		When("specifying max unhealthy", func() {
			It("fails creation on percentage > 100%", func() {

			})
			It("fails creation on negative number", func() {

			})
			It("succeeds creation on percentage between 0%-100%", func() {

			})
		})
	})
	Context("Reconciliation", func() {
		When("few nodes are unhealthy and below max unhealthy", func() {

			It("create a remdiation CR for each unhealthy node", func() {

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
