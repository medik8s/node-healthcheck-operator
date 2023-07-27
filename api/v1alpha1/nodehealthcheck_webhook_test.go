package v1alpha1

import (
	"time"

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
					RemediationTemplate: &v1.ObjectReference{
						Kind:       "R",
						Namespace:  "dummy",
						Name:       "r",
						APIVersion: "r",
					},
				},
			}
		})

		Context("with valid config and remediation template", func() {
			It("should be allowed", func() {
				Expect(nhc.validate()).To(Succeed())
			})
		})

		Context("with valid config and escalating remediations", func() {
			BeforeEach(func() {
				setEscalatingRemediations(nhc)
			})

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

		Context("with empty selector", func() {
			BeforeEach(func() {
				selector := metav1.LabelSelector{}
				nhc.Spec.Selector = selector
			})

			It("should be denied", func() {
				Expect(nhc.validate()).To(MatchError(ContainSubstring(missingSelectorError)))
			})
		})

		Context("with neither remediation template or escalating remediations set", func() {
			BeforeEach(func() {
				nhc.Spec.RemediationTemplate = nil
				nhc.Spec.EscalatingRemediations = []EscalatingRemediation{}
			})
			It("should be denied", func() {
				Expect(nhc.validate()).To(MatchError(ContainSubstring(mandatoryRemediationError)))
			})
		})

		Context("with both remediation template and escalating remediations set", func() {
			BeforeEach(func() {
				templ := nhc.Spec.RemediationTemplate
				setEscalatingRemediations(nhc)
				nhc.Spec.RemediationTemplate = templ
			})
			It("should be denied", func() {
				Expect(nhc.validate()).To(MatchError(ContainSubstring(mutualRemediationError)))
			})
		})

		Context("with escalating remediations", func() {
			Context("with duplicate order", func() {
				BeforeEach(func() {
					setEscalatingRemediations(nhc)
					nhc.Spec.EscalatingRemediations[0].Order = 42
					nhc.Spec.EscalatingRemediations[2].Order = 42
				})
				It("should be denied", func() {
					Expect(nhc.validate()).To(MatchError(ContainSubstring(uniqueOrderError)))
					Expect(nhc.validate()).To(MatchError(ContainSubstring("42")))
				})
			})

			Context("with too low timeout", func() {
				BeforeEach(func() {
					setEscalatingRemediations(nhc)
					nhc.Spec.EscalatingRemediations[0].Timeout = metav1.Duration{Duration: 42 * time.Second}
				})
				It("should be denied", func() {
					Expect(nhc.validate()).To(MatchError(ContainSubstring(minimumTimeoutError)))
					Expect(nhc.validate()).To(MatchError(ContainSubstring("42s")))
				})
			})

			Context("with duplicate remediator", func() {
				BeforeEach(func() {
					setEscalatingRemediations(nhc)
					nhc.Spec.EscalatingRemediations[1].RemediationTemplate = nhc.Spec.EscalatingRemediations[0].RemediationTemplate
					nhc.Spec.EscalatingRemediations[1].RemediationTemplate.Name = "dummy"
				})
				It("should be denied", func() {
					Expect(nhc.validate()).To(MatchError(ContainSubstring(uniqueRemediatorError)))
					Expect(nhc.validate()).To(MatchError(ContainSubstring(nhc.Spec.EscalatingRemediations[1].RemediationTemplate.Kind)))
				})
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

		Context("updating remediation template", func() {
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

		Context("updating escalating remediations", func() {
			BeforeEach(func() {
				setEscalatingRemediations(nhcOld)
				nhcNew = nhcOld.DeepCopy()
				nhcNew.Spec.EscalatingRemediations[0].Order = 42
			})
			It("should be denied", func() {
				Expect(nhcNew.ValidateUpdate(nhcOld)).To(MatchError(
					And(
						ContainSubstring(OngoingRemediationError),
						ContainSubstring("escalating remediations"),
					),
				))
			})
		})
	})
})

func setEscalatingRemediations(nhc *NodeHealthCheck) {
	nhc.Spec.RemediationTemplate = nil
	nhc.Spec.EscalatingRemediations = []EscalatingRemediation{
		{
			RemediationTemplate: v1.ObjectReference{
				Kind:       "R2",
				Namespace:  "dummy",
				Name:       "r2",
				APIVersion: "r2",
			},
			Order:   20,
			Timeout: metav1.Duration{Duration: 2 * time.Minute},
		},
		{
			RemediationTemplate: v1.ObjectReference{
				Kind:       "R3",
				Namespace:  "dummy",
				Name:       "r3",
				APIVersion: "r3",
			},
			Order:   30,
			Timeout: metav1.Duration{Duration: 3 * time.Minute},
		},
		{
			RemediationTemplate: v1.ObjectReference{
				Kind:       "R1",
				Namespace:  "dummy",
				Name:       "r1",
				APIVersion: "r1",
			},
			Order:   10,
			Timeout: metav1.Duration{Duration: 1 * time.Minute},
		},
	}
}
