package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	commonlabels "github.com/medik8s/common/pkg/labels"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	coordv1 "k8s.io/api/coordination/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/api/machine/v1beta1"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/mhc"
	"github.com/medik8s/node-healthcheck-operator/e2e/utils"
)

const (
	nhcName = "test-nhc"
)

var _ = Describe("e2e - NHC", Label("NHC"), func() {
	var nhc *v1alpha1.NodeHealthCheck

	BeforeEach(func() {

		// prepare "classic" NHC with single remediation
		minHealthy := intstr.FromInt(1)
		nhc = &v1alpha1.NodeHealthCheck{
			ObjectMeta: metav1.ObjectMeta{
				Name: nhcName,
			},
			Spec: v1alpha1.NodeHealthCheckSpec{
				MinHealthy: &minHealthy,
				Selector: metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      commonlabels.ControlPlaneRole,
							Operator: metav1.LabelSelectorOpDoesNotExist,
						},
						{
							Key:      commonlabels.MasterRole,
							Operator: metav1.LabelSelectorOpDoesNotExist,
						},
					},
				},
				RemediationTemplate: &v1.ObjectReference{
					Kind:       "SelfNodeRemediationTemplate",
					APIVersion: "self-node-remediation.medik8s.io/v1alpha1",
					Name:       snrTemplateName,
					Namespace:  operatorNsName,
				},
				UnhealthyConditions: []v1alpha1.UnhealthyCondition{
					{
						Type:     "Ready",
						Status:   "False",
						Duration: metav1.Duration{Duration: unhealthyConditionDuration},
					},
					{
						Type:     "Ready",
						Status:   "Unknown",
						Duration: metav1.Duration{Duration: unhealthyConditionDuration},
					},
				},
			},
		}
	})

	JustBeforeEach(func() {
		// create NHC
		Eventually(func() error {
			return k8sClient.Create(context.Background(), nhc)
		}, "1m", "5s").Should(Succeed())

		DeferCleanup(func() {
			Eventually(func() error {
				err := k8sClient.Delete(context.Background(), nhc)
				if errors.IsNotFound(err) {
					return nil
				}
				return err
			}, "1m", "5s").Should(Succeed())
		})
	})

	Context("With custom MHC", labelOcpOnly, func() {

		var mhc *v1beta1.MachineHealthCheck

		BeforeEach(func() {
			By("creating custom MHC")
			mhc = &v1beta1.MachineHealthCheck{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-mhc",
					Namespace: "default",
				},
				Spec: v1beta1.MachineHealthCheckSpec{
					Selector: metav1.LabelSelector{},
					UnhealthyConditions: []v1beta1.UnhealthyCondition{
						{
							Type:    "Dummy",
							Status:  "Dummy",
							Timeout: metav1.Duration{Duration: 1 * time.Minute},
						},
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), mhc)).To(Succeed(), "failed to create MHC")
		})

		AfterEach(func() {
			// don't put this in to deferred cleanup, NHC will be deleted by outer AfterEach first
			By("deleting custom MHC")
			Expect(k8sClient.Delete(context.Background(), mhc)).To(Succeed(), "failed to delete MHC")
			By("waiting for NHC to be re-enabled")
			Eventually(func(g Gomega) {
				nhc = getNodeHealthCheck()
				g.Expect(meta.IsStatusConditionTrue(nhc.Status.Conditions, v1alpha1.ConditionTypeDisabled)).To(BeFalse(), "disabled condition should be false")
				g.Expect(nhc.Status.Phase).To(Equal(v1alpha1.PhaseEnabled), "phase should be Enabled")
			}, 3*time.Second, 1*time.Second).Should(Succeed(), "NHC should be enabled after MHC deletion")
		})

		It("should report disabled NHC", func() {
			By("waiting for NHC to be disabled")
			Eventually(func(g Gomega) {
				nhc = getNodeHealthCheck()
				g.Expect(meta.IsStatusConditionTrue(nhc.Status.Conditions, v1alpha1.ConditionTypeDisabled)).To(BeTrue(), "disabled condition should be true")
				g.Expect(nhc.Status.Phase).To(Equal(v1alpha1.PhaseDisabled), "phase should be Disabled")
			}, 3*time.Second, 1*time.Second).Should(Succeed(), "NHC should be disabled because of custom MHC")
		})
	}) // end of custom MHC context

	Context("Remediation", func() {
		var nodeUnderTest *v1.Node

		Context("Worker node with unhealthy condition", func() {
			var node2 *v1.Node
			var node3 *v1.Node

			BeforeEach(func() {
				if nodeUnderTest == nil {
					workers := utils.GetWorkerNodes(k8sClient)
					nodeUnderTest = &workers[0]
					node2 = &workers[1]
					node3 = &workers[2]
				}
			})

			Context("with escalating remediation config", labelOcpOnly, func() {

				var (
					escTimeout        metav1.Duration
					nodeUnhealthyTime time.Time
					//lease params
					leaseNs   = "medik8s-leases"
					leaseName string
					lease     *coordv1.Lease
				)

				BeforeEach(func() {
					escTimeout = metav1.Duration{Duration: 1 * time.Minute}
					leaseName = fmt.Sprintf("%s-%s", "node", nodeUnderTest.Name)
					lease = &coordv1.Lease{}

					// modify nhc to use escalating remediations
					nhc.Spec.RemediationTemplate = nil
					nhc.Spec.EscalatingRemediations = []v1alpha1.EscalatingRemediation{
						{
							RemediationTemplate: v1.ObjectReference{
								Kind:       dummyRemediationTemplateGVK.Kind,
								APIVersion: dummyRemediationTemplateGVK.GroupVersion().String(),
								Name:       dummyTemplateName,
								Namespace:  testNsName,
							},
							Order:   0,
							Timeout: escTimeout,
						},
						{
							RemediationTemplate: v1.ObjectReference{
								Kind:       "SelfNodeRemediationTemplate",
								APIVersion: "self-node-remediation.medik8s.io/v1alpha1",
								Name:       snrTemplateName,
								Namespace:  operatorNsName,
							},
							Order:   5,
							Timeout: escTimeout,
						},
					}
				})

				It("Remediates a host", func() {
					By("making node unhealthy")
					nodeUnhealthyTime = utils.MakeNodeUnready(k8sClient, clientSet, nodeUnderTest, testNsName, log)

					By("ensuring 1st remediation CR exists")
					waitTime := nodeUnhealthyTime.Add(unhealthyConditionDuration + 3*time.Second).Sub(time.Now())
					Eventually(
						fetchRemediationResourceByName(nodeUnderTest.Name,
							testNsName,
							schema.GroupVersionResource{
								Group:    dummyRemediationGVK.Group,
								Version:  dummyRemediationGVK.Version,
								Resource: strings.ToLower(dummyRemediationGVK.Kind) + "s",
							},
						), waitTime, 1*time.Second).
						Should(Succeed())

					By("ensuring lease exist")
					err := k8sClient.Get(context.Background(), ctrl.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)
					Expect(err).ToNot(HaveOccurred())

					// SNR is very fast on kind, so use a short poll intervals, otherwise node might already be healthy again

					By("waiting and checking 1st remediation timed out")
					// sleep for most of the configured timeout
					time.Sleep(escTimeout.Duration - 2*time.Second)
					var nhc *v1alpha1.NodeHealthCheck
					Eventually(func(g Gomega) *metav1.Time {
						nhc = getNodeHealthCheck()
						g.Expect(nhc.Status.UnhealthyNodes).To(HaveLen(1), "expected unhealthy node!")
						log.Info("checking timeout", "node", nhc.Status.UnhealthyNodes[0].Name, "remediation kind", nhc.Status.UnhealthyNodes[0].Remediations[0].Resource.Kind, "timedOut", nhc.Status.UnhealthyNodes[0].Remediations[0].TimedOut)
						return nhc.Status.UnhealthyNodes[0].Remediations[0].TimedOut
					}, "5s", "500ms").ShouldNot(BeNil(), "1st remediation should have timed out")

					By("ensuring 2nd remediation CR exists")
					Eventually(
						fetchRemediationResourceByName(nodeUnderTest.Name, operatorNsName, remediationGVR), "2s", "500ms").
						Should(Succeed())

					By("ensuring status is set")
					Eventually(func(g Gomega) {
						nhc = getNodeHealthCheck()
						g.Expect(nhc.Status.InFlightRemediations).To(HaveLen(1))
						g.Expect(nhc.Status.UnhealthyNodes).To(HaveLen(1))
						g.Expect(nhc.Status.UnhealthyNodes[0].Remediations).To(HaveLen(2))
						g.Expect(nhc.Status.Phase).To(Equal(v1alpha1.PhaseRemediating))
					}, "2s", "500ms").Should(Succeed())

					By("waiting for healthy node")
					utils.WaitForNodeHealthyCondition(k8sClient, nodeUnderTest, v1.ConditionTrue)

					By("ensuring lease is removed")
					Eventually(
						func() bool {
							err := k8sClient.Get(context.Background(), ctrl.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)
							return errors.IsNotFound(err)
						}, 5*time.Minute, 5*time.Second).
						Should(BeTrue())

					By("waiting for remediation CR deletion, else cleanup fails")
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(nhc), nhc)).To(Succeed())
						g.Expect(nhc.Status.UnhealthyNodes).To(HaveLen(0))
					}, "5m", "5s").Should(Succeed(), "CR not deleted")

				})
			}) // end of escalating remediation context

			Context("with classic remediation config", func() {

				var nodeUnhealthyTime time.Time

				BeforeEach(func() {
					nodeUnderTest = node2
				})

				It("Remediates a host and prevents config updates", func() {
					By("making node unhealthy")
					nodeUnhealthyTime = utils.MakeNodeUnready(k8sClient, clientSet, nodeUnderTest, testNsName, log)

					By("ensuring remediation CR exists")
					waitTime := nodeUnhealthyTime.Add(unhealthyConditionDuration + 3*time.Second).Sub(time.Now())
					Eventually(
						fetchRemediationResourceByName(nodeUnderTest.Name, operatorNsName, remediationGVR), waitTime, "500ms").
						Should(Succeed())

					By("ensuring status is set")
					Eventually(func(g Gomega) {
						nhc = getNodeHealthCheck()
						g.Expect(nhc.Status.InFlightRemediations).To(HaveLen(1))
						g.Expect(nhc.Status.UnhealthyNodes).To(HaveLen(1))
						g.Expect(nhc.Status.UnhealthyNodes[0].Remediations).To(HaveLen(1))
						g.Expect(nhc.Status.Phase).To(Equal(v1alpha1.PhaseRemediating))
					}, "10s", "500ms").Should(Succeed())

					// let's do some NHC validation tests here
					By("ensuring negative minHealthy update fails")
					nhc = getNodeHealthCheck()
					negValue := intstr.FromInt(-1)
					nhc.Spec.MinHealthy = &negValue
					Expect(k8sClient.Update(context.Background(), nhc)).To(MatchError(ContainSubstring("MinHealthy")), "negative minHealthy update should be prevented")

					By("ensuring selector update fails")
					nhc = getNodeHealthCheck()
					nhc.Spec.Selector = metav1.LabelSelector{
						MatchLabels: map[string]string{
							"foo": "bar",
						},
					}
					Expect(k8sClient.Update(context.Background(), nhc)).To(MatchError(ContainSubstring(v1alpha1.OngoingRemediationError)), "selector update should be prevented")

					By("ensuring config deletion fails")
					nhc = getNodeHealthCheck()
					Expect(k8sClient.Delete(context.Background(), nhc)).To(MatchError(ContainSubstring(v1alpha1.OngoingRemediationError)), "deletion should be prevented")

					By("ensuring minHealthy update succeeds")
					nhc = getNodeHealthCheck()
					newValue := intstr.FromString("10%")
					nhc.Spec.MinHealthy = &newValue
					Expect(k8sClient.Update(context.Background(), nhc)).To(Succeed(), "minHealthy update should be allowed")

					By("waiting for healthy node condition")
					utils.WaitForNodeHealthyCondition(k8sClient, nodeUnderTest, v1.ConditionTrue)

					By("waiting for remediation CR deletion, else cleanup fails")
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(nhc), nhc)).To(Succeed())
						g.Expect(nhc.Status.UnhealthyNodes).To(HaveLen(0))
					}, "5m", "5s").Should(Succeed(), "CR not deleted")
				})
			}) // end of classic remediation context

			// Run this as last one, since the node won't be remediated!
			Context("with terminating node", labelOcpOnly, func() {

				BeforeEach(func() {
					nodeUnderTest = node3
					Expect(k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(nodeUnderTest), nodeUnderTest)).To(Succeed())
					conditions := nodeUnderTest.Status.Conditions
					conditions = append(conditions, v1.NodeCondition{
						Type:   mhc.NodeConditionTerminating,
						Status: "True",
					})
					nodeUnderTest.Status.Conditions = conditions
					Expect(k8sClient.Status().Update(context.Background(), nodeUnderTest)).To(Succeed())

					utils.MakeNodeUnready(k8sClient, clientSet, nodeUnderTest, testNsName, log)
				})

				AfterEach(func() {
					Expect(k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(nodeUnderTest), nodeUnderTest)).To(Succeed())
					conditions := nodeUnderTest.Status.Conditions
					for i, cond := range conditions {
						if cond.Type == mhc.NodeConditionTerminating {
							conditions = append(conditions[:i], conditions[i+1:]...)
							break
						}
					}
					nodeUnderTest.Status.Conditions = conditions
					Expect(k8sClient.Status().Update(context.Background(), nodeUnderTest)).To(Succeed())

					// now it should remediate, wait until the node is healthy for cleaning up!
					By("waiting for healthy node condition")
					utils.WaitForNodeHealthyCondition(k8sClient, nodeUnderTest, v1.ConditionTrue)

					By("waiting for remediation CR deletion, else cleanup fails")
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(nhc), nhc)).To(Succeed())
						g.Expect(nhc.Status.UnhealthyNodes).To(HaveLen(0))
					}, "5m", "5s").Should(Succeed(), "CR not deleted")
				})

				It("should not remediate", func() {
					Consistently(
						fetchRemediationResourceByName(nodeUnderTest.Name, operatorNsName, remediationGVR), unhealthyConditionDuration+60*time.Second, 10*time.Second).
						ShouldNot(Succeed())
				})
			}) // end of terminating node context

		}) // end of worker node context

		Context("Control plane node with unhealthy condition, with classic remediation", func() {
			BeforeEach(func() {
				// modify nhc to select control plane nodes
				nhc.Spec.Selector = metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      commonlabels.ControlPlaneRole,
							Operator: metav1.LabelSelectorOpExists,
						},
					},
				}
				nodeUnderTest = utils.GetControlPlaneNode(k8sClient)
			})

			It("Remediates the control plane node", func() {
				By("making node unhealthy")
				nodeUnhealthyTime := utils.MakeNodeUnready(k8sClient, clientSet, nodeUnderTest, testNsName, log)

				By("ensuring remediation CR exists")
				waitTime := nodeUnhealthyTime.Add(unhealthyConditionDuration + 3*time.Second).Sub(time.Now())
				// for control plane remediation we need to wait longer, because NHC might need to change leader / restart the pod
				waitTime += 6 * time.Minute
				Eventually(
					fetchRemediationResourceByName(nodeUnderTest.Name, operatorNsName, remediationGVR), waitTime, "1s").
					Should(Succeed())

				By("ensuring status is set")
				Eventually(func(g Gomega) {
					nhc = getNodeHealthCheck()
					g.Expect(nhc.Status.InFlightRemediations).To(HaveLen(1))
					g.Expect(nhc.Status.UnhealthyNodes).To(HaveLen(1))
					g.Expect(nhc.Status.UnhealthyNodes[0].Remediations).To(HaveLen(1))
					g.Expect(nhc.Status.Phase).To(Equal(v1alpha1.PhaseRemediating))
				}, "10s", "500ms").Should(Succeed())

				By("waiting for healthy node condition")
				utils.WaitForNodeHealthyCondition(k8sClient, nodeUnderTest, v1.ConditionTrue)

				By("waiting for remediation CR deletion, else cleanup fails")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(nhc), nhc)).To(Succeed())
					g.Expect(nhc.Status.UnhealthyNodes).To(HaveLen(0))
				}, "5m", "5s").Should(Succeed(), "CR not deleted")
			})
		}) // end of control plane node context

	}) // end of remediation context
})

func getNodeHealthCheck() *v1alpha1.NodeHealthCheck {
	nhc := &v1alpha1.NodeHealthCheck{ObjectMeta: metav1.ObjectMeta{Name: nhcName}}
	ExpectWithOffset(1, k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(nhc), nhc)).To(Succeed(), "failed to get NHC")
	return nhc
}
