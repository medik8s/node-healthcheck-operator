package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var _ = Describe("NodeHealthCheck Validation", func() {

	Context("a NHC", func() {

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

})
