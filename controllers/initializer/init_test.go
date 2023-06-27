package initializer

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/defaults"
)

var _ = Describe("Init", func() {

	JustBeforeEach(func() {
		initializer := New(k8sManager, ctrl.Log.WithName("test initializer"))
		Expect(initializer.Start(context.Background())).To(Succeed())
	})

	AfterEach(func() {
		// delete default config for next test
		nhc := &v1alpha1.NodeHealthCheck{}
		Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: defaults.DefaultCRName}, nhc)).To(Succeed())
		Expect(k8sClient.Delete(context.Background(), nhc)).To(Succeed())
	})

	expectUpToDateDefaultConfig := func(nhc *v1alpha1.NodeHealthCheck) {
		ExpectWithOffset(1, nhc.Name).To(Equal(defaults.DefaultCRName))
		ExpectWithOffset(1, nhc.Spec.RemediationTemplate).To(Equal(defaults.DefaultTemplateRef))
		ExpectWithOffset(1, nhc.Spec.Selector).To(Equal(defaults.DefaultSelector))
	}

	When("initialization is called on upgrade", func() {

		BeforeEach(func() {
			// create outdated default config
			outdatedNHC := &v1alpha1.NodeHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name: defaults.DefaultCRName,
				},
				Spec: v1alpha1.NodeHealthCheckSpec{
					Selector: metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{{
							Key:      "node-role.kubernetes.io/worker",
							Operator: metav1.LabelSelectorOpExists,
						}},
					},
					RemediationTemplate: &corev1.ObjectReference{
						Kind:       "PoisonPillRemediationTemplate",
						APIVersion: "poison-pill.medik8s.io/v1alpha1",
						Name:       "poison-pill-default-template",
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), outdatedNHC)).To(Succeed())
		})

		It("updates the default NHC resource", func() {
			nhc := &v1alpha1.NodeHealthCheck{}
			Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: defaults.DefaultCRName}, nhc)).To(Succeed())
			expectUpToDateDefaultConfig(nhc)
		})
	})

	When("initialization is called on a NHC with escalating remediation", func() {

		BeforeEach(func() {
			// create outdated default config
			outdatedNHC := &v1alpha1.NodeHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name: defaults.DefaultCRName,
				},
				Spec: v1alpha1.NodeHealthCheckSpec{
					Selector: metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{{
							Key:      "node-role.kubernetes.io/worker",
							Operator: metav1.LabelSelectorOpExists,
						}},
					},
					EscalatingRemediations: []v1alpha1.EscalatingRemediation{
						{
							RemediationTemplate: corev1.ObjectReference{
								Kind:       "PoisonPillRemediationTemplate",
								APIVersion: "poison-pill.medik8s.io/v1alpha1",
								Name:       "poison-pill-default-template",
							},
							Order:   0,
							Timeout: metav1.Duration{},
						},
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), outdatedNHC)).To(Succeed())
		})

		It("should not panic and not touch remediation", func() {
			nhc := &v1alpha1.NodeHealthCheck{}
			Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: defaults.DefaultCRName}, nhc)).To(Succeed())
			Expect(nhc.Name).To(Equal(defaults.DefaultCRName))
			Expect(nhc.Spec.EscalatingRemediations[0].RemediationTemplate.Kind).To(Equal("PoisonPillRemediationTemplate"))
			Expect(nhc.Spec.RemediationTemplate).To(BeNil())
		})
	})
})
