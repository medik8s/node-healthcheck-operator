package v1alpha1

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var _ = Describe("NodeHealthCheck Validation", func() {
	var mockValidatorClient = &mockClient{
		listFunc: func(context.Context, client.ObjectList, ...client.ListOption) error { return nil },
	}
	var validator = &customValidator{mockValidatorClient, testCaps}
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
				Expect(validator.validate(context.Background(), nhc)).To(Succeed())
			})
		})

		Context("with valid config and escalating remediations", func() {
			BeforeEach(func() {
				setEscalatingRemediations(nhc)
			})

			It("should be allowed", func() {
				Expect(validator.validate(context.Background(), nhc)).To(Succeed())
			})
		})

		Context("with negative minHealthy", func() {
			BeforeEach(func() {
				mh := intstr.FromInt(-1)
				nhc.Spec.MinHealthy = &mh
			})

			It("should be allowed", func() {
				Expect(validator.validate(context.Background(), nhc)).To(Succeed())
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
				Expect(validator.validate(context.Background(), nhc)).To(MatchError(ContainSubstring(invalidSelectorError)))
			})
		})

		Context("with empty selector", func() {
			BeforeEach(func() {
				selector := metav1.LabelSelector{}
				nhc.Spec.Selector = selector
			})

			It("should be denied", func() {
				Expect(validator.validate(context.Background(), nhc)).To(MatchError(ContainSubstring(missingSelectorError)))
			})
		})

		Context("with neither remediation template or escalating remediations set", func() {
			BeforeEach(func() {
				nhc.Spec.RemediationTemplate = nil
				nhc.Spec.EscalatingRemediations = []EscalatingRemediation{}
			})
			It("should be denied", func() {
				Expect(validator.validate(context.Background(), nhc)).To(MatchError(ContainSubstring(mandatoryRemediationError)))
			})
		})

		Context("with both remediation template and escalating remediations set", func() {
			BeforeEach(func() {
				templ := nhc.Spec.RemediationTemplate
				setEscalatingRemediations(nhc)
				nhc.Spec.RemediationTemplate = templ
			})
			It("should be denied", func() {
				Expect(validator.validate(context.Background(), nhc)).To(MatchError(ContainSubstring(mutualRemediationError)))
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
					Expect(validator.validate(context.Background(), nhc)).To(MatchError(ContainSubstring(uniqueOrderError)))
					Expect(validator.validate(context.Background(), nhc)).To(MatchError(ContainSubstring("42")))
				})
			})

			Context("with too low timeout", func() {
				BeforeEach(func() {
					setEscalatingRemediations(nhc)
					nhc.Spec.EscalatingRemediations[0].Timeout = metav1.Duration{Duration: 42 * time.Second}
				})
				It("should be denied", func() {
					Expect(validator.validate(context.Background(), nhc)).To(MatchError(ContainSubstring(minimumTimeoutError)))
					Expect(validator.validate(context.Background(), nhc)).To(MatchError(ContainSubstring("42s")))
				})
			})

			Context("with duplicate remediator", func() {
				var firstTemplate, secondTemplate unstructured.Unstructured
				BeforeEach(func() {
					setEscalatingRemediations(nhc)
					nhc.Spec.EscalatingRemediations[1].RemediationTemplate = nhc.Spec.EscalatingRemediations[0].RemediationTemplate
					nhc.Spec.EscalatingRemediations[1].RemediationTemplate.Name = "dummy"

					//Set mock validator client
					firstTemplate = unstructured.Unstructured{}
					firstTemplate.SetKind(nhc.Spec.EscalatingRemediations[0].RemediationTemplate.Kind)
					firstTemplate.SetName(nhc.Spec.EscalatingRemediations[0].RemediationTemplate.Name)
					secondTemplate = *firstTemplate.DeepCopy()
					secondTemplate.SetName(nhc.Spec.EscalatingRemediations[1].RemediationTemplate.Name)

					mockValidatorClient.listFunc = func(ctx context.Context, templatesList client.ObjectList, opts ...client.ListOption) error {
						listTemplate := templatesList.(*unstructured.UnstructuredList)
						listTemplate.Items = []unstructured.Unstructured{firstTemplate, secondTemplate}
						return nil
					}

				})
				When("remediator does not support multiple templates", func() {

					It("should be denied", func() {
						Expect(validator.validate(context.Background(), nhc)).To(MatchError(ContainSubstring(uniqueRemediatorError)))
						Expect(validator.validate(context.Background(), nhc)).To(MatchError(ContainSubstring(nhc.Spec.EscalatingRemediations[1].RemediationTemplate.Kind)))
					})
				})

				When("remediator DOES support multiple templates", func() {
					BeforeEach(func() {
						firstTemplate.SetAnnotations(map[string]string{"remediation.medik8s.io/multiple-templates-support": "true"})
						secondTemplate.SetAnnotations(map[string]string{"remediation.medik8s.io/multiple-templates-support": "true"})
						DeferCleanup(func() {
							firstTemplate.SetAnnotations(nil)
							secondTemplate.SetAnnotations(nil)
						})
					})

					It("should be allowed", func() {
						Expect(validator.validate(context.Background(), nhc)).To(Succeed())
					})
				})

			})
		})

		Context("with unsupported control plane topology", func() {
			BeforeEach(func() {
				testCaps.IsSupportedControlPlaneTopology = false
			})
			AfterEach(func() {
				testCaps.IsSupportedControlPlaneTopology = true
			})
			It("should be denied", func() {
				Expect(validator.validate(context.Background(), nhc)).To(MatchError(ContainSubstring(unsupportedCpTopologyError)))
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
					UnhealthyNodes: []*UnhealthyNode{
						{
							Name: "test",
							Remediations: []*Remediation{
								{
									Resource: v1.ObjectReference{
										Kind: "test",
										Name: "test",
									},
									Started:  metav1.Now(),
									TimedOut: nil,
								},
							},
							ConditionsHealthyTimestamp: nil,
						},
					},
				},
			}
		})
		validateError := func(validate func(ctx context.Context, old runtime.Object, new runtime.Object) (warnings admission.Warnings, err error), old, new *NodeHealthCheck, substrings ...string) {
			warnings, err := validate(context.Background(), old, new)
			ExpectWithOffset(1, warnings).To(BeEmpty())
			matchers := make([]types.GomegaMatcher, 0)
			for _, substring := range substrings {
				matchers = append(matchers, ContainSubstring(substring))
			}
			ExpectWithOffset(1, err).To(MatchError(
				And(matchers...),
			))
		}

		Context("updating selector", func() {
			BeforeEach(func() {
				nhcNew = nhcOld.DeepCopy()
				nhcNew.Spec.Selector.MatchExpressions[0].Key = "node-role.kubernetes.io/infra"
			})
			It("should be denied", func() {
				validateError(validator.ValidateUpdate, nhcOld, nhcNew, OngoingRemediationError, "selector")
			})
		})

		Context("updating remediation template", func() {
			BeforeEach(func() {
				nhcNew = nhcOld.DeepCopy()
				nhcNew.Spec.RemediationTemplate.Name = "newName"
			})
			It("should be denied", func() {
				validateError(validator.ValidateUpdate, nhcOld, nhcNew, OngoingRemediationError, "remediation template")
			})
		})

		Context("updating escalating remediations", func() {
			BeforeEach(func() {
				setEscalatingRemediations(nhcOld)
				nhcNew = nhcOld.DeepCopy()
				nhcNew.Spec.EscalatingRemediations[0].Order = 42
			})
			It("should be denied", func() {
				validateError(validator.ValidateUpdate, nhcOld, nhcNew, OngoingRemediationError, "escalating remediations")
			})
		})
	})

	Context("Test isRemediating", func() {
		var nhc *NodeHealthCheck

		BeforeEach(func() {
			nhc = &NodeHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
			}
		})

		When("unhealthy node isn't remediated yet", func() {
			BeforeEach(func() {
				nhc.Status.UnhealthyNodes = []*UnhealthyNode{
					{
						Name:                       "test",
						Remediations:               nil,
						ConditionsHealthyTimestamp: nil,
					},
				}
			})
			It("should return false", func() {
				Expect(nhc.isRemediating()).To(BeFalse())
			})
		})

		When("unhealthy node is remediated", func() {
			BeforeEach(func() {
				nhc.Status.UnhealthyNodes = []*UnhealthyNode{
					{
						Name: "test",
						Remediations: []*Remediation{
							{
								Resource: v1.ObjectReference{
									Kind: "test",
									Name: "test",
								},
								Started:  metav1.Now(),
								TimedOut: nil,
							},
						},
						ConditionsHealthyTimestamp: nil,
					},
				}
			})
			It("should return true", func() {
				Expect(nhc.isRemediating()).To(BeTrue())
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

type mockClient struct {
	client.Client
	listFunc func(context.Context, client.ObjectList, ...client.ListOption) error
}

func (m *mockClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return m.listFunc(ctx, list, opts...)
}
