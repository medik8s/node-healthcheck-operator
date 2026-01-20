package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	commonannotations "github.com/medik8s/common/pkg/annotations"
	commonconditions "github.com/medik8s/common/pkg/conditions"
	commonLabels "github.com/medik8s/common/pkg/labels"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	coordv1 "k8s.io/api/coordination/v1"
	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	"k8s.io/utils/ptr"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/resources"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils/annotations"
)

const (
	unhealthyConditionDuration = 10 * time.Second
	nodeUnhealthyIn            = 5 * time.Second
)

var _ = Describe("Node Health Check CR", func() {
	AfterEach(func() {
		clearEvents()
	})

	Context("Defaults", func() {
		var underTest *v1alpha1.NodeHealthCheck

		BeforeEach(func() {
			underTest = &v1alpha1.NodeHealthCheck{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: v1alpha1.NodeHealthCheckSpec{
					Selector:            metav1.LabelSelector{},
					RemediationTemplate: infraRemediationTemplateRef.DeepCopy(),
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
			It("it should have all default values set", func() {
				Expect(underTest.Namespace).To(BeEmpty())
				Expect(underTest.Spec.UnhealthyConditions).To(HaveLen(2))
				Expect(underTest.Spec.UnhealthyConditions[0].Type).To(Equal(v1.NodeReady))
				Expect(underTest.Spec.UnhealthyConditions[0].Status).To(Equal(v1.ConditionFalse))
				Expect(underTest.Spec.UnhealthyConditions[0].Duration).To(Equal(metav1.Duration{Duration: time.Minute * 5}))
				Expect(underTest.Spec.UnhealthyConditions[1].Type).To(Equal(v1.NodeReady))
				Expect(underTest.Spec.UnhealthyConditions[1].Status).To(Equal(v1.ConditionUnknown))
				Expect(underTest.Spec.UnhealthyConditions[1].Duration).To(Equal(metav1.Duration{Duration: time.Minute * 5}))
				Expect(underTest.Spec.Selector.MatchLabels).To(BeEmpty())
				Expect(underTest.Spec.Selector.MatchExpressions).To(BeEmpty())
			})
		})

		When("updating status", func() {
			It("succeeds updating only part of the fields", func() {
				Expect(underTest.Status).ToNot(BeNil())
				Expect(underTest.Status.HealthyNodes).To(BeNil())
				patch := client.MergeFrom(underTest.DeepCopy())
				underTest.Status.HealthyNodes = pointer.Int(1)
				underTest.Status.ObservedNodes = pointer.Int(6)
				err := k8sClient.Status().Patch(context.Background(), underTest, patch)
				Expect(err).NotTo(HaveOccurred())
				Expect(*underTest.Status.HealthyNodes).To(Equal(1))
				Expect(*underTest.Status.ObservedNodes).To(Equal(6))
			})
		})

	})

	Context("Validation", func() {
		var underTest *v1alpha1.NodeHealthCheck

		BeforeEach(func() {
			underTest = &v1alpha1.NodeHealthCheck{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: v1alpha1.NodeHealthCheckSpec{
					RemediationTemplate: infraRemediationTemplateRef.DeepCopy(),
				},
			}
		})

		AfterEach(func() {
			_ = k8sClient.Delete(context.Background(), underTest)
		})

		When("specifying an external remediation template", func() {
			It("should succeed creation if a template CR doesn't exists", func() {
				err := k8sClient.Create(context.Background(), underTest)
				Expect(err).NotTo(HaveOccurred())
			})
		})
		When("specifying min healthy", func() {
			Context("minHealthy", func() {
				It("fails creation on percentage > 100%", func() {
					invalidPercentage := intstr.FromString("150%")
					underTest.Spec.MinHealthy = &invalidPercentage
					err := k8sClient.Create(context.Background(), underTest)
					Expect(errors.IsInvalid(err)).To(BeTrue())
				})

				It("fails creation on negative number", func() {
					// This test does not work yet, because the "minimum" validation
					// of kubebuilder does not work for IntOrString.
					// Un-skip this as soon as this is supported.
					// For now negative minHealthy is validated via webhook.
					Skip("Does not work yet")
					invalidInt := intstr.FromInt(-10)
					underTest.Spec.MinHealthy = &invalidInt
					err := k8sClient.Create(context.Background(), underTest)
					Expect(errors.IsInvalid(err)).To(BeTrue())
				})

				It("succeeds creation on percentage between 0%-100%", func() {
					validPercentage := intstr.FromString("30%")
					underTest.Spec.MinHealthy = &validPercentage
					err := k8sClient.Create(context.Background(), underTest)
					Expect(errors.IsInvalid(err)).To(BeFalse())
				})
			})

			Context("maxUnhealthy", func() {
				It("fails creation on percentage > 100%", func() {
					invalidPercentage := intstr.FromString("150%")
					underTest.Spec.MaxUnhealthy = &invalidPercentage
					err := k8sClient.Create(context.Background(), underTest)
					Expect(errors.IsInvalid(err)).To(BeTrue())
				})

				It("fails creation on negative number", func() {
					// This test does not work yet, because the "minimum" validation
					// of kubebuilder does not work for IntOrString.
					// Un-skip this as soon as this is supported.
					// For now negative minHealthy is validated via webhook.
					Skip("Does not work yet")
					invalidInt := intstr.FromInt(-10)
					underTest.Spec.MaxUnhealthy = &invalidInt
					err := k8sClient.Create(context.Background(), underTest)
					Expect(errors.IsInvalid(err)).To(BeTrue())
				})

				It("succeeds creation on percentage between 0%-100%", func() {
					validPercentage := intstr.FromString("30%")
					underTest.Spec.MaxUnhealthy = &validPercentage
					err := k8sClient.Create(context.Background(), underTest)
					Expect(errors.IsInvalid(err)).To(BeFalse())
				})
			})

		})
	})

	createObjects := func(objects ...client.Object) {
		for _, obj := range objects {
			Expect(k8sClient.Create(context.Background(), obj)).To(Succeed())
		}
	}

	deleteObjects := func(objects ...client.Object) {
		for _, obj := range objects {
			// ignore errors, CRs might be deleted by reconcile
			_ = k8sClient.Delete(context.Background(), obj)
		}
	}

	Context("Reconciliation", func() {
		const (
			unhealthyNodeName = "unhealthy-worker-node-1"
		)
		var (
			underTest *v1alpha1.NodeHealthCheck
			objects   []client.Object
			// Lease params
			leaseName                             = fmt.Sprintf("%s-%s", "node", unhealthyNodeName)
			mockRequeueDurationIfLeaseTaken       = time.Second * 2
			mockDefaultLeaseDuration              = time.Second * 2
			mockLeaseBuffer                       = time.Second
			otherLeaseDurationInSeconds     int32 = 3
		)

		setupObjects := func(unhealthy int, healthy int, unhealthyNow bool) {
			objects = newNodes(unhealthy, healthy, false, unhealthyNow)
			objects = append(objects, underTest)
		}

		BeforeEach(func() {
			underTest = newNodeHealthCheck()
		})

		JustBeforeEach(func() {
			createObjects(objects...)
			// give the reconciler some time
			time.Sleep(2 * time.Second)
			// get updated NHC
			Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
		})

		AfterEach(func() {
			// delete all created objects
			deleteObjects(objects...)

			// delete all remediation CRs
			for _, cr := range newBaseCRs(underTest) {
				if cr.GetKind() == "dummy" {
					continue
				}
				crList := &unstructured.UnstructuredList{Object: cr.Object}
				Expect(k8sClient.List(context.Background(), crList)).To(Succeed())
				for _, item := range crList.Items {
					if len(item.GetFinalizers()) > 0 {
						item.SetFinalizers([]string{})
						Expect(k8sClient.Update(context.Background(), &item)).To(Succeed())
						Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(&item), &item)).To(Succeed())
					}
					err := k8sClient.Delete(context.Background(), &item)
					if err != nil {
						Expect(errors.IsNotFound(err)).To(BeTrue())
					}
				}
			}

			// Cleanup lease
			lease := &coordv1.Lease{}
			err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: leaseNs, Name: leaseName}, lease)
			if err == nil {
				err = k8sClient.Delete(context.Background(), lease)
				Expect(err).NotTo(HaveOccurred())
			}

			// let thing settle a bit
			time.Sleep(1 * time.Second)
		})

		testReconcile := func() {

			When("Remediation template is broken", func() {

				BeforeEach(func() {
					setupObjects(0, 2, true)
				})

				expectTemplateNotFound := func(g Gomega, nhc *v1alpha1.NodeHealthCheck, expectedError string) {
					g.ExpectWithOffset(1, underTest.Status.Phase).To(Equal(v1alpha1.PhaseDisabled))
					g.ExpectWithOffset(1, underTest.Status.Reason).To(ContainSubstring(expectedError))
					g.ExpectWithOffset(1, underTest.Status.Conditions).To(ContainElement(
						And(
							HaveField("Type", v1alpha1.ConditionTypeDisabled),
							HaveField("Status", metav1.ConditionTrue),
							HaveField("Reason", v1alpha1.ConditionReasonDisabledTemplateNotFound),
						)))
				}

				Context("with invalid kind", func() {
					BeforeEach(func() {
						if underTest.Spec.RemediationTemplate != nil {
							underTest.Spec.RemediationTemplate.Kind = "dummyTemplate"
						} else {
							underTest.Spec.EscalatingRemediations[0].RemediationTemplate.Kind = "dummyTemplate"
						}
					})

					It("should set corresponding condition", func() {
						expectTemplateNotFound(Default, underTest, "failed to get")
					})
				})

				Context("with missing namespace", func() {
					BeforeEach(func() {
						if underTest.Spec.RemediationTemplate != nil {
							underTest.Spec.RemediationTemplate.Namespace = ""
						} else {
							underTest.Spec.EscalatingRemediations[0].RemediationTemplate.Namespace = ""
						}
					})

					It("should set corresponding condition", func() {
						expectTemplateNotFound(Default, underTest, "no namespace is provided")
					})
				})

				Context("templated is deleted after NHC creation", func() {
					It("should set corresponding condition", func() {
						By("deleting template")
						templateRef := underTest.Spec.RemediationTemplate
						if templateRef == nil {
							templateRef = &underTest.Spec.EscalatingRemediations[0].RemediationTemplate
						}
						key := client.ObjectKey{
							Namespace: templateRef.Namespace,
							Name:      templateRef.Name,
						}
						template := &unstructured.Unstructured{}
						template.SetGroupVersionKind(templateRef.GroupVersionKind())
						Expect(k8sClient.Get(context.Background(), key, template)).To(Succeed())
						Expect(k8sClient.Delete(context.Background(), template)).To(Succeed())
						DeferCleanup(func() {
							By("recreating template")
							template.SetResourceVersion("")
							Expect(k8sClient.Create(context.Background(), template)).To(Succeed())
						})
						Eventually(func(g Gomega) {
							g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
							expectTemplateNotFound(g, underTest, "failed to get")
						}, "5s", "200ms").Should(Succeed(), "expected disabled NHC")
					})
				})
			})

			Context("Machine owners", func() {
				When("Metal3RemediationTemplate is in wrong namespace", func() {

					BeforeEach(func() {
						setupObjects(1, 2, true)

						// set metal3 template
						if underTest.Spec.RemediationTemplate != nil {
							underTest.Spec.RemediationTemplate.Kind = "Metal3RemediationTemplate"
							underTest.Spec.RemediationTemplate.Name = "nok"
							underTest.Spec.RemediationTemplate.Namespace = "default"
						} else {
							underTest.Spec.EscalatingRemediations[0].RemediationTemplate.Kind = "Metal3RemediationTemplate"
							underTest.Spec.EscalatingRemediations[0].RemediationTemplate.Name = "nok"
							underTest.Spec.EscalatingRemediations[0].RemediationTemplate.Namespace = "default"
						}
					})

					It("should be disabled", func() {
						Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseDisabled))
						Expect(underTest.Status.Reason).To(
							ContainSubstring("Metal3RemediationTemplate must be in the openshift-machine-api namespace"),
						)
						Expect(underTest.Status.Conditions).To(ContainElement(
							And(
								HaveField("Type", v1alpha1.ConditionTypeDisabled),
								HaveField("Status", metav1.ConditionTrue),
								HaveField("Reason", v1alpha1.ConditionReasonDisabledTemplateInvalid),
							)))
					})
				})
			})

			When("few nodes are unhealthy and healthy nodes meet min healthy", func() {
				BeforeEach(func() {
					setupObjects(1, 2, false)
				})

				It("create a remediation CR for each unhealthy node and updates status", func() {

					By("checking CR isn't created when unhealthy duration didn't expire yet")

					// first call should fail, because the node gets unready in a few seconds only
					cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
					Expect(cr).To(BeNil())

					By("waiting until nodes are unhealthy")
					time.Sleep(nodeUnhealthyIn)

					By("checking CR is created now")
					cr = findRemediationCRForNHC(unhealthyNodeName, underTest)
					Expect(cr).ToNot(BeNil())
					Expect(cr.Object).To(ContainElement(map[string]interface{}{"size": "foo"}))
					Expect(cr.GetOwnerReferences()).
						To(ContainElement(
							And(
								// Kind and API version aren't set on underTest, envtest issue...
								// Controller is empty for HaveField because false is the zero value?
								HaveField("Name", underTest.Name),
								HaveField("UID", underTest.UID),
							),
						))
					Expect(cr.GetAnnotations()[oldRemediationCRAnnotationKey]).To(BeEmpty())

					By("simulating remediator by putting a finalizer on the remediation CR")
					cr.SetFinalizers([]string{"dummy"})
					Expect(k8sClient.Update(context.Background(), cr)).To(Succeed())

					By("checking NHC status")
					Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
					Expect(*underTest.Status.HealthyNodes).To(Equal(2))
					Expect(*underTest.Status.ObservedNodes).To(Equal(3))
					Expect(underTest.Status.UnhealthyNodes).To(HaveLen(1))
					Expect(underTest.Status.UnhealthyNodes[0].Name).To(Equal(unhealthyNodeName))
					Expect(underTest.Status.UnhealthyNodes[0].Remediations).To(HaveLen(1))
					Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Resource.GroupVersionKind()).To(Equal(cr.GroupVersionKind()))
					Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Resource.Name).To(Equal(cr.GetName()))
					Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Resource.Namespace).To(Equal(cr.GetNamespace()))
					Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Resource.UID).To(Equal(cr.GetUID()))
					Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Started).ToNot(BeNil())
					Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].TimedOut).To(BeNil())
					Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseRemediating))
					Expect(underTest.Status.Reason).ToNot(BeEmpty())
					Expect(underTest.Status.Conditions).To(ContainElement(
						And(
							HaveField("Type", v1alpha1.ConditionTypeDisabled),
							HaveField("Status", metav1.ConditionFalse),
							HaveField("Reason", v1alpha1.ConditionReasonEnabled),
						)))

					By("making node ready")
					unhealthyNode := &v1.Node{}
					unhealthyNode.Name = unhealthyNodeName
					Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(unhealthyNode), unhealthyNode)).To(Succeed())
					unhealthyNode.Status.Conditions = []v1.NodeCondition{
						{
							Type:   v1.NodeReady,
							Status: v1.ConditionTrue,
						},
					}
					Expect(k8sClient.Status().Update(context.Background(), unhealthyNode))

					By("expecting status update")
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
						g.Expect(underTest.Status.UnhealthyNodes[0].ConditionsHealthyTimestamp).ToNot(BeNil())
						// ensure node is still considered unhealthy though
						g.Expect(*underTest.Status.HealthyNodes).To(Equal(2))
						g.Expect(underTest.Status.UnhealthyNodes[0].Remediations).To(HaveLen(1))
						g.Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseRemediating))
					}, "5s", "500ms").Should(Succeed(), "expected conditionsHealthyTimestamp to be set")

					By("simulating remediator finished by removing finalizer")
					Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)).To(Succeed())
					cr.SetFinalizers([]string{})
					Expect(k8sClient.Update(context.Background(), cr)).To(Succeed())

					By("expecting CR deletion")
					Eventually(func(g Gomega) {
						err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
						Expect(errors.IsNotFound(err)).To(BeTrue())
					}, "5s", "500ms").Should(Succeed(), "expected CR deletion")

					By("expecting status update")
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
						g.Expect(underTest.Status.UnhealthyNodes).To(HaveLen(0))
						g.Expect(*underTest.Status.HealthyNodes).To(Equal(3))
						g.Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseEnabled))
					}, "5s", "500ms").Should(Succeed(), "expected conditionsHealthyTimestamp to be set")

				})

			})

			When("few nodes are unhealthy and healthy nodes below min healthy", func() {
				BeforeEach(func() {
					setupObjects(4, 3, true)
				})

				It("skips remediation - CR is not created, status updated correctly", func() {
					Expect(findRemediationCRForNHC(unhealthyNodeName, underTest)).To(BeNil())
					Expect(*underTest.Status.HealthyNodes).To(Equal(3))
					Expect(*underTest.Status.ObservedNodes).To(Equal(7))
					Expect(underTest.Status.UnhealthyNodes).To(HaveLen(4))
					Expect(underTest.Status.UnhealthyNodes[0].Remediations).To(HaveLen(0))
					Expect(underTest.Status.UnhealthyNodes[1].Remediations).To(HaveLen(0))
					Expect(underTest.Status.UnhealthyNodes[2].Remediations).To(HaveLen(0))
					Expect(underTest.Status.UnhealthyNodes[3].Remediations).To(HaveLen(0))
					Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseEnabled))
					Expect(underTest.Status.Reason).ToNot(BeEmpty())
				})

			})

			When("few nodes become healthy", func() {
				BeforeEach(func() {
					setupObjects(1, 2, true)
					remediationCR := newRemediationCRForNHC("healthy-worker-node-2", underTest)
					remediationCR.SetFinalizers([]string{"dummy"})
					remediationCROther := newRemediationCRForNHC("healthy-worker-node-1", underTest)
					refs := remediationCROther.GetOwnerReferences()
					refs[0].Name = "other"
					remediationCROther.SetOwnerReferences(refs)
					objects = append(objects, remediationCR, remediationCROther)
				})

				It("deletes an existing remediation CR and updates status", func() {
					By("verifying CR owned by other NHC isn't deleted")
					cr := findRemediationCRForNHC("healthy-worker-node-1", underTest)
					Expect(cr).ToNot(BeNil())

					By("verifying CR owned by us has deletion timestamp")
					Eventually(func(g Gomega) {
						cr = findRemediationCRForNHC("healthy-worker-node-2", underTest)
						g.Expect(cr).ToNot(BeNil())
						g.Expect(cr.GetDeletionTimestamp()).ToNot(BeNil())
					}, "2s", "100ms").Should(Succeed())

					By("verifying node with CR is still considered unhealthy")
					Expect(*underTest.Status.HealthyNodes).To(Equal(1))
					Expect(*underTest.Status.ObservedNodes).To(Equal(3))
					// don't test Status.InFlightRemediations / Status.UnhealthyNodes here, they aren't updated for
					// the existing CR created by the test...

					By("simulating remediator finished by removing finalizer on the cp remediation CR")
					Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)).To(Succeed())
					cr.SetFinalizers([]string{})
					Expect(k8sClient.Update(context.Background(), cr)).To(Succeed())

					By("verifying CR is deleted")
					Eventually(func(g Gomega) {
						err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
						g.Expect(errors.IsNotFound(err)).To(BeTrue())
					}, "2s", "100ms").Should(Succeed())

					By("verifying next node remediated now")
					Eventually(func(g Gomega) {
						cr = findRemediationCRForNHC(unhealthyNodeName, underTest)
						g.Expect(cr).ToNot(BeNil())
					}, "2s", "100ms").Should(Succeed())

					By("verifying status")
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
						g.Expect(*underTest.Status.HealthyNodes).To(Equal(2))
					}, "2s", "100ms").Should(Succeed())
					Expect(*underTest.Status.ObservedNodes).To(Equal(3))
					Expect(underTest.Status.UnhealthyNodes).To(HaveLen(1))
					Expect(underTest.Status.UnhealthyNodes[0].Name).To(Equal(unhealthyNodeName))
					Expect(underTest.Status.UnhealthyNodes[0].Remediations).To(HaveLen(1))
					Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Resource.GroupVersionKind()).To(Equal(cr.GroupVersionKind()))
					Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Resource.Name).To(Equal(cr.GetName()))
					Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Resource.Namespace).To(Equal(cr.GetNamespace()))
					Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Resource.UID).To(Equal(cr.GetUID()))
					Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Started).ToNot(BeNil())
					Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].TimedOut).To(BeNil())
					Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseRemediating))
					Expect(underTest.Status.Reason).ToNot(BeEmpty())
				})
			})

			When("an old remediation cr exists", func() {
				BeforeEach(func() {
					setupObjects(1, 2, true)
				})

				AfterEach(func() {
					fakeTime = nil
				})

				It("an alert flag is set on remediation cr", func() {
					By("faking time and triggering another reconcile")
					afterTimeout := time.Now().Add(remediationCRAlertTimeout).Add(2 * time.Minute)
					fakeTime = &afterTimeout
					newMinHealthy := intstr.FromString("52%")
					underTest.Spec.MinHealthy = &newMinHealthy
					Expect(k8sClient.Update(context.Background(), underTest)).To(Succeed())
					time.Sleep(2 * time.Second)

					cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
					Expect(cr).ToNot(BeNil())
					Expect(cr.GetAnnotations()[oldRemediationCRAnnotationKey]).To(Equal("flagon"))
				})
			})

			When("a remediation cr not owned by current NHC exists", func() {
				BeforeEach(func() {
					cr := newRemediationCRForNHC(unhealthyNodeName, underTest)
					owners := cr.GetOwnerReferences()
					owners[0].Name = "not-me"
					cr.SetOwnerReferences(owners)
					Expect(k8sClient.Create(context.Background(), cr)).To(Succeed())
					setupObjects(1, 2, true)
				})

				It("remediation cr should not be processed", func() {
					Expect(underTest.Status.UnhealthyNodes).To(HaveLen(1))
					Expect(underTest.Status.UnhealthyNodes[0].Name).To(Equal(unhealthyNodeName))
					Expect(underTest.Status.UnhealthyNodes[0].Remediations).To(HaveLen(0))
				})
			})

			When("two NHC CRs with different templates target the same unhealthy node", func() {

				otherTestCRName := "other-test"
				var otherTestCR *v1alpha1.NodeHealthCheck

				BeforeEach(func() {
					// prepare other CR but do not create yet, in order to have a predictable order of things to happen
					otherTestCR = underTest.DeepCopy()
					otherTestCR.SetName(otherTestCRName)
					otherTestCR.Spec.RemediationTemplate = &v1.ObjectReference{
						Kind:       "Metal3RemediationTemplate",
						Namespace:  MachineNamespace,
						Name:       "ok",
						APIVersion: "test.medik8s.io/v1alpha1",
					}

					setupObjects(1, 2, true)
				})

				It("only one NHC should remediate that node", func() {

					By("Verifying node is remediated by 1st NHC")
					Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
					Expect(underTest.Status.UnhealthyNodes).To(HaveLen(1))
					Expect(underTest.Status.UnhealthyNodes[0].Remediations).To(HaveLen(1))

					By("Creating a 2nd NHC")
					Expect(k8sClient.Create(context.Background(), otherTestCR)).To(Succeed())
					DeferCleanup(func() {
						Expect(k8sClient.Delete(context.Background(), otherTestCR)).To(Succeed())
					})
					By("Verifying node is NOT remediated by 2nd NHC")
					// wait for unhealthy node
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(otherTestCR), otherTestCR)).To(Succeed())
						g.Expect(otherTestCR.Status.UnhealthyNodes).To(HaveLen(1))
					}, "5s", "1s").Should(Succeed(), "unhealthy node not detected")
					// ensure no remediation starts
					Consistently(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(otherTestCR), otherTestCR)).To(Succeed())
						g.Expect(otherTestCR.Status.UnhealthyNodes).To(HaveLen(1))
						g.Expect(otherTestCR.Status.UnhealthyNodes[0].Remediations).To(HaveLen(0))
					}, "5s", "1s").Should(Succeed(), "duplicate remediation detected!")
				})
			})

		}

		Context("with spec.remediationTemplate and with multiple same kind support", func() {
			// multiple same kind support is default
			testReconcile()
		})

		Context("with spec.remediationTemplate and without multiple same kind support", func() {
			BeforeEach(func() {
				underTest.Spec.RemediationTemplate = infraRemediationTemplateRef.DeepCopy()
			})
			testReconcile()

			Context("Node Lease", func() {

				BeforeEach(func() {
					setupObjects(1, 2, true)
				})
				When("an unhealthy node becomes healthy", func() {
					It("node lease is removed", func() {
						cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
						Expect(cr).ToNot(BeNil())
						// Verify lease exist
						lease := &coordv1.Lease{}
						err := k8sClient.Get(context.Background(), client.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)
						Expect(err).ToNot(HaveOccurred())

						// Mock node becoming healthy
						mockNodeGettingHealthy(unhealthyNodeName)

						// Remediation should be removed
						Eventually(func() bool {
							err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
							return errors.IsNotFound(err)
						}, "2s", "100ms").Should(BeTrue(), "remediation CR wasn't removed")

						// Verify NHC removed the lease
						Eventually(func() bool {
							err = k8sClient.Get(context.Background(), client.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)
							return errors.IsNotFound(err)
						}, "2s", "100ms").Should(BeTrue(), "lease wasn't removed")

					})

					It("node lease not owned by us isn't removed, but status is updated (invalidate lease error is ignored)", func() {
						cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
						Expect(cr).ToNot(BeNil())

						// Verify lease exist
						lease := &coordv1.Lease{}
						err := k8sClient.Get(context.Background(), client.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)
						Expect(err).ToNot(HaveOccurred())

						// change lease owner
						newLeaseOwner := "someone-else"
						lease.Spec.HolderIdentity = pointer.String(newLeaseOwner)
						Expect(k8sClient.Update(context.Background(), lease)).To(Succeed(), "failed to update lease owner")

						// Mock node becoming healthy
						mockNodeGettingHealthy(unhealthyNodeName)

						// Remediation should be removed
						Eventually(func() bool {
							err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
							return errors.IsNotFound(err)
						}, "2s", "100ms").Should(BeTrue(), "remediation CR wasn't removed")

						// Status should be updated even though lease isn't owned by us anymore
						Eventually(func(g Gomega) {
							g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
							g.Expect(underTest.Status.UnhealthyNodes).To(BeEmpty())
						}, "2s", "100ms").Should(Succeed(), "status update failed")

						// Verify NHC didn't touch the lease
						Consistently(func(g Gomega) {
							g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)).To(Succeed(), "failed to get lease")
							g.Expect(*lease.Spec.HolderIdentity).To(Equal(newLeaseOwner))
						}, "2s", "100ms").Should(Succeed(), "lease was touched even though it's not owned by us")

					})
				})

				When("an unhealthy node lease is already taken", func() {
					BeforeEach(func() {
						mockLeaseParams(mockRequeueDurationIfLeaseTaken, mockDefaultLeaseDuration, mockLeaseBuffer)

						// Create a mock lease that is already taken
						now := metav1.NowMicro()
						lease := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: leaseName, Namespace: leaseNs}, Spec: coordv1.LeaseSpec{HolderIdentity: pointer.String("notNHC"), LeaseDurationSeconds: &otherLeaseDurationInSeconds, RenewTime: &now, AcquireTime: &now}}
						err := k8sClient.Create(context.Background(), lease)
						Expect(err).NotTo(HaveOccurred())
					})

					It("a remediation CR isn't created until lease is obtained", func() {
						cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
						Expect(cr).To(BeNil())

						Expect(*underTest.Status.HealthyNodes).To(Equal(2))
						Expect(*underTest.Status.ObservedNodes).To(Equal(3))
						Expect(underTest.Status.UnhealthyNodes).To(HaveLen(1))
						Expect(underTest.Status.UnhealthyNodes[0].Name).To(Equal(unhealthyNodeName))
						Expect(underTest.Status.UnhealthyNodes[0].Remediations).To(HaveLen(0))

						Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseEnabled))
						Expect(underTest.Status.Reason).ToNot(BeEmpty())
						Expect(underTest.Status.Conditions).To(ContainElement(
							And(
								HaveField("Type", v1alpha1.ConditionTypeDisabled),
								HaveField("Status", metav1.ConditionFalse),
								HaveField("Reason", v1alpha1.ConditionReasonEnabled),
							)))
						// Expecting NHC to acquire the lease now and create the CR - checking CR first
						Eventually(func(g Gomega) {
							cr = findRemediationCRForNHC(unhealthyNodeName, underTest)
							g.Expect(cr).ToNot(BeNil())
						}, mockRequeueDurationIfLeaseTaken*2+time.Millisecond*100, time.Millisecond*100).Should(Succeed())

						// Verifying lease is created
						lease := &coordv1.Lease{}
						err := k8sClient.Get(context.Background(), client.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)
						Expect(err).ToNot(HaveOccurred())
						Expect(*lease.Spec.HolderIdentity).To(Equal(fmt.Sprintf("%s-%s", "NodeHealthCheck", underTest.GetName())))
						Expect(*lease.Spec.LeaseDurationSeconds).To(Equal(int32(2 + 1 /*2 seconds is DefaultLeaseDuration (mocked) + 1 second buffer (mocked)  */)))
						Expect(lease.Spec.AcquireTime).ToNot(BeNil())
						Expect(*lease.Spec.AcquireTime).To(Equal(*lease.Spec.RenewTime))
						verifyEvent("Warning", utils.EventReasonRemediationSkipped, fmt.Sprintf("Skipped remediation of node: %s, because node lease is already taken", unhealthyNodeName))
						leaseExpireTime := lease.Spec.AcquireTime.Time.Add(mockRequeueDurationIfLeaseTaken*3 + mockLeaseBuffer)
						timeLeftForLease := leaseExpireTime.Sub(time.Now())
						// Wait for lease to be extended
						time.Sleep(timeLeftForLease * 3 / 4)
						lease = &coordv1.Lease{}
						err = k8sClient.Get(context.Background(), client.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)
						// Verify NHC extended the lease
						Expect(err).ToNot(HaveOccurred())
						Expect(*lease.Spec.AcquireTime).ToNot(Equal(*lease.Spec.RenewTime))
						Expect(lease.Spec.RenewTime.Sub(lease.Spec.AcquireTime.Time) > 0).To(BeTrue())

						// Wait for lease to expire
						time.Sleep(timeLeftForLease/4 + time.Millisecond*100)
						lease = &coordv1.Lease{}
						err = k8sClient.Get(context.Background(), client.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)
						// Verify NHC removed the lease
						Expect(errors.IsNotFound(err)).To(BeTrue())
						// Verify NHC sets timeout annotation
						err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
						Expect(err).ToNot(HaveOccurred())
						_, isNhcTimeOutSet := cr.GetAnnotations()[commonannotations.NhcTimedOut]
						Expect(isNhcTimeOutSet).To(BeTrue())

					})

				})
			})

			When("unhealthy condition changes", func() {
				BeforeEach(func() {
					setupObjects(1, 2, true)
				})

				It("should not delete CR when duration didn't expire yet", func() {
					cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
					Expect(cr).ToNot(BeNil())

					By("changing to other unhealthy condition")
					node := &v1.Node{}
					Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: unhealthyNodeName}, node)).To(Succeed())
					Expect(node.Status.Conditions[0].Status).ToNot(Equal(v1.ConditionFalse))
					node.Status.Conditions[0].Status = v1.ConditionFalse
					node.Status.Conditions[0].LastTransitionTime = metav1.Now()
					Expect(k8sClient.Status().Update(context.Background(), node)).To(Succeed())

					By("ensuring CR isn't deleted")
					Consistently(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)).To(Succeed())
					}, unhealthyConditionDuration*2, "1s").Should(Succeed(), "CR was deleted")

					By("changing to healthy condition")
					Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: unhealthyNodeName}, node)).To(Succeed())
					node.Status.Conditions[0].Status = v1.ConditionTrue
					node.Status.Conditions[0].LastTransitionTime = metav1.Now()
					Expect(k8sClient.Status().Update(context.Background(), node)).To(Succeed())

					By("ensuring CR is deleted")
					Eventually(func(g Gomega) {
						err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
						g.Expect(errors.IsNotFound(err)).To(BeTrue())
					}, "2s", "200ms").Should(Succeed(), "CR wasn't deleted")
				})

			})

			When("node stays unhealthy", func() {
				BeforeEach(func() {
					// Mocking lease in order to reassure lease extension will trigger a second reconcile (in which the event shouldn't be triggered)
					mockLeaseParams(mockRequeueDurationIfLeaseTaken, mockDefaultLeaseDuration, mockLeaseBuffer)
					setupObjects(1, 2, true)
				})
				It("Node unhealthy event should occur once", func() {
					verifyEvent("Normal", utils.EventReasonDetectedUnhealthy, fmt.Sprintf("Node matches unhealthy condition. Node %q, condition type %q, condition status %q", unhealthyNodeName, "Ready", "Unknown"))
					// After verification event is extracted so this is how we verify it occurred only once
					verifyNoEvent("Normal", utils.EventReasonDetectedUnhealthy, fmt.Sprintf("Node matches unhealthy condition. Node %q, condition type %q, condition status %q", unhealthyNodeName, "Ready", "Unknown"))

				})
			})

			When("a Succeeded remediation times out", func() {
				BeforeEach(func() {
					mockLeaseParams(mockRequeueDurationIfLeaseTaken, mockDefaultLeaseDuration, mockLeaseBuffer)
					setupObjects(1, 2, true)
				})

				It("should not set time out annotation", func() {
					cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
					Expect(cr).ToNot(BeNil())

					// Changing remediation's Status to Succeeded
					cr = updateStatusCondition(cr)
					Expect(k8sClient.Status().Update(context.Background(), cr)).To(Succeed())

					// Wait for lease to expire
					lease := &coordv1.Lease{}
					err := k8sClient.Get(context.Background(), client.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)
					leaseExpireTime := lease.Spec.AcquireTime.Time.Add(mockRequeueDurationIfLeaseTaken*3 + mockLeaseBuffer)
					timeLeftForLease := leaseExpireTime.Sub(time.Now())
					time.Sleep(timeLeftForLease + time.Millisecond*100)

					// Verify that the remediation CR doesn't have the timeout annotation
					Consistently(func(g Gomega) {
						err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
						Expect(errors.IsNotFound(err)).To(BeFalse())
						_, isNhcTimeOutSet := cr.GetAnnotations()[commonannotations.NhcTimedOut]
						Expect(isNhcTimeOutSet).To(BeFalse())
					}, "10s", "1s").Should(Succeed())
				})
			})
		})

		Context("with a single escalating remediation", func() {

			Context("without multiple same kind support", func() {
				BeforeEach(func() {
					underTest.Spec.RemediationTemplate = nil
					underTest.Spec.EscalatingRemediations = []v1alpha1.EscalatingRemediation{
						{
							RemediationTemplate: *infraRemediationTemplateRef.DeepCopy(),
							Order:               0,
							Timeout:             metav1.Duration{Duration: time.Minute},
						},
					}
				})

				testReconcile()
			})

			Context("with multiple same kind support", func() {
				BeforeEach(func() {
					underTest.Spec.RemediationTemplate = nil
					underTest.Spec.EscalatingRemediations = []v1alpha1.EscalatingRemediation{
						{
							RemediationTemplate: *infraMultipleRemediationTemplateRef.DeepCopy(),
							Order:               0,
							Timeout:             metav1.Duration{Duration: time.Minute},
						},
					}
				})

				testReconcile()
			})
		})

		Context("with multiple escalating remediations", func() {
			firstRemediationTimeout := time.Second
			secondRemediationTimeout := 4 * time.Second
			thirdRemediationTimeout := time.Second
			fourthRemediationTimeout := time.Second
			BeforeEach(func() {
				mockLeaseParams(mockRequeueDurationIfLeaseTaken, mockDefaultLeaseDuration, mockLeaseBuffer)

				templateRef1 := underTest.Spec.RemediationTemplate
				underTest.Spec.RemediationTemplate = nil

				templateRef2 := templateRef1.DeepCopy()
				templateRef2.Kind = "Metal3RemediationTemplate"
				templateRef2.Name = "ok"
				templateRef2.Namespace = MachineNamespace

				underTest.Spec.EscalatingRemediations = []v1alpha1.EscalatingRemediation{
					{
						RemediationTemplate: *templateRef1,
						Order:               0,
						Timeout:             metav1.Duration{Duration: firstRemediationTimeout},
					},
					{
						RemediationTemplate: *templateRef2,
						Order:               5,
						Timeout:             metav1.Duration{Duration: secondRemediationTimeout},
					},
					{
						RemediationTemplate: *multiSupportTemplateRef,
						Order:               6,
						Timeout:             metav1.Duration{Duration: thirdRemediationTimeout},
					},
					{
						RemediationTemplate: *secondMultiSupportTemplateRef,
						Order:               8,
						Timeout:             metav1.Duration{Duration: fourthRemediationTimeout},
					},
				}

				setupObjects(1, 2, false)

			})

			It("it should try one remediation after another", func() {
				cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
				Expect(cr).To(BeNil())

				// wait until nodes are unhealthy
				Eventually(func(g Gomega) {
					cr = findRemediationCRForNHC(unhealthyNodeName, underTest)
					g.Expect(cr).ToNot(BeNil())
				}, time.Second*10, time.Millisecond*300).Should(Succeed())

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
					g.Expect(*underTest.Status.HealthyNodes).To(Equal(2))
					g.Expect(*underTest.Status.ObservedNodes).To(Equal(3))
					g.Expect(underTest.Status.UnhealthyNodes).To(HaveLen(1))
					g.Expect(underTest.Status.UnhealthyNodes[0].Name).To(Equal(unhealthyNodeName))
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations).To(HaveLen(1))
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Resource.GroupVersionKind()).To(Equal(cr.GroupVersionKind()))
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Resource.Name).To(Equal(cr.GetName()))
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Resource.Namespace).To(Equal(cr.GetNamespace()))
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Resource.UID).To(Equal(cr.GetUID()))
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Started).ToNot(BeNil())
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].TimedOut).To(BeNil())
					g.Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseRemediating))
				}, time.Second*10, time.Millisecond*300).Should(Succeed())

				// Verify lease is created
				lease := &coordv1.Lease{}
				err := k8sClient.Get(context.Background(), client.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)
				Expect(err).ToNot(HaveOccurred())
				Expect(*lease.Spec.LeaseDurationSeconds).To(Equal(int32(firstRemediationTimeout.Seconds() + mockLeaseBuffer.Seconds()) /*First escalation timeout (1) + buffer (1) */))
				Expect(lease.Spec.AcquireTime).ToNot(BeNil())
				Expect(*lease.Spec.AcquireTime).To(Equal(*lease.Spec.RenewTime))

				// Wait for 1st remediation to time out and 2nd to start
				Eventually(func(g Gomega) {
					// get updated CR
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)).To(Succeed())
					g.Expect(cr.GetAnnotations()).To(HaveKeyWithValue(Equal("remediation.medik8s.io/nhc-timed-out"), Not(BeNil())))

				}, time.Second*10, time.Millisecond*300).Should(Succeed())

				// Get new CR
				var newCr *unstructured.Unstructured
				Eventually(func(g Gomega) {
					newCr = findRemediationCRForNHCSecondRemediation(unhealthyNodeName, underTest)
					g.Expect(newCr).ToNot(BeNil())
				}, time.Second*10, time.Millisecond*300).Should(Succeed())

				// Get updated NHC
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Resource.GroupVersionKind()).To(Equal(cr.GroupVersionKind()))
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].TimedOut).ToNot(BeNil())
					g.Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseRemediating))

					g.Expect(*underTest.Status.HealthyNodes).To(Equal(2))
					g.Expect(*underTest.Status.ObservedNodes).To(Equal(3))
					g.Expect(underTest.Status.UnhealthyNodes).To(HaveLen(1))
					g.Expect(underTest.Status.UnhealthyNodes[0].Name).To(Equal(unhealthyNodeName))
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations).To(HaveLen(2))
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations[1].Resource.GroupVersionKind()).To(Equal(newCr.GroupVersionKind()))
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations[1].Resource.Name).To(Equal(newCr.GetName()))
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations[1].Resource.Namespace).To(Equal(newCr.GetNamespace()))
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations[1].Resource.UID).To(Equal(newCr.GetUID()))
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations[1].Started).ToNot(BeNil())
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations[1].TimedOut).To(BeNil())

				}, time.Second*10, time.Millisecond*300).Should(Succeed())

				// Verify lease was extended
				err = k8sClient.Get(context.Background(), client.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)
				Expect(err).ToNot(HaveOccurred())
				Expect(*lease.Spec.LeaseDurationSeconds).To(Equal(int32(secondRemediationTimeout.Seconds() + mockLeaseBuffer.Seconds())))
				Expect(lease.Spec.AcquireTime).ToNot(BeNil())
				Expect(lease.Spec.RenewTime.Sub(lease.Spec.AcquireTime.Time) > 0).To(BeTrue())

				// Wait for 2nd remediation to time out
				Eventually(func(g Gomega) {
					// get updated CR
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(newCr), newCr)).To(Succeed())
					g.Expect(cr.GetAnnotations()).To(HaveKeyWithValue(Equal("remediation.medik8s.io/nhc-timed-out"), Not(BeNil())))
				}, time.Second*10, time.Millisecond*300).Should(Succeed())

				// Get updated NHC
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations[1].Resource.GroupVersionKind()).To(Equal(newCr.GroupVersionKind()))
					g.Expect(underTest.Status.UnhealthyNodes[0].Remediations[1].TimedOut).ToNot(BeNil())
					g.Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseRemediating))

				}, time.Second*10, time.Millisecond*300).Should(Succeed())

				// Wait for 3rd remediation to start
				var thirdCR *unstructured.Unstructured
				Eventually(func(g Gomega) {
					thirdCR = findRemediationCRForTemplate(unhealthyNodeName, underTest, *multiSupportTemplateRef)
					g.Expect(thirdCR).ToNot(BeNil())
					g.Expect(thirdCR.GetName()).ToNot(Equal(unhealthyNodeName))
					g.Expect(thirdCR.GetAnnotations()[commonannotations.NodeNameAnnotation]).To(Equal(unhealthyNodeName))
					g.Expect(thirdCR.GetAnnotations()[annotations.TemplateNameAnnotation]).To(Equal(multiSupportTemplateRef.Name))
					g.Expect(thirdCR.GetAnnotations()).ToNot(HaveKey(Equal("remediation.medik8s.io/nhc-timed-out")))
				}, time.Second*10, time.Millisecond*300).Should(Succeed())

				// Wait for 3rd remediation to time out
				Eventually(func(g Gomega) {
					// get updated CR
					thirdCR = findRemediationCRForTemplate(unhealthyNodeName, underTest, *multiSupportTemplateRef)
					g.Expect(thirdCR).ToNot(BeNil())
					g.Expect(thirdCR.GetName()).ToNot(Equal(unhealthyNodeName))
					g.Expect(thirdCR.GetAnnotations()[commonannotations.NodeNameAnnotation]).To(Equal(unhealthyNodeName))
					g.Expect(thirdCR.GetAnnotations()[annotations.TemplateNameAnnotation]).To(Equal(multiSupportTemplateRef.Name))
					g.Expect(thirdCR.GetAnnotations()).To(HaveKeyWithValue(Equal("remediation.medik8s.io/nhc-timed-out"), Not(BeNil())))
				}, time.Second*10, time.Millisecond*300).Should(Succeed())

				// Wait for 4th remediation to start
				var fourthCR *unstructured.Unstructured
				Eventually(func(g Gomega) {
					fourthCR = findRemediationCRForTemplate(unhealthyNodeName, underTest, *secondMultiSupportTemplateRef)
					g.Expect(fourthCR).ToNot(BeNil())
					g.Expect(fourthCR.GetName()).ToNot(Equal(unhealthyNodeName))
					g.Expect(fourthCR.GetAnnotations()[commonannotations.NodeNameAnnotation]).To(Equal(unhealthyNodeName))
					g.Expect(fourthCR.GetAnnotations()[annotations.TemplateNameAnnotation]).To(Equal(secondMultiSupportTemplateRef.Name))
					g.Expect(fourthCR.GetAnnotations()).ToNot(HaveKey(Equal("remediation.medik8s.io/nhc-timed-out")))
				}, time.Second*10, time.Millisecond*300).Should(Succeed())

				// Verify lease still exist (since long expire time wasn't reached)
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)).ToNot(HaveOccurred())
					g.Expect(*lease.Spec.LeaseDurationSeconds).To(Equal(int32(secondRemediationTimeout.Seconds() + mockLeaseBuffer.Seconds())))
					g.Expect(lease.Spec.AcquireTime).ToNot(BeNil())

				}, time.Second*10, time.Millisecond*300).Should(Succeed())

				// make node healthy
				node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: unhealthyNodeName}}
				Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(node), node)).To(Succeed())
				node.Status.Conditions[0].Status = v1.ConditionTrue
				Expect(k8sClient.Status().Update(context.Background(), node)).To(Succeed())

				// Calculating time left for lease
				timeLeftOnLease := time.Duration(*lease.Spec.LeaseDurationSeconds)*time.Second - time.Now().Sub(lease.Spec.RenewTime.Time)
				// wait a bit
				time.Sleep(2 * time.Second)
				timeLeftOnLease = timeLeftOnLease - time.Second*2
				// Verify lease has time left before it should expire
				Expect(timeLeftOnLease > time.Millisecond*500).To(BeTrue()) // a bit over 1 second at this stage
				// Verify lease was removed because the CR was deleted (even though there was some time left)
				err = k8sClient.Get(context.Background(), client.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)
				Expect(errors.IsNotFound(err)).To(BeTrue())

				// Get updated NHC
				Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
				Expect(*underTest.Status.HealthyNodes).To(Equal(3))
				Expect(*underTest.Status.ObservedNodes).To(Equal(3))
				Expect(underTest.Status.UnhealthyNodes).To(HaveLen(0))
				Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseEnabled))

				// Ensure CRs are deleted
				Eventually(func(g Gomega) {
					err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
					err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(newCr), newCr)
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
					err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(thirdCR), thirdCR)
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
					err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(fourthCR), fourthCR)
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
				}, "5s", "200ms").Should(Succeed(), "CR wasn't deleted")
			})

			When("a Succeded remediation times out", func() {
				It("should not set timeout remediation and continue with the next remediation", func() {
					// first call should fail, because the node gets unready in a few seconds only
					cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
					Expect(cr).To(BeNil())

					// wait until nodes are unhealthy
					Eventually(func(g Gomega) {
						cr = findRemediationCRForNHC(unhealthyNodeName, underTest)
						g.Expect(cr).ToNot(BeNil())
					}, time.Second*10, time.Millisecond*300).Should(Succeed())

					// Set Remediation Succeded condition
					cr = updateStatusCondition(cr)
					Expect(k8sClient.Status().Update(context.Background(), cr)).To(Succeed())

					// Wait for 1st remediation to time out and 2nd to start
					lease := &coordv1.Lease{}
					err := k8sClient.Get(context.Background(), client.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)
					Expect(err).ToNot(HaveOccurred())

					leaseExpireTime := lease.Spec.AcquireTime.Time.Add(firstRemediationTimeout + mockLeaseBuffer)
					timeLeftForLease := leaseExpireTime.Sub(time.Now())
					time.Sleep(timeLeftForLease + time.Millisecond*100)

					// Verify that the remediation CR doesn't have the timeout annotation
					Consistently(func(g Gomega) {
						err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
						Expect(errors.IsNotFound(err)).To(BeFalse())
						_, isNhcTimeOutSet := cr.GetAnnotations()[commonannotations.NhcTimedOut]
						Expect(isNhcTimeOutSet).To(BeFalse())
					}, "10s", "1s").Should(Succeed())

					// Verify the 2nd remediation exists
					Eventually(func(g Gomega) {
						newCr := findRemediationCRForNHCSecondRemediation(unhealthyNodeName, underTest)
						g.Expect(newCr).ToNot(BeNil())
					}, time.Second*10, time.Millisecond*300).Should(Succeed())
				})
			})

			When("unhealthy condition changes", func() {

				It("should not proceed with next remediation when duration didn't expire yet", func() {
					// wait until nodes are unhealthy
					time.Sleep(nodeUnhealthyIn)

					cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
					Expect(cr).ToNot(BeNil())

					By("changing to other unhealthy condition")
					node := &v1.Node{}
					Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: unhealthyNodeName}, node)).To(Succeed())
					Expect(node.Status.Conditions[0].Status).ToNot(Equal(v1.ConditionFalse))
					node.Status.Conditions[0].Status = v1.ConditionFalse
					node.Status.Conditions[0].LastTransitionTime = metav1.Now()
					Expect(k8sClient.Status().Update(context.Background(), node)).To(Succeed())

					By("ensuring CR isn't deleted")
					Consistently(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)).To(Succeed())
					}, firstRemediationTimeout+2*time.Second, "500ms").Should(Succeed(), "CR was deleted")

					By("waiting for duration expiration")
					time.Sleep(unhealthyConditionDuration - firstRemediationTimeout)

					By("ensuring next remediation is processed now")
					// old CR timed out
					Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)).To(Succeed())
					Expect(cr.GetAnnotations()).To(HaveKeyWithValue(Equal("remediation.medik8s.io/nhc-timed-out"), Not(BeNil())))
					// new CR created
					newCr := findRemediationCRForNHCSecondRemediation(unhealthyNodeName, underTest)
					Expect(newCr).ToNot(BeNil())

					By("changing to healthy condition")
					Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: unhealthyNodeName}, node)).To(Succeed())
					node.Status.Conditions[0].Status = v1.ConditionTrue
					node.Status.Conditions[0].LastTransitionTime = metav1.Now()
					Expect(k8sClient.Status().Update(context.Background(), node)).To(Succeed())

					By("ensuring CRs are deleted")
					Eventually(func(g Gomega) {
						err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
						g.Expect(errors.IsNotFound(err)).To(BeTrue())
						err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(newCr), newCr)
						g.Expect(errors.IsNotFound(err)).To(BeTrue())
					}, "2s", "200ms").Should(Succeed(), "CR wasn't deleted")
				})

			})

		})

		Context("with Node marked for excluding remediation", func() {
			BeforeEach(func() {
				setupObjects(1, 2, true)
				node := objects[0].(*v1.Node)
				node.GetLabels()[commonLabels.ExcludeFromRemediation] = "true"

			})
			It("remediation shouldn't be created", func() {
				Expect(underTest.Status.UnhealthyNodes).To(HaveLen(1))
				Expect(underTest.Status.UnhealthyNodes[0].Remediations).To(HaveLen(0))
			})
		})

		Context("with Node setup to delay healthy", func() {
			BeforeEach(func() {
				setupObjects(1, 2, false)
			})
			When("Delay is set for 3 seconds", func() {
				BeforeEach(func() {
					underTest.Spec.HealthyDelay = &metav1.Duration{Duration: time.Second * 3}
				})
				It("remediation deletion should be delayed", func() {
					// First call should fail, because the node gets unready in a few seconds only
					cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
					Expect(cr).To(BeNil())

					// Wait until nodes are unhealthy
					Eventually(func(g Gomega) {
						cr = findRemediationCRForNHC(unhealthyNodeName, underTest)
						g.Expect(cr).ToNot(BeNil())
					}, time.Second*10, time.Millisecond*300).Should(Succeed())

					// Get updated NHC
					Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
					// Check delay status isn't applied until the node is healthy
					Expect(underTest.Status.UnhealthyNodes[0].HealthyDelayed).To(BeNil())

					mockNodeGettingHealthy(unhealthyNodeName)

					// Remediation shouldn't be removed even though the node is healthy because of delay
					Consistently(func(g Gomega) {
						cr = findRemediationCRForNHC(unhealthyNodeName, underTest)
						g.Expect(cr).ToNot(BeNil())
					}, time.Second*2, time.Millisecond*300).Should(Succeed())

					// Check healthy delay annotation on the CR
					Expect(cr.GetAnnotations()["remediation.medik8s.io/healthy-delay"]).ToNot(BeEmpty())

					// Get updated NHC
					Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
					// Check status was updated
					Expect(underTest.Status.UnhealthyNodes[0].HealthyDelayed).To(Equal(ptr.To(true)))

					// Delay is done, remediation should be removed
					Eventually(func(g Gomega) {
						cr = findRemediationCRForNHC(unhealthyNodeName, underTest)
						g.Expect(cr).To(BeNil())
					}, time.Second*3, time.Millisecond*300).Should(Succeed())

					// Get updated NHC
					Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())

					// Check unhealthy was removed
					Eventually(func(g Gomega) {
						g.Expect(len(underTest.Status.UnhealthyNodes)).To(BeZero())
					}, time.Second*3, time.Millisecond*300).Should(Succeed())

				})
			})
			When("Delay is set indefinitely", func() {
				BeforeEach(func() {
					underTest.Spec.HealthyDelay = &metav1.Duration{Duration: time.Second * -1}
				})
				It("remediation should only be deleted after node is manually confirmed to be healthy", func() {
					// First call should fail, because the node gets unready in a few seconds only
					cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
					Expect(cr).To(BeNil())

					// Wait until nodes are unhealthy
					Eventually(func(g Gomega) {
						cr = findRemediationCRForNHC(unhealthyNodeName, underTest)
						g.Expect(cr).ToNot(BeNil())
					}, time.Second*10, time.Millisecond*300).Should(Succeed())

					// Get updated NHC
					Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
					// Check Delay status isn't applied until the node is healthy
					Expect(underTest.Status.UnhealthyNodes[0].HealthyDelayed).To(BeNil())

					// Simulate node getting healthy
					mockNodeGettingHealthy(unhealthyNodeName)

					// Remediation shouldn't be removed even though the node is healthy because of delay
					Consistently(func(g Gomega) {
						cr = findRemediationCRForNHC(unhealthyNodeName, underTest)
						g.Expect(cr).ToNot(BeNil())
					}, time.Second*5, time.Millisecond*300).Should(Succeed())

					// Mock user intervention marking the node as healthy by using manually-confirmed-healthy annotation
					unhealthyNode := &v1.Node{}
					Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: unhealthyNodeName}, unhealthyNode)).To(Succeed())
					unhealthyNode.SetAnnotations(map[string]string{resources.RemediationManuallyConfirmedHealthyAnnotationKey: "true"})
					Expect(k8sClient.Update(context.Background(), unhealthyNode)).To(Succeed())

					// User confirmed node as healthy so remediation should be removed
					Eventually(func(g Gomega) {
						cr = findRemediationCRForNHC(unhealthyNodeName, underTest)
						g.Expect(cr).To(BeNil())
					}, time.Second*3, time.Millisecond*300).Should(Succeed())

					// Get updated NHC
					Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())

					// Check unhealthy was removed
					Eventually(func(g Gomega) {
						g.Expect(len(underTest.Status.UnhealthyNodes)).To(BeZero())
					}, time.Second*3, time.Millisecond*300).Should(Succeed())

					// Check node annotation was removed
					remediatedNode := &v1.Node{}
					Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: unhealthyNodeName}, remediatedNode))
					_, found := remediatedNode.GetAnnotations()[resources.RemediationManuallyConfirmedHealthyAnnotationKey]
					Expect(found).To(BeFalse())
				})
			})
		})

		Context("with progressing condition being set", func() {

			BeforeEach(func() {
				templateRef1 := underTest.Spec.RemediationTemplate
				underTest.Spec.RemediationTemplate = nil
				underTest.Spec.EscalatingRemediations = []v1alpha1.EscalatingRemediation{
					{
						RemediationTemplate: *templateRef1,
						Order:               0,
						Timeout:             metav1.Duration{Duration: 5 * time.Minute},
					},
				}
				setupObjects(1, 2, true)
			})

			It("it should timeout early", func() {
				cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
				Expect(cr).ToNot(BeNil())

				Expect(underTest.Status.UnhealthyNodes).To(HaveLen(1))
				Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].Started).ToNot(BeNil())
				Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].TimedOut).To(BeNil())

				By("letting the remediation stop progressing")
				conditions := []interface{}{
					map[string]interface{}{
						"type":               "Succeeded",
						"status":             "False",
						"lastTransitionTime": time.Now().Format(time.RFC3339),
					},
				}
				unstructured.SetNestedSlice(cr.Object, conditions, "status", "conditions")
				Expect(k8sClient.Status().Update(context.Background(), cr))

				// Wait for hardcoded timeout to expire
				time.Sleep(5 * time.Second)

				// get updated CR
				Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)).To(Succeed())
				Expect(cr.GetAnnotations()).To(HaveKeyWithValue(Equal("remediation.medik8s.io/nhc-timed-out"), Not(BeNil())))

				// get updated NHC
				Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
				Expect(underTest.Status.UnhealthyNodes[0].Remediations[0].TimedOut).ToNot(BeNil())
				Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseRemediating))
			})
		})

		Context("with expected permanent node deletion", func() {

			BeforeEach(func() {
				// TODO will work with classic remediation as well when https://github.com/medik8s/node-healthcheck-operator/pull/230 is merged
				templateRef1 := underTest.Spec.RemediationTemplate
				underTest.Spec.RemediationTemplate = nil
				underTest.Spec.EscalatingRemediations = []v1alpha1.EscalatingRemediation{
					{
						RemediationTemplate: *templateRef1,
						Order:               0,
						Timeout:             metav1.Duration{Duration: 5 * time.Minute},
					},
				}
				setupObjects(1, 2, true)
			})

			deleteNode := func() {
				By("deleting the node")
				node := &v1.Node{}
				node.Name = unhealthyNodeName
				Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(node), node)).To(Succeed())
				Expect(k8sClient.Delete(context.Background(), node))
			}

			markCR := func(succeededStatus metav1.ConditionStatus) *unstructured.Unstructured {
				By("marking CR for permanent node deletion expected")
				cr := findRemediationCRForNHC("unhealthy-worker-node-1", underTest)
				Expect(cr).ToNot(BeNil())

				conditions := []interface{}{
					map[string]interface{}{
						"type":               commonconditions.SucceededType,
						"status":             string(succeededStatus),
						"lastTransitionTime": time.Now().Format(time.RFC3339),
					},
					map[string]interface{}{
						"type":               commonconditions.PermanentNodeDeletionExpectedType,
						"status":             "True",
						"lastTransitionTime": time.Now().Format(time.RFC3339),
					},
				}
				unstructured.SetNestedSlice(cr.Object, conditions, "status", "conditions")
				Expect(k8sClient.Status().Update(context.Background(), cr))
				return cr
			}

			expectCRDeletion := func(cr *unstructured.Unstructured) {
				By("waiting for CR to be deleted")
				Eventually(func() bool {
					err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
					return errors.IsNotFound(err)
				}, "2s", "200ms").Should(BeTrue())

				// get updated NHC
				Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
				Expect(underTest.Status.UnhealthyNodes).To(BeEmpty())
			}

			ensureCRNotDeleted := func(cr *unstructured.Unstructured) {
				By("ensuring CR is not deleted")
				Consistently(func() error {
					return k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
				}, "10s", "1s").ShouldNot(HaveOccurred())

				// get updated NHC
				Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
				Expect(underTest.Status.UnhealthyNodes).ToNot(BeEmpty())
			}

			It("it should delete orphaned CR when CR with expected node deletion succeeded", func() {
				Expect(underTest.Status.UnhealthyNodes).To(HaveLen(1))
				cr := markCR(metav1.ConditionFalse)
				deleteNode()
				ensureCRNotDeleted(cr)
				cr = markCR(metav1.ConditionTrue)
				expectCRDeletion(cr)
			})

			It("it should delete orphaned CR when node is deleted", func() {
				Expect(underTest.Status.UnhealthyNodes).To(HaveLen(1))
				cr := findRemediationCRForNHC("unhealthy-worker-node-1", underTest)
				Expect(cr).ToNot(BeNil())
				deleteNode()
				expectCRDeletion(cr)
			})
		})

		Context("control plane nodes", func() {

			var pdb *policyv1.PodDisruptionBudget
			pdbSelector := map[string]string{
				"app": "guard",
			}

			createGuardPod := func(nodeName string, ready bool) {
				pod := &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "guard-pod-",
						Namespace:    pdb.Namespace,
						Labels:       pdbSelector,
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:  "test",
								Image: "test",
							},
						},
						NodeName: nodeName,
					},
				}
				Expect(k8sClient.Create(context.Background(), pod)).To(Succeed())
				DeferCleanup(func() {
					Expect(k8sClient.Delete(context.Background(), pod, &client.DeleteOptions{GracePeriodSeconds: pointer.Int64(0)})).To(Succeed())
				})

				// update pod status
				Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), pod)).To(Succeed())
				status := v1.ConditionTrue
				if !ready {
					status = v1.ConditionFalse
				}
				pod.Status.Conditions = []v1.PodCondition{
					{
						Type:   v1.PodReady,
						Status: status,
					},
				}
				Expect(k8sClient.Status().Update(context.Background(), pod)).To(Succeed())
			}

			updatePdb := func(disruptionAllowed bool) {
				Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pdb), pdb)).To(Succeed())
				pdb.Status.DisruptionsAllowed = 1
				if !disruptionAllowed {
					pdb.Status.DisruptionsAllowed = 0
				}
				Expect(k8sClient.Status().Update(context.Background(), pdb)).To(Succeed())
			}

			BeforeEach(func() {
				// create etcd namespace
				ns := &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "openshift-etcd",
					},
				}
				// namespaces can't be deleted!
				err := k8sClient.Create(context.Background(), ns)
				if err != nil {
					Expect(errors.IsAlreadyExists(err)).To(BeTrue())
				}

				// create PDB
				pdb = &policyv1.PodDisruptionBudget{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "some-name",
						Namespace: "openshift-etcd",
					},
					Spec: policyv1.PodDisruptionBudgetSpec{
						Selector: &metav1.LabelSelector{MatchLabels: pdbSelector},
					},
				}
				Expect(k8sClient.Create(context.Background(), pdb)).To(Succeed())
				DeferCleanup(func() {
					Expect(k8sClient.Delete(context.Background(), pdb)).To(Succeed())
				})

				updatePdb(true)

			})

			When("two control plane nodes are unhealthy", func() {
				BeforeEach(func() {
					objects = newNodes(2, 1, true, true)
					objects = append(objects, newNodes(1, 5, false, true)...)
					underTest = newNodeHealthCheck()
					objects = append(objects, underTest)
					updatePdb(false)
					createGuardPod("unhealthy-control-plane-node-1", false)
					createGuardPod("unhealthy-control-plane-node-2", false)
				})

				It("should remediate one after another", func() {

					var remediatedUnhealthyCPNodeName string
					Eventually(func(g Gomega) {
						for _, unhealthyNode := range underTest.Status.UnhealthyNodes {
							if strings.Contains(unhealthyNode.Name, "unhealthy-control-plane-node") &&
								len(unhealthyNode.Remediations) == 1 {
								remediatedUnhealthyCPNodeName = unhealthyNode.Name
								break
							}
						}
						Expect(remediatedUnhealthyCPNodeName).ToNot(BeEmpty())
					}, "2s", "100ms").Should(Succeed())

					cr := newBaseCRs(underTest)[0]
					crList := &unstructured.UnstructuredList{Object: cr.Object}
					Expect(k8sClient.List(context.Background(), crList)).To(Succeed())

					Expect(len(crList.Items)).To(BeNumerically("==", 2), "expected 2 remediations, one for control plane, one for worker")
					Expect(crList.Items).To(ContainElements(
						// the unhealthy worker
						HaveField("Object", HaveKeyWithValue("metadata", HaveKeyWithValue("name", ContainSubstring(unhealthyNodeName)))),
						// one of the unhealthy control plane nodes
						HaveField("Object", HaveKeyWithValue("metadata", HaveKeyWithValue("name", ContainSubstring(remediatedUnhealthyCPNodeName)))),
					))
					Expect(*underTest.Status.HealthyNodes).To(Equal(6))
					Expect(*underTest.Status.ObservedNodes).To(Equal(9))
					Expect(underTest.Status.UnhealthyNodes).To(HaveLen(3))
					Expect(underTest.Status.UnhealthyNodes).To(ContainElements(
						And(
							HaveField("Name", unhealthyNodeName),
							HaveField("Remediations", ContainElement(
								And(
									HaveField("Resource.Name", ContainSubstring(unhealthyNodeName)),
									HaveField("Started", Not(BeNil())),
									HaveField("TimedOut", BeNil()),
								),
							)),
						),
						And(
							HaveField("Name", remediatedUnhealthyCPNodeName),
							HaveField("Remediations", ContainElement(
								And(
									HaveField("Resource.Name", ContainSubstring(remediatedUnhealthyCPNodeName)),
									HaveField("Started", Not(BeNil())),
									HaveField("TimedOut", BeNil()),
								),
							)),
						),
					))

					By("simulating remediator by putting a finalizer on the cp remediation CR")
					unhealthyCPNodeCR := findRemediationCRForNHC(remediatedUnhealthyCPNodeName, underTest)
					Expect(unhealthyCPNodeCR).ToNot(BeNil())
					unhealthyCPNodeCR.SetFinalizers([]string{"dummy"})
					Expect(k8sClient.Update(context.Background(), unhealthyCPNodeCR)).To(Succeed())

					unremediatedUnhealthyCPNodeName := "unhealthy-control-plane-node-1"
					if unremediatedUnhealthyCPNodeName == remediatedUnhealthyCPNodeName {
						unremediatedUnhealthyCPNodeName = "unhealthy-control-plane-node-2"
					}

					// verify correct events for skipping remediation
					verifyEvent(v1.EventTypeWarning, utils.EventReasonRemediationSkipped, fmt.Sprintf("Skipping remediation of %s for preventing control plane / etcd quorum loss, going to retry in a minute", unremediatedUnhealthyCPNodeName))
					verifyNoEvent(v1.EventTypeWarning, utils.EventReasonRemediationSkipped, fmt.Sprintf("Skipping remediation of %s for preventing control plane / etcd quorum loss, going to retry in a minute", remediatedUnhealthyCPNodeName))

					By("make cp node healthy")
					unhealthyCPNode := &v1.Node{}
					unhealthyCPNode.Name = remediatedUnhealthyCPNodeName
					Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(unhealthyCPNode), unhealthyCPNode)).To(Succeed())
					unhealthyCPNode.Status.Conditions = []v1.NodeCondition{
						{
							Type:   v1.NodeReady,
							Status: v1.ConditionTrue,
						},
					}
					Expect(k8sClient.Status().Update(context.Background(), unhealthyCPNode))

					By("waiting for remediation end of cp node")
					Eventually(func(g Gomega) *metav1.Time {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(unhealthyCPNodeCR), unhealthyCPNodeCR)).To(Succeed())
						return unhealthyCPNodeCR.GetDeletionTimestamp()
					}, "2s", "100ms").ShouldNot(BeNil(), "expected CR to be deleted")

					By("ensuring other cp node isn't remediated yet")
					Consistently(func(g Gomega) bool {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
						for _, unhealthyNode := range underTest.Status.UnhealthyNodes {
							if strings.Contains(unhealthyNode.Name, "unhealthy-control-plane-node") &&
								unhealthyNode.Name != remediatedUnhealthyCPNodeName {
								return true
							}
						}
						return false

					}, "5s", "1s").Should(BeTrue())

					By("simulating remediator finished by removing finalizer on the cp remediation CR")
					Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(unhealthyCPNodeCR), unhealthyCPNodeCR)).To(Succeed())
					unhealthyCPNodeCR.SetFinalizers([]string{})
					Expect(k8sClient.Update(context.Background(), unhealthyCPNodeCR)).To(Succeed())

					By("ensuring other cp node is remediated now")
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
						g.Expect(underTest.Status.UnhealthyNodes).To(HaveLen(2))
						g.Expect(underTest.Status.UnhealthyNodes).To(ContainElements(
							And(
								HaveField("Name", unremediatedUnhealthyCPNodeName),
								// ensure it's the other cp node now
								Not(HaveField("Name", remediatedUnhealthyCPNodeName)),
								HaveField("Remediations", ContainElement(
									And(
										HaveField("Resource.Name", ContainSubstring("unhealthy-control-plane-node")),
										HaveField("Started", Not(BeNil())),
										HaveField("TimedOut", BeNil()),
									),
								)),
							),
						))
					}, "2s", "100ms").Should(Succeed())
					crList = &unstructured.UnstructuredList{Object: cr.Object}
					Expect(k8sClient.List(context.Background(), crList)).To(Succeed())
					Expect(len(crList.Items)).To(BeNumerically("==", 2), "expected 2 remediations, one for control plane, one for worker")
					Expect(crList.Items).To(ContainElements(
						// the unhealthy worker
						HaveField("Object", HaveKeyWithValue("metadata", HaveKeyWithValue("name", ContainSubstring("unhealthy-worker-node-1")))),
						// the other unhealthy control plane nodes
						HaveField("Object", HaveKeyWithValue("metadata", HaveKeyWithValue("name", ContainSubstring("unhealthy-control-plane-node")))),
					))
					Expect(crList.Items).ToNot(ContainElements(
						// the old unhealthy control plane node
						HaveField("Object", HaveKeyWithValue("metadata", HaveKeyWithValue("name", ContainSubstring(remediatedUnhealthyCPNodeName)))),
					))

				})
			})

			Context("one control plane node is unhealthy, and DisruptionsAllowed = 0", func() {
				BeforeEach(func() {
					objects = newNodes(1, 2, true, true)
					underTest = newNodeHealthCheck()
					objects = append(objects, underTest)
				})

				When("unhealthy node is not disrupted already (guard pod is ready)", func() {

					BeforeEach(func() {
						updatePdb(false)
						createGuardPod("unhealthy-control-plane-node-1", true)
					})

					It("doesn't create a remediation CR for control plane node", func() {
						Consistently(func(g Gomega) {
							cr := findRemediationCRForNHC("unhealthy-control-plane-node-1", underTest)
							Expect(cr).To(BeNil())

							Expect(*underTest.Status.HealthyNodes).To(Equal(2))
							Expect(*underTest.Status.ObservedNodes).To(Equal(3))
							Expect(underTest.Status.UnhealthyNodes).To(HaveLen(1))
							Expect(underTest.Status.UnhealthyNodes).To(ContainElements(
								And(
									HaveField("Name", "unhealthy-control-plane-node-1"),
									HaveField("Remediations", BeNil()),
								),
							))
						}, "2s", "200ms").Should(Succeed())
					})
				})

				When("unhealthy node is disrupted already (guard pod is not ready)", func() {

					BeforeEach(func() {
						updatePdb(false)
						createGuardPod("unhealthy-control-plane-node-1", false)
					})

					It("does create a remediation CR for control plane node", func() {
						Eventually(func(g Gomega) {
							cr := findRemediationCRForNHC("unhealthy-control-plane-node-1", underTest)
							g.Expect(cr).ToNot(BeNil())

							g.Expect(*underTest.Status.HealthyNodes).To(Equal(2))
							g.Expect(*underTest.Status.ObservedNodes).To(Equal(3))
							g.Expect(underTest.Status.UnhealthyNodes).To(HaveLen(1))
							g.Expect(underTest.Status.UnhealthyNodes).To(ContainElements(
								And(
									HaveField("Name", "unhealthy-control-plane-node-1"),
									HaveField("Remediations", Not(BeNil())),
								),
							))
						}, "2s", "200ms").Should(Succeed())
					})
				})

				When("unhealthy node has no guard pod (node doesn't run etcd or guard pod was deleted)", func() {

					It("does create a remediation CR for control plane node", func() {
						Eventually(func(g Gomega) {
							cr := findRemediationCRForNHC("unhealthy-control-plane-node-1", underTest)
							g.Expect(cr).ToNot(BeNil())

							g.Expect(*underTest.Status.HealthyNodes).To(Equal(2))
							g.Expect(*underTest.Status.ObservedNodes).To(Equal(3))
							g.Expect(underTest.Status.UnhealthyNodes).To(HaveLen(1))
							g.Expect(underTest.Status.UnhealthyNodes).To(ContainElements(
								And(
									HaveField("Name", "unhealthy-control-plane-node-1"),
									HaveField("Remediations", Not(BeNil())),
								),
							))
						}, "2s", "200ms").Should(Succeed())
					})
				})
			})

		})

		When("remediation is needed but pauseRequests exists", func() {
			BeforeEach(func() {
				setupObjects(1, 2, true)
				underTest.Spec.PauseRequests = []string{"I'm an admin, asking you to stop remediating this group of nodes"}
			})

			It("skips remediation and updates status", func() {
				cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
				Expect(cr).To(BeNil())

				Expect(*underTest.Status.HealthyNodes).To(Equal(0))
				Expect(*underTest.Status.ObservedNodes).To(Equal(0))
				Expect(underTest.Status.UnhealthyNodes).To(BeEmpty())
				Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhasePaused))
				Expect(underTest.Status.Reason).ToNot(BeEmpty())
			})
		})

		When("Nodes are candidates for remediation and cluster is upgrading", func() {
			BeforeEach(func() {
				clusterUpgradeRequeueAfter = 5 * time.Second
				setupObjects(1, 2, true)
			})
			When("non HCP Upgrade", func() {
				BeforeEach(func() {
					upgradeChecker.Upgrading = true
				})

				AfterEach(func() {
					upgradeChecker.Upgrading = false
				})

				It("doesn't not remediate but requeues reconciliation and updates status", func() {
					cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
					Expect(cr).To(BeNil())

					Expect(*underTest.Status.HealthyNodes).To(Equal(0))
					Expect(*underTest.Status.ObservedNodes).To(Equal(0))
					Expect(underTest.Status.UnhealthyNodes).To(BeEmpty())
					Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseEnabled))
					Expect(underTest.Status.Reason).ToNot(BeEmpty())

					By("stopping upgrade and waiting for requeue")
					upgradeChecker.Upgrading = false
					time.Sleep(10 * time.Second)
					cr = findRemediationCRForNHC(unhealthyNodeName, underTest)
					Expect(cr).ToNot(BeNil())

					Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
					Expect(*underTest.Status.HealthyNodes).To(Equal(2))
					Expect(*underTest.Status.ObservedNodes).To(Equal(3))
					Expect(underTest.Status.UnhealthyNodes).To(HaveLen(1))
				})

			})
			When("HCP Upgrade", func() {
				var currentMachineConfigAnnotationKey = "machineconfiguration.openshift.io/currentConfig"
				var desiredMachineConfigAnnotationKey = "machineconfiguration.openshift.io/desiredConfig"
				BeforeEach(func() {
					// Use a real upgrade checker instead of mock
					prevUpgradeChecker := nhcReconciler.ClusterUpgradeStatusChecker
					nhcReconciler.ClusterUpgradeStatusChecker = ocpUpgradeChecker
					DeferCleanup(func() {
						nhcReconciler.ClusterUpgradeStatusChecker = prevUpgradeChecker
					})

					// Simulate HCP Upgrade on the unhealthy node
					upgradingNode := objects[0]
					upgradingNodeAnnotations := map[string]string{}
					if upgradingNode.GetAnnotations() != nil {
						upgradingNodeAnnotations = upgradingNode.GetAnnotations()
					}
					upgradingNodeAnnotations[currentMachineConfigAnnotationKey] = "fakeVersion1"
					upgradingNodeAnnotations[desiredMachineConfigAnnotationKey] = "fakeVersion2"
					upgradingNode.SetAnnotations(upgradingNodeAnnotations)

				})

				It("doesn't not remediate but requeues reconciliation and updates status", func() {

					cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
					Expect(cr).To(BeNil())

					Expect(*underTest.Status.HealthyNodes).To(Equal(0))
					Expect(*underTest.Status.ObservedNodes).To(Equal(0))
					Expect(underTest.Status.UnhealthyNodes).To(BeEmpty())
					Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseEnabled))
					Expect(underTest.Status.Reason).ToNot(BeEmpty())

					By("stopping upgrade and waiting for requeue")
					unhealthyNode := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: unhealthyNodeName}}
					Expect(k8sClient.Get(context.TODO(), client.ObjectKeyFromObject(unhealthyNode), unhealthyNode)).To(Succeed())
					if unhealthyNode.GetAnnotations()[currentMachineConfigAnnotationKey] != unhealthyNode.GetAnnotations()[desiredMachineConfigAnnotationKey] {
						// Simulating upgrade complete.
						unhealthyNode.Annotations[currentMachineConfigAnnotationKey] = unhealthyNode.GetAnnotations()[desiredMachineConfigAnnotationKey]
						Expect(k8sClient.Update(context.TODO(), unhealthyNode)).To(Succeed())
					}

					time.Sleep(10 * time.Second)
					cr = findRemediationCRForNHC(unhealthyNodeName, underTest)
					Expect(cr).ToNot(BeNil())

					Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
					Expect(*underTest.Status.HealthyNodes).To(Equal(2))
					Expect(*underTest.Status.ObservedNodes).To(Equal(3))
					Expect(underTest.Status.UnhealthyNodes).To(HaveLen(1))
				})

			})
		})

		When("Node label changes", func() {

			var unlabeledNode *v1.Node

			BeforeEach(func() {
				setupObjects(0, 3, false)

				// set label on some of the nodes
				nrLabeled := 0
				for _, obj := range objects {
					switch obj.(type) {
					case *v1.Node:
						if nrLabeled < 2 {
							obj.(*v1.Node).Labels = map[string]string{"foo": "bar"}
							nrLabeled++
						} else {
							unlabeledNode = obj.(*v1.Node)
						}
					}
				}
				setSelectorOnNHC(objects, metav1.LabelSelector{
					MatchLabels: map[string]string{"foo": "bar"},
				})
			})

			It("should reconcile and update observed nodes", func() {
				Expect(*underTest.Status.ObservedNodes).To(Equal(2), "expected 2 observed nodes")

				// update label and wait for update nr of observed nodes
				unlabeledNode.Labels = map[string]string{"foo": "bar"}
				Expect(k8sClient.Update(context.Background(), unlabeledNode)).To(Succeed())
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
					g.Expect(*underTest.Status.ObservedNodes).To(Equal(3), "expected 3 observed nodes after label update")
				}, "2s", "100ms").Should(Succeed())
			})
		})

		Context("Machine owners", func() {
			When("Metal3RemediationTemplate is in correct namespace", func() {

				var machine *machinev1beta1.Machine

				BeforeEach(func() {
					setupObjects(1, 2, true)

					// create machine
					machine = &machinev1beta1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-machine",
							Namespace: MachineNamespace,
						},
					}
					objects = append(objects, machine)

					// set machine annotation to unhealthy node
					for _, o := range objects {
						o := o
						if o.GetName() == unhealthyNodeName {
							ann := make(map[string]string)
							ann["machine.openshift.io/machine"] = fmt.Sprintf("%s/%s", machine.Namespace, machine.Name)
							o.SetAnnotations(ann)
						}
					}

					// set metal3 template
					underTest.Spec.RemediationTemplate.Kind = "Metal3RemediationTemplate"
					underTest.Spec.RemediationTemplate.Name = "ok"
					underTest.Spec.RemediationTemplate.Namespace = MachineNamespace

				})

				It("should set owner ref to the machine", func() {
					cr := findRemediationCRForNHC(unhealthyNodeName, underTest)
					Expect(cr).ToNot(BeNil())
					Expect(cr.GetOwnerReferences()).To(
						ContainElement(
							And(
								// Kind and API version aren't set on underTest, envtest issue...
								// Controller is empty for HaveField because false is the zero value?
								HaveField("Name", machine.Name),
								HaveField("UID", machine.UID),
							),
						),
					)
				})
			})

		})

		Context("Storm Recovery", func() {
			var stormCooldownDuration = time.Second * 2
			BeforeEach(func() {
				underTest = newNodeHealthCheckWithStormRecovery(stormCooldownDuration, 4)
				setupObjects(3, 4, true) // 3 unhealthy, 4 healthy = 7 total
			})
			When("consecutive storms are triggered", func() {
				It("they should start and finish according delay and min max criteria", func() {
					// Phase 1: Verify initial state - normal operation
					By("verifying initial normal operation")
					// 7 total, 4 healthy, 3 unhealthy, 3 remediations
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
						g.Expect(*underTest.Status.HealthyNodes).To(Equal(4))
						g.Expect(len(underTest.Status.UnhealthyNodes)).To(Equal(3))
						g.Expect(utils.IsConditionSet(underTest.Status.Conditions, v1alpha1.ConditionTypeStormActive, v1alpha1.ConditionReasonStormThresholdChange)).To(BeFalse())
						g.Expect(getRemediationsCount(underTest)).To(Equal(3))
					}, "5s", "1s").Should(Succeed())

					// Phase 2: Make the fourth node unhealthy - triggers first storm recovery
					By("making one more node unhealthy - triggers storm recovery")
					mockNodeGettingUnhealthy("healthy-worker-node-1")
					// wait for node to turn unhealthy
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
						g.Expect(len(underTest.Status.UnhealthyNodes)).To(Equal(4))
					}, "15s", "100ms").Should(Succeed())

					// Verify Storm recovery is activated
					// 7 total, 3 healthy, 4 unhealthy, 3 remediations (additional remediation not created because of min healthy constraint)
					Consistently(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
						g.Expect(*underTest.Status.HealthyNodes).To(Equal(3))
						g.Expect(len(underTest.Status.UnhealthyNodes)).To(Equal(4))
						g.Expect(getRemediationsCount(underTest)).To(Equal(3))
						g.Expect(utils.IsConditionTrue(underTest.Status.Conditions, v1alpha1.ConditionTypeStormActive, v1alpha1.ConditionReasonStormThresholdChange)).To(BeTrue())
					}, stormCooldownDuration+time.Second, "100ms").Should(Succeed())

					// Phase 3: Recover one node - storm recovery remains active due to 2-second delay
					By("recovering one node - storm recovery remains active due to 2-second delay")
					mockNodeGettingHealthy("unhealthy-worker-node-1")

					// wait for node to recover
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
						g.Expect(*underTest.Status.HealthyNodes).To(Equal(4))
					}, "5s", "100ms").Should(Succeed())
					// 7 total, 4 healthy, 3 unhealthy, 2 remediations (one removed due to healthy node), 1 remediation is pending to be created (not created because of storm)

					var stormTerminationStartTime time.Time
					// Wait for StormCooldownActive condition to be set
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
						cooldownCondition := meta.FindStatusCondition(underTest.Status.Conditions, v1alpha1.ConditionTypeStormCooldownActive)
						g.Expect(cooldownCondition).ToNot(BeNil())
						g.Expect(cooldownCondition.Status).To(Equal(metav1.ConditionTrue))
						stormTerminationStartTime = cooldownCondition.LastTransitionTime.Time
					}, "5s", "100ms").Should(Succeed())

					timeUntilStormIsDone := stormTerminationStartTime.Add(stormCooldownDuration).Sub(time.Now())

					Consistently(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
						g.Expect(*underTest.Status.HealthyNodes).To(Equal(4))
						g.Expect(len(underTest.Status.UnhealthyNodes)).To(Equal(3))
						g.Expect(getRemediationsCount(underTest)).To(Equal(2))
						g.Expect(utils.IsConditionTrueAnyReason(underTest.Status.Conditions, v1alpha1.ConditionTypeStormCooldownActive)).To(BeTrue())
					}, timeUntilStormIsDone-time.Millisecond*100, "100ms").Should(Succeed())
					// Expected termination time of the first storm
					cooldownCondition := meta.FindStatusCondition(underTest.Status.Conditions, v1alpha1.ConditionTypeStormCooldownActive)
					firstStormTerminationTime := cooldownCondition.LastTransitionTime.Time.Add(underTest.Spec.StormCooldownDuration.Duration)

					// Expect Storm Recovery mode to end
					// 7 total, 4 healthy, 3 unhealthy, 3 remediations (pending remediation created when storm is done)
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
						g.Expect(utils.IsConditionSet(underTest.Status.Conditions, v1alpha1.ConditionTypeStormActive, v1alpha1.ConditionReasonStormThresholdChange)).To(BeTrue())
						g.Expect(utils.IsConditionTrue(underTest.Status.Conditions, v1alpha1.ConditionTypeStormActive, v1alpha1.ConditionReasonStormThresholdChange)).To(BeFalse())
						g.Expect(getRemediationsCount(underTest)).To(Equal(3))
					}, "2s", "100ms").Should(Succeed())

					// Phase 5:  Make the fourth node unhealthy - triggers the second storm
					By("making the 4th node unhealthy - triggers second storm recovery")
					mockNodeGettingUnhealthy("healthy-worker-node-2")
					// wait for node to turn unhealthy
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
						g.Expect(len(underTest.Status.UnhealthyNodes)).To(Equal(4))
					}, "15s", "100ms").Should(Succeed())

					// Verify Second Storm mode is activated
					// 7 total, 3 healthy, 4 unhealthy, 3 remediations (additional remediation not created because of min healthy constraint)
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
						g.Expect(*underTest.Status.HealthyNodes).To(Equal(3))
						g.Expect(len(underTest.Status.UnhealthyNodes)).To(Equal(4))
						g.Expect(getRemediationsCount(underTest)).To(Equal(3))
						g.Expect(utils.IsConditionTrue(underTest.Status.Conditions, v1alpha1.ConditionTypeStormActive, v1alpha1.ConditionReasonStormThresholdChange)).To(BeTrue())
						stormActiveCondition := meta.FindStatusCondition(underTest.Status.Conditions, v1alpha1.ConditionTypeStormActive)
						// Verifies StormRecoveryStartTime is updated properly when a second storm starts
						g.Expect(stormActiveCondition.LastTransitionTime.After(firstStormTerminationTime)).To(BeTrue())
						// Verifies StormCooldownActive condition of the first storm is cleared
						cooldownCondition := meta.FindStatusCondition(underTest.Status.Conditions, v1alpha1.ConditionTypeStormCooldownActive)
						g.Expect(cooldownCondition.Status).To(Equal(metav1.ConditionFalse))
					}, "5s", "100ms").Should(Succeed())
				})
			})
		})

	})

	// TODO move to new suite in utils package
	Context("Controller Watches", func() {
		var (
			underTest1 *v1alpha1.NodeHealthCheck
			underTest2 *v1alpha1.NodeHealthCheck
			objects    []client.Object
		)

		JustBeforeEach(func() {
			createObjects(objects...)
			time.Sleep(2 * time.Second)
		})

		AfterEach(func() {
			deleteObjects(objects...)
			time.Sleep(1 * time.Second)
		})

		When("a node changes status and is selectable by one NHC selector", func() {
			BeforeEach(func() {
				objects = newNodes(3, 10, false, true)
				underTest1 = newNodeHealthCheck()
				underTest2 = newNodeHealthCheck()
				underTest2.Name = "test-2"
				emptySelector, _ := metav1.ParseToLabelSelector("fooLabel=bar")
				underTest2.Spec.Selector = *emptySelector
				objects = append(objects, underTest1, underTest2)
			})

			It("creates a reconcile request", func() {
				handler := utils.NHCByNodeMapperFunc(k8sClient, controllerruntime.Log)
				updatedNode := v1.Node{
					ObjectMeta: controllerruntime.ObjectMeta{Name: "healthy-worker-node-1"},
				}
				requests := handler(context.TODO(), &updatedNode)
				Expect(len(requests)).To(Equal(1))
				Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: underTest1.GetName()}}))
			})
		})

		When("a node changes status and is selectable by the more 2 NHC selector", func() {
			BeforeEach(func() {
				objects = newNodes(3, 10, false, true)
				underTest1 = newNodeHealthCheck()
				underTest2 = newNodeHealthCheck()
				underTest2.Name = "test-2"
				objects = append(objects, underTest1, underTest2)
			})

			It("creates 2 reconcile requests", func() {
				handler := utils.NHCByNodeMapperFunc(k8sClient, controllerruntime.Log)
				updatedNode := v1.Node{
					ObjectMeta: controllerruntime.ObjectMeta{Name: "healthy-worker-node-1"},
				}
				requests := handler(context.TODO(), &updatedNode)
				Expect(len(requests)).To(Equal(2))
				Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: underTest1.GetName()}}))
				Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: underTest2.GetName()}}))
			})
		})
		When("a node changes status and there are no NHC objects", func() {
			BeforeEach(func() {
				objects = newNodes(3, 10, false, true)
			})

			It("doesn't create reconcile requests", func() {
				handler := utils.NHCByNodeMapperFunc(k8sClient, controllerruntime.Log)
				updatedNode := v1.Node{
					ObjectMeta: controllerruntime.ObjectMeta{Name: "healthy-worker-node-1"},
				}
				requests := handler(context.TODO(), &updatedNode)
				Expect(requests).To(BeEmpty())
			})
		})
	})

	Describe("Node updates", func() {
		var oldConditions []v1.NodeCondition
		var newConditions []v1.NodeCondition
		var oldLabels map[string]string
		var newLabels map[string]string

		Context("conditions", func() {
			When("no Ready condition exists on new node", func() {
				BeforeEach(func() {
					newConditions = []v1.NodeCondition{
						{
							Type:   v1.NodeDiskPressure,
							Status: v1.ConditionTrue,
						},
					}
				})
				It("should not request reconcile", func() {
					Expect(conditionsNeedReconcile(oldConditions, newConditions)).To(BeFalse())
				})
			})

			When("condition types and statuses equal", func() {
				BeforeEach(func() {
					oldConditions = []v1.NodeCondition{
						{
							Type:   v1.NodeDiskPressure,
							Status: v1.ConditionTrue,
						},
						{
							Type:   v1.NodeReady,
							Status: v1.ConditionTrue,
						},
					}
					newConditions = []v1.NodeCondition{
						{
							Type:   v1.NodeReady,
							Status: v1.ConditionTrue,
						},
						{
							Type:   v1.NodeDiskPressure,
							Status: v1.ConditionTrue,
						},
					}
				})
				It("should not request reconcile", func() {
					Expect(conditionsNeedReconcile(oldConditions, newConditions)).To(BeFalse())
				})
			})

			When("condition type changed", func() {
				BeforeEach(func() {
					oldConditions = []v1.NodeCondition{
						{
							Type:   v1.NodeDiskPressure,
							Status: v1.ConditionTrue,
						},
					}
					newConditions = []v1.NodeCondition{
						{
							Type:   v1.NodeReady,
							Status: v1.ConditionTrue,
						},
					}
				})
				It("should request reconcile", func() {
					Expect(conditionsNeedReconcile(oldConditions, newConditions)).To(BeTrue())
				})
			})

			When("condition status changed", func() {
				BeforeEach(func() {
					oldConditions = []v1.NodeCondition{
						{
							Type:   v1.NodeReady,
							Status: v1.ConditionTrue,
						},
					}
					newConditions = []v1.NodeCondition{
						{
							Type:   v1.NodeReady,
							Status: v1.ConditionFalse,
						},
					}
				})
				It("should request reconcile", func() {
					Expect(conditionsNeedReconcile(oldConditions, newConditions)).To(BeTrue())
				})
			})

			When("condition was added", func() {
				BeforeEach(func() {
					oldConditions = append(newConditions,
						v1.NodeCondition{
							Type:   v1.NodeReady,
							Status: v1.ConditionTrue,
						},
					)
					newConditions = []v1.NodeCondition{
						{
							Type:   v1.NodeReady,
							Status: v1.ConditionTrue,
						},
						{
							Type:   v1.NodeDiskPressure,
							Status: v1.ConditionFalse,
						},
					}
				})
				It("should request reconcile", func() {
					Expect(conditionsNeedReconcile(oldConditions, newConditions)).To(BeTrue())
				})
			})

			When("condition was removed", func() {
				BeforeEach(func() {
					oldConditions = append(newConditions,
						v1.NodeCondition{
							Type:   v1.NodeReady,
							Status: v1.ConditionTrue,
						},
						v1.NodeCondition{
							Type:   v1.NodeDiskPressure,
							Status: v1.ConditionTrue,
						},
					)

					newConditions = append(newConditions, v1.NodeCondition{
						Type:   v1.NodeReady,
						Status: v1.ConditionTrue,
					})
				})
				It("should request reconcile", func() {
					Expect(conditionsNeedReconcile(oldConditions, newConditions)).To(BeTrue())
				})
			})
		})

		Context("labels", func() {

			var (
				underTest *v1alpha1.NodeHealthCheck
				objects   []client.Object
				logger    = controllerruntime.Log.WithName("test")
			)

			BeforeEach(func() {
				underTest = newNodeHealthCheck()
				objects = []client.Object{underTest}
			})

			JustBeforeEach(func() {
				createObjects(objects...)
				// give the reconciler some time
				time.Sleep(2 * time.Second)
			})

			AfterEach(func() {
				// delete all created objects
				deleteObjects(objects...)
			})

			When("label ExcludeFromRemediation is added", func() {
				BeforeEach(func() {
					oldLabels = map[string]string{}
					newLabels = map[string]string{
						commonLabels.ExcludeFromRemediation: "true",
					}
				})
				It("should request reconcile", func() {
					Expect(labelsNeedReconcile(k8sClient, oldLabels, newLabels, logger)).To(BeTrue())
				})
			})

			When("label ExcludeFromRemediation is removed", func() {
				BeforeEach(func() {
					oldLabels = map[string]string{
						commonLabels.ExcludeFromRemediation: "true",
					}
					newLabels = map[string]string{}
				})
				It("should request reconcile", func() {
					Expect(labelsNeedReconcile(k8sClient, oldLabels, newLabels, logger)).To(BeTrue())
				})
			})

			Context("A label different than ExcludeFromRemediation is added", func() {
				When("the label is used in a NHC selector", func() {
					BeforeEach(func() {
						oldLabels = map[string]string{}
						newLabels = map[string]string{
							"some-random-label": "true",
						}
						setSelectorOnNHC(objects, metav1.LabelSelector{
							MatchLabels: map[string]string{"some-random-label": "true"},
						})
					})
					It("should request reconcile", func() {
						Expect(labelsNeedReconcile(k8sClient, oldLabels, newLabels, logger)).To(BeTrue())
					})
				})
				When("the label is not used in a NHC selector", func() {
					BeforeEach(func() {
						oldLabels = map[string]string{}
						newLabels = map[string]string{
							"some-random-label": "true",
						}
						setSelectorOnNHC(objects, metav1.LabelSelector{
							MatchLabels: map[string]string{"wrong-label": "true"},
						})
					})
					It("should not request reconcile", func() {
						Expect(labelsNeedReconcile(k8sClient, oldLabels, newLabels, logger)).To(BeFalse())
					})
				})
				When("the NHC selector is empty", func() {
					BeforeEach(func() {
						oldLabels = map[string]string{}
						newLabels = map[string]string{
							"some-random-label": "true",
						}
					})
					It("should not request reconcile", func() {
						Expect(labelsNeedReconcile(k8sClient, oldLabels, newLabels, logger)).To(BeFalse())
					})
				})
			})

			Context("A label different than ExcludeFromRemediation is removed", func() {
				When("the label is used in a NHC selector", func() {
					BeforeEach(func() {
						oldLabels = map[string]string{
							"some-random-label": "true",
						}
						newLabels = map[string]string{}
						setSelectorOnNHC(objects, metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "some-random-label", Operator: metav1.LabelSelectorOpExists},
							},
						})
					})
					It("should request reconcile", func() {
						Expect(labelsNeedReconcile(k8sClient, oldLabels, newLabels, logger)).To(BeTrue())
					})
				})
				When("the label is not used in a NHC selector", func() {
					BeforeEach(func() {
						oldLabels = map[string]string{
							"some-random-label": "true",
						}
						newLabels = map[string]string{}
						setSelectorOnNHC(objects, metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "wrong-label", Operator: metav1.LabelSelectorOpExists},
							},
						})
					})
					It("should not request reconcile", func() {
						Expect(labelsNeedReconcile(k8sClient, oldLabels, newLabels, logger)).To(BeFalse())
					})
				})
			})

			Context("A label different than ExcludeFromRemediation is updated", func() {
				When("the label is used in a NHC selector", func() {
					BeforeEach(func() {
						oldLabels = map[string]string{
							"some-random-label": "true",
						}
						newLabels = map[string]string{
							"some-random-label": "false",
						}
						setSelectorOnNHC(objects, metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "some-random-label", Operator: metav1.LabelSelectorOpExists},
							},
						})
					})
					It("should request reconcile", func() {
						Expect(labelsNeedReconcile(k8sClient, oldLabels, newLabels, logger)).To(BeTrue())
					})
				})
				When("the label is not used in a NHC selector", func() {
					BeforeEach(func() {
						oldLabels = map[string]string{
							"some-random-label": "true",
						}
						newLabels = map[string]string{
							"some-random-label": "false",
						}
						setSelectorOnNHC(objects, metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "wrong-label", Operator: metav1.LabelSelectorOpExists},
							},
						})
					})
					It("should not request reconcile", func() {
						Expect(labelsNeedReconcile(k8sClient, oldLabels, newLabels, logger)).To(BeFalse())
					})
				})
			})

			Context("A label different than ExcludeFromRemediation is unmodified", func() {
				When("the label is used in a NHC selector", func() {
					BeforeEach(func() {
						oldLabels = map[string]string{
							"some-random-label": "true",
						}
						newLabels = map[string]string{
							"some-random-label": "true",
						}
						setSelectorOnNHC(objects, metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "some-random-label", Operator: metav1.LabelSelectorOpExists},
							},
						})
					})
					It("should not request reconcile", func() {
						Expect(labelsNeedReconcile(k8sClient, oldLabels, newLabels, logger)).To(BeFalse())
					})
				})
				When("the label is not used in a NHC selector", func() {
					BeforeEach(func() {
						oldLabels = map[string]string{
							"some-random-label": "true",
						}
						newLabels = map[string]string{
							"some-random-label": "true",
						}
						setSelectorOnNHC(objects, metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "wrong-label", Operator: metav1.LabelSelectorOpExists},
							},
						})
					})
					It("should not request reconcile", func() {
						Expect(labelsNeedReconcile(k8sClient, oldLabels, newLabels, logger)).To(BeFalse())
					})
				})
			})
		})
	})

	Context("Unhealthy condition checks", func() {

		var (
			r = &NodeHealthCheckReconciler{
				Recorder: record.NewFakeRecorder(1),
			}
			nhc            = newNodeHealthCheck()
			nodeConditions []v1.NodeCondition
			node           *v1.Node

			condType1         = v1.NodeConditionType("type1")
			condType2         = v1.NodeConditionType("type2")
			condStatusMatch   = v1.ConditionTrue
			condStatusNoMatch = v1.ConditionUnknown

			now                      = time.Now()
			unhealthyDuration        = metav1.Duration{Duration: 10 * time.Second}
			expireIn                 = 2 * time.Second
			expiredTransitionTime    = metav1.Time{Time: now.Add(-unhealthyDuration.Duration).Add(-time.Second)}
			notExpiredTransitionTime = metav1.Time{Time: now.Add(-unhealthyDuration.Duration).Add(expireIn)}

			// this is always added in tested code
			expireBuffer = time.Second
		)

		BeforeEach(func() {
			fakeTime = &now
			DeferCleanup(func() {
				fakeTime = nil
			})

			nhc.Spec.UnhealthyConditions = []v1alpha1.UnhealthyCondition{
				{
					Type:     condType1,
					Status:   condStatusMatch,
					Duration: unhealthyDuration,
				},
				{
					Type:     condType2,
					Status:   condStatusMatch,
					Duration: unhealthyDuration,
				},
			}
		})

		JustBeforeEach(func() {
			node = &v1.Node{}
			node.Name = "test-node"
			node.Status.Conditions = nodeConditions
		})

		When("no condition matches", func() {
			BeforeEach(func() {
				nodeConditions = []v1.NodeCondition{
					{
						Type:               condType1,
						Status:             condStatusNoMatch,
						LastTransitionTime: notExpiredTransitionTime,
					},
					{
						Type:               condType2,
						Status:             condStatusNoMatch,
						LastTransitionTime: expiredTransitionTime,
					},
				}
			})
			It("should not report match, should not report expiry", func() {
				match, expire := r.matchesUnhealthyConditions(nhc, node)
				Expect(match).To(BeFalse(), "expected healthy")
				Expect(expire).To(BeNil(), "expected expire to not be set")
			})
		})

		When("a single condition matches but didn't expire", func() {
			BeforeEach(func() {
				nodeConditions = []v1.NodeCondition{
					{
						Type:               condType1,
						Status:             condStatusMatch,
						LastTransitionTime: notExpiredTransitionTime,
					},
				}
			})
			It("should not report match, should report expiry", func() {
				match, expire := r.matchesUnhealthyConditions(nhc, node)
				Expect(match).To(BeFalse(), "expected healthy")
				Expect(expire).ToNot(BeNil(), "expected expire to be set")
				Expect(*expire).To(Equal(expireIn+expireBuffer), "expected expire in 1 second")
			})
		})

		When("first condition matches but didn't expire, second condition matches and expired", func() {
			BeforeEach(func() {
				nodeConditions = []v1.NodeCondition{
					{
						Type:               condType1,
						Status:             condStatusMatch,
						LastTransitionTime: notExpiredTransitionTime,
					},
					{
						Type:               condType2,
						Status:             condStatusMatch,
						LastTransitionTime: expiredTransitionTime,
					},
				}
			})
			It("should report match, should not report expiry", func() {
				match, expire := r.matchesUnhealthyConditions(nhc, node)
				Expect(match).To(BeTrue(), "expected not healthy")
				Expect(expire).To(BeNil(), "expected expire to not be set")
			})
		})

		When("first condition doesn't match, second condition matches and didn't expire", func() {
			BeforeEach(func() {
				nodeConditions = []v1.NodeCondition{
					{
						Type:               condType1,
						Status:             condStatusNoMatch,
						LastTransitionTime: notExpiredTransitionTime,
					},
					{
						Type:               condType2,
						Status:             condStatusMatch,
						LastTransitionTime: notExpiredTransitionTime,
					},
				}
			})
			It("should not report match, should not report expiry", func() {
				match, expire := r.matchesUnhealthyConditions(nhc, node)
				Expect(match).To(BeFalse(), "expected healthy")
				Expect(expire).ToNot(BeNil(), "expected expire to be set")
				Expect(*expire).To(Equal(expireIn+expireBuffer), "expected expire in 1 second")
			})
		})

	})

	Context("getMinHealthy", func() {
		DescribeTable("should return absolute minHealthy",
			func(minHealthy, maxUnhealthy *intstr.IntOrString, totalNodes, expectedAbsoluteMinHealthy int, expectedErr error) {
				nhc := &v1alpha1.NodeHealthCheck{
					Spec: v1alpha1.NodeHealthCheckSpec{
						MinHealthy:   minHealthy,
						MaxUnhealthy: maxUnhealthy,
					},
				}
				absoluteMinHealthy, err := getMinHealthy(nhc, totalNodes)
				if expectedErr != nil {
					Expect(err).To(HaveOccurred())
					Expect(err).To(MatchError(expectedErr))
				} else {
					Expect(err).ToNot(HaveOccurred())
					Expect(absoluteMinHealthy).To(Equal(expectedAbsoluteMinHealthy))
				}
			},
			Entry("minHealthy - positive value",
				ptrToIntStr("8"),
				nil,
				10,
				8,
				nil,
			),
			Entry("minHealthy - positive percentage value",
				ptrToIntStr("80%"),
				nil,
				10,
				8,
				nil,
			),
			Entry("minHealthy - positive percentage value with round-up",
				ptrToIntStr("75%"),
				nil,
				10,
				8,
				nil,
			),
			Entry("minHealthy - 0%",
				ptrToIntStr("0%"),
				nil,
				10,
				0,
				nil,
			),
			Entry("minHealthy - 100%",
				ptrToIntStr("100%"),
				nil,
				10,
				10,
				nil,
			),
			Entry("minHealthy - negative value",
				ptrToIntStr("-3"),
				nil,
				10,
				-3,
				fmt.Errorf("minHealthy must not be negative: -3"),
			),
			Entry("minHealthy - negative percentage value",
				ptrToIntStr("-30%"),
				nil,
				10,
				-3,
				fmt.Errorf("minHealthy is negative: -3"),
			),
			Entry("maxUnhealthy - positive value",
				nil,
				ptrToIntStr("8"),
				10,
				2,
				nil,
			),
			Entry("maxUnhealthy - positive percentage value",
				nil,
				ptrToIntStr("80%"),
				10,
				2,
				nil,
			),
			Entry("maxUnhealthy - positive percentage value with round-up",
				nil,
				ptrToIntStr("75%"),
				10,
				2,
				nil,
			),
			Entry("maxUnhealthy - 0%",
				nil,
				ptrToIntStr("0%"),
				10,
				10,
				nil,
			),
			Entry("maxUnhealthy - 100%",
				nil,
				ptrToIntStr("100%"),
				10,
				0,
				nil,
			),
			Entry("maxUnhealthy - negative value",
				nil,
				ptrToIntStr("-3"),
				10,
				0,
				fmt.Errorf("maxUnhealthy must not be negative: -3"),
			),
			Entry("maxUnhealthy - negative percentage value",
				nil,
				ptrToIntStr("-30%"),
				10,
				-3,
				fmt.Errorf("maxUnhealthy is negative: -3"),
			),
			Entry("maxUnhealthy > number of selected nodes",
				nil,
				ptrToIntStr("12"),
				10,
				12,
				fmt.Errorf("maxUnhealthy is greater than the number of selected nodes: 12"),
			),
			Entry("minHealthy and maxUnhealthy at the same time",
				ptrToIntStr("2"),
				ptrToIntStr("2"),
				10,
				-3,
				fmt.Errorf("minHealthy and maxUnhealthy cannot be specified at the same time"),
			),
			Entry("nor minHealthy nor maxUnhealthy",
				nil,
				nil,
				10,
				0,
				fmt.Errorf("one of minHealthy and maxUnhealthy should be specified"),
			),
		)
	})
})

func mockLeaseParams(mockRequeueDurationIfLeaseTaken, mockDefaultLeaseDuration, mockLeaseBuffer time.Duration) {
	orgRequeueIfLeaseTaken := resources.RequeueIfLeaseTaken
	orgDefaultLeaseDuration := utils.DefaultRemediationDuration
	orgLeaseBuffer := resources.LeaseBuffer
	// Set up mock values so tests can run in a reasonable time
	resources.RequeueIfLeaseTaken = mockRequeueDurationIfLeaseTaken
	utils.DefaultRemediationDuration = mockDefaultLeaseDuration
	resources.LeaseBuffer = mockLeaseBuffer

	ns := &v1.Namespace{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: leaseNs}, ns); err != nil {
		if errors.IsNotFound(err) {
			ns.Name = leaseNs
			err := k8sClient.Create(context.Background(), ns)
			Expect(err).ToNot(HaveOccurred())
		}

	}

	DeferCleanup(func() {
		resources.RequeueIfLeaseTaken = orgRequeueIfLeaseTaken
		utils.DefaultRemediationDuration = orgDefaultLeaseDuration
		resources.LeaseBuffer = orgLeaseBuffer
	})
}

func findRemediationCRForNHC(nodeName string, nhc *v1alpha1.NodeHealthCheck) *unstructured.Unstructured {
	var templateRef v1.ObjectReference
	if nhc.Spec.RemediationTemplate != nil {
		templateRef = *nhc.Spec.RemediationTemplate
	} else {
		templateRef = nhc.Spec.EscalatingRemediations[0].RemediationTemplate
	}
	return findRemediationCRForTemplate(nodeName, nhc, templateRef)
}

func findRemediationCRForNHCSecondRemediation(nodeName string, nhc *v1alpha1.NodeHealthCheck) *unstructured.Unstructured {
	templateRef := nhc.Spec.EscalatingRemediations[1].RemediationTemplate
	return findRemediationCRForTemplate(nodeName, nhc, templateRef)
}

func findRemediationCRForTemplate(nodeName string, nhc *v1alpha1.NodeHealthCheck, templateRef v1.ObjectReference) *unstructured.Unstructured {
	baseCr := newBaseCR(nhc, templateRef)
	crList := &unstructured.UnstructuredList{Object: baseCr.Object}
	if err := k8sClient.List(ctx, crList); err == nil {
		for _, cr := range crList.Items {
			ann := cr.GetAnnotations()
			if ann != nil {
				if ann[annotations.TemplateNameAnnotation] == templateRef.Name &&
					ann[commonannotations.NodeNameAnnotation] == nodeName {
					return &cr
				}
			}
		}
	}
	return nil
}

func getRemediationsCount(nhc *v1alpha1.NodeHealthCheck) int {
	if nhc.Spec.RemediationTemplate != nil {
		return getRemediationsCountForTemplate(nhc, *nhc.Spec.RemediationTemplate)
	}

	count := 0
	for _, escalationRemediation := range nhc.Spec.EscalatingRemediations {
		count += getRemediationsCountForTemplate(nhc, escalationRemediation.RemediationTemplate)
	}

	return count
}

func getRemediationsCountForTemplate(nhc *v1alpha1.NodeHealthCheck, templateRef v1.ObjectReference) int {
	baseCr := newBaseCR(nhc, templateRef)
	crList := &unstructured.UnstructuredList{Object: baseCr.Object}
	if err := k8sClient.List(ctx, crList); err == nil {
		return len(crList.Items)
	}
	return 0
}

func newBaseCR(nhc *v1alpha1.NodeHealthCheck, templateRef v1.ObjectReference) unstructured.Unstructured {
	crKind := templateRef.Kind[:len(templateRef.Kind)-len("template")]
	crs := newBaseCRs(nhc)
	for _, cr := range crs {
		if cr.GetKind() == crKind {
			return cr
		}
	}
	// Can not happen...
	return unstructured.Unstructured{}
}

func newBaseCRs(nhc *v1alpha1.NodeHealthCheck) []unstructured.Unstructured {
	crs := make([]unstructured.Unstructured, 0)

	appendCr := func(gvk schema.GroupVersionKind) {
		cr := unstructured.Unstructured{}
		kind := gvk.Kind
		// Remove trailing template
		kind = kind[:len(kind)-len("template")]
		cr.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   gvk.Group,
			Version: gvk.Version,
			Kind:    kind,
		})
		crs = append(crs, cr)
	}

	if nhc.Spec.RemediationTemplate != nil {
		appendCr(nhc.Spec.RemediationTemplate.GroupVersionKind())
	}

	for _, rem := range nhc.Spec.EscalatingRemediations {
		appendCr(rem.RemediationTemplate.GroupVersionKind())
	}

	return crs
}

func newRemediationCRForNHC(nodeName string, nhc *v1alpha1.NodeHealthCheck) *unstructured.Unstructured {
	var templateRef v1.ObjectReference
	if nhc.Spec.RemediationTemplate != nil {
		templateRef = *nhc.Spec.RemediationTemplate
	} else {
		templateRef = nhc.Spec.EscalatingRemediations[0].RemediationTemplate
	}
	owner := metav1.OwnerReference{
		APIVersion: nhc.APIVersion,
		Kind:       nhc.Kind,
		Name:       nhc.Name,
		UID:        nhc.UID,
	}
	return newRemediationCR(nodeName, nodeName, templateRef, owner)
}

func newNodeHealthCheck() *v1alpha1.NodeHealthCheck {
	unhealthy := intstr.FromString("51%")
	return &v1alpha1.NodeHealthCheck{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NodeHealthCheck",
			APIVersion: "remediation.medik8s.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
			UID:  "1234",
		},
		Spec: v1alpha1.NodeHealthCheckSpec{
			Selector:   metav1.LabelSelector{},
			MinHealthy: &unhealthy,
			UnhealthyConditions: []v1alpha1.UnhealthyCondition{
				{
					Type:     v1.NodeReady,
					Status:   v1.ConditionFalse,
					Duration: metav1.Duration{Duration: unhealthyConditionDuration},
				},
				{
					Type:     v1.NodeReady,
					Status:   v1.ConditionUnknown,
					Duration: metav1.Duration{Duration: unhealthyConditionDuration},
				},
			},
			RemediationTemplate: infraMultipleRemediationTemplateRef.DeepCopy(),
		},
	}
}

func setSelectorOnNHC(objects []client.Object, selector metav1.LabelSelector) {
	for _, obj := range objects {
		switch obj.(type) {
		case *v1alpha1.NodeHealthCheck:
			obj.(*v1alpha1.NodeHealthCheck).Spec.Selector = selector
		}
	}
}

func newNodeHealthCheckWithStormRecovery(delay time.Duration, minHealthy int32) *v1alpha1.NodeHealthCheck {
	nhc := newNodeHealthCheck()
	minHealthyIntStr := intstr.FromInt32(minHealthy)
	nhc.Spec.MinHealthy = &minHealthyIntStr
	nhc.Spec.StormCooldownDuration = &metav1.Duration{Duration: delay}
	return nhc
}

func newNodes(unhealthy int, healthy int, isControlPlane bool, unhealthyNow bool) []client.Object {
	o := make([]client.Object, 0, healthy+unhealthy)
	roleName := "-worker"
	if isControlPlane {
		roleName = "-control-plane"
	}
	for i := unhealthy; i > 0; i-- {
		node := newNode(fmt.Sprintf("unhealthy%s-node-%d", roleName, i), v1.NodeReady, v1.ConditionUnknown, isControlPlane, unhealthyNow)
		o = append(o, node)
	}
	for i := healthy; i > 0; i-- {
		o = append(o, newNode(fmt.Sprintf("healthy%s-node-%d", roleName, i), v1.NodeReady, v1.ConditionTrue, isControlPlane, unhealthyNow))
	}
	return o
}

func newNode(name string, t v1.NodeConditionType, s v1.ConditionStatus, isControlPlane bool, unhealthyNow bool) client.Object {
	labels := make(map[string]string, 1)
	if isControlPlane {
		labels[commonLabels.ControlPlaneRole] = ""
	} else {
		labels[commonLabels.WorkerRole] = ""
	}
	// let the node get unhealthy in a few seconds
	transitionTime := time.Now().Add(-(unhealthyConditionDuration - nodeUnhealthyIn + 2*time.Second))
	// unless requested otherwise
	if unhealthyNow {
		transitionTime = time.Now().Add(-(unhealthyConditionDuration + 2*time.Second))
	}
	return &v1.Node{
		TypeMeta: metav1.TypeMeta{Kind: "Node"},
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{
				{
					Type:               t,
					Status:             s,
					LastTransitionTime: metav1.Time{Time: transitionTime},
				},
			},
		},
	}
}

// updateStatusCondition sets the Status.Condition Succeeded on an unstructured object
func updateStatusCondition(o *unstructured.Unstructured) *unstructured.Unstructured {
	// add .Status to the object if it doesn't exist
	if _, found, _ := unstructured.NestedFieldNoCopy(o.Object, "status"); !found {
		o.Object["status"] = make(map[string]interface{})
	}

	// add .Status.Conditions if it doesn't exist
	if _, found, _ := unstructured.NestedFieldNoCopy(o.Object, "status", "conditions"); !found {
		o.Object["status"].(map[string]interface{})["conditions"] = []interface{}{}
	}

	// add a condition to .Status.Conditions if it doesn't exist
	conditions := make([]interface{}, 0)
	conditions = append(conditions, map[string]interface{}{
		"type":               "Succeeded",
		"status":             "True",
		"lastTransitionTime": time.Now().Format(time.RFC3339),
	})

	o.Object["status"].(map[string]interface{})["conditions"] = conditions
	return o
}

func clearEvents() {
	for {
		select {
		case _ = <-fakeRecorder.Events:

		default:
			return
		}
	}
}

func verifyEvent(eventType, reason, message string) {
	EventuallyWithOffset(1, func() bool {
		return isEventOccurred(eventType, reason, message)
	}, 5*time.Second, 250*time.Millisecond).Should(BeTrue())
}

func verifyNoEvent(eventType, reason, message string) {
	ConsistentlyWithOffset(1, func() bool {
		return isEventOccurred(eventType, reason, message)
	}, 5*time.Second, 250*time.Millisecond).Should(BeFalse())
}

func isEventOccurred(eventType string, reason string, message string) bool {
	expected := fmt.Sprintf("%s %s [remediation] %s", eventType, reason, message)
	isEventMatch := false

	unMatchedEvents := make(chan string, len(fakeRecorder.Events))
	By(fmt.Sprintf("verifying that the event was: %s", expected))
	isDone := false
	for {
		select {
		case event := <-fakeRecorder.Events:
			if isEventMatch = event == expected; isEventMatch {
				isDone = true
			} else {
				unMatchedEvents <- event
			}
		default:
			isDone = true
		}
		if isDone {
			break
		}
	}

	close(unMatchedEvents)
	for unMatchedEvent := range unMatchedEvents {
		fakeRecorder.Events <- unMatchedEvent
	}
	return isEventMatch
}

func ptrToIntStr(val string) *intstr.IntOrString {
	v := intstr.Parse(val)
	return &v
}

func mockNodeGettingHealthy(unhealthyNodeName string) {
	setNodeReadyCondition(unhealthyNodeName, v1.ConditionTrue)
}

func mockNodeGettingUnhealthy(healthyNodeName string) {
	setNodeReadyCondition(healthyNodeName, v1.ConditionFalse)
}

func setNodeReadyCondition(unhealthyNodeName string, conditionStatus v1.ConditionStatus) {
	node := &v1.Node{}
	err := k8sClient.Get(context.Background(), client.ObjectKey{Name: unhealthyNodeName}, node)
	Expect(err).ToNot(HaveOccurred())
	for i, c := range node.Status.Conditions {
		if c.Type == v1.NodeReady {
			node.Status.Conditions[i].Status = conditionStatus
		}
	}
	err = k8sClient.Status().Update(context.Background(), node)
	Expect(err).ToNot(HaveOccurred())
}
