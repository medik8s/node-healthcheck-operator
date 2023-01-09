package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var _ = Describe("NodeHealthCheck Validation", func() {

	Context("Creating or updating a NHC", func() {

		var nhc *NodeHealthCheck

		BeforeEach(func() {
			mh := intstr.FromString("51%")
			nhc = &NodeHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: NodeHealthCheckSpec{
					MinHealthy: &mh,
					Selector: metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "node-role.kubernetes.io/control-plane",
								Operator: metav1.LabelSelectorOpDoesNotExist,
							},
						},
					},
				},
			}
		})

		Context("with valid config", func() {
			It("should be allowed", func() {
				Expect(nhc.validate()).To(Succeed())
			})
		})

		Context("with negative minHealthy", func() {
			BeforeEach(func() {
				mh := intstr.FromInt(-1)
				nhc.Spec.MinHealthy = &mh
			})

			It("should be denied", func() {
				Expect(nhc.validate()).To(MatchError(ContainSubstring(minHealthyError)))
			})
		})

		Context("with invalid selector", func() {
			BeforeEach(func() {
				selector := metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key: "node-role.kubernetes.io/control-plane",
							// LabelSelectorOpIn needs a value
							Operator: metav1.LabelSelectorOpIn,
						},
					},
				}
				nhc.Spec.Selector = selector
			})

			It("should be denied", func() {
				Expect(nhc.validate()).To(MatchError(ContainSubstring(invalidSelectorError)))
			})
		})
	})

	Context("During ongoing remediation", func() {

		var nhcOld *NodeHealthCheck
		var nhcNew *NodeHealthCheck

		BeforeEach(func() {
			mh := intstr.FromString("51%")
			nhcOld = &NodeHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Spec: NodeHealthCheckSpec{
					MinHealthy: &mh,
					Selector: metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "node-role.kubernetes.io/control-plane",
								Operator: metav1.LabelSelectorOpDoesNotExist,
							},
						},
					},
					RemediationTemplate: &v1.ObjectReference{
						Name: "OldName",
					},
				},
				Status: NodeHealthCheckStatus{
					InFlightRemediations: map[string]metav1.Time{
						"test": metav1.Now(),
					},
				},
			}
		})

		Context("updating selector", func() {
			BeforeEach(func() {
				nhcNew = nhcOld.DeepCopy()
				nhcNew.Spec.Selector.MatchExpressions[0].Key = "node-role.kubernetes.io/infra"
			})
			It("should be denied", func() {
				Expect(nhcNew.ValidateUpdate(nhcOld)).To(MatchError(
					And(
						ContainSubstring(OngoingRemediationError),
						ContainSubstring("selector"),
					),
				))
			})
		})

		Context("updating template", func() {
			BeforeEach(func() {
				nhcNew = nhcOld.DeepCopy()
				nhcNew.Spec.RemediationTemplate.Name = "newName"
			})
			It("should be denied", func() {
				Expect(nhcNew.ValidateUpdate(nhcOld)).To(MatchError(
					And(
						ContainSubstring(OngoingRemediationError),
						ContainSubstring("remediation template"),
					),
				))
			})
		})
	})
})
