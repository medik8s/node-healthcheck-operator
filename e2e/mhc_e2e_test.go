package e2e

import (
	"context"
	"fmt"
	"time"

	commonlabels "github.com/medik8s/common/pkg/labels"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	coordv1 "k8s.io/api/coordination/v1"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/api/machine/v1beta1"

	controllerutils "github.com/medik8s/node-healthcheck-operator/controllers/utils"
	"github.com/medik8s/node-healthcheck-operator/e2e/utils"
)

const (
	mhcName      = "test-mhc"
	mhcNamespace = "openshift-machine-api"
)

var _ = Describe("e2e - MHC", Label("MHC", labelOcpOnlyValue), func() {
	var nodeUnderTest *v1.Node
	var mhc *v1beta1.MachineHealthCheck
	var workers *v1.NodeList
	var leaseName string

	BeforeEach(func() {

		// prepare MHC
		mhc = &v1beta1.MachineHealthCheck{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mhcName,
				Namespace: mhcNamespace,
			},
			Spec: v1beta1.MachineHealthCheckSpec{
				MaxUnhealthy: &intstr.IntOrString{IntVal: 1},
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{
						"machine.openshift.io/cluster-api-machine-role": "worker",
					},
				},
				RemediationTemplate: &v1.ObjectReference{
					Kind:       "SelfNodeRemediationTemplate",
					APIVersion: "self-node-remediation.medik8s.io/v1alpha1",
					Name:       snrTemplateName,
					Namespace:  mhcNamespace,
				},
				UnhealthyConditions: []v1beta1.UnhealthyCondition{
					{
						Type:    "Ready",
						Status:  "False",
						Timeout: metav1.Duration{Duration: unhealthyConditionDuration},
					},
					{
						Type:    "Ready",
						Status:  "Unknown",
						Timeout: metav1.Duration{Duration: unhealthyConditionDuration},
					},
				},
			},
		}

		if nodeUnderTest == nil {
			// find a worker node
			workers = &v1.NodeList{}
			selector := labels.NewSelector()
			reqCpRole, _ := labels.NewRequirement(commonlabels.ControlPlaneRole, selection.DoesNotExist, []string{})
			reqMRole, _ := labels.NewRequirement(commonlabels.MasterRole, selection.DoesNotExist, []string{})
			selector = selector.Add(*reqCpRole, *reqMRole)
			Expect(k8sClient.List(context.Background(), workers, &ctrl.ListOptions{LabelSelector: selector})).ToNot(HaveOccurred())
			Expect(len(workers.Items)).To(BeNumerically(">=", 3))
			nodeUnderTest = &workers.Items[0]

			var err error
			_, _, err = controllerutils.GetMachineNamespaceName(nodeUnderTest)
			Expect(err).ToNot(HaveOccurred(), "failed to get machine name from node")

			leaseName = fmt.Sprintf("%s-%s", "node", nodeUnderTest.Name)
		}
	})

	JustBeforeEach(func() {
		// create MHC
		Eventually(func() error {
			return k8sClient.Create(context.Background(), mhc)
		}, "1m", "5s").Should(Succeed())
	})

	JustAfterEach(func() {
		// delete MHC
		// Heads up: DO NOT PUT THIS INTO A DeferCleanup() ABOVE!
		// We need to delete the MHC CR before all inner AfterEach() blocks,
		// in order to not start unwanted remediation!
		// DeferCleanup() runs after ALL AfterEach blocks.
		Eventually(func() error {
			err := k8sClient.Delete(context.Background(), mhc)
			if k8serrors.IsNotFound(err) {
				return nil
			}
			return err
		}, "1m", "5s").Should(Succeed())
	})

	When("Node conditions meets the unhealthy criteria", func() {

		Context("and with valid config", func() {

			var nodeUnhealthyTime time.Time

			It("remediates a host", func() {
				By("making node unhealthy")
				nodeUnhealthyTime = utils.MakeNodeUnready(k8sClient, clientSet, nodeUnderTest, testNsName, log)

				By("ensuring remediation CR exists")
				waitTime := nodeUnhealthyTime.Add(unhealthyConditionDuration + 3*time.Second).Sub(time.Now())
				Eventually(
					ensureRemediationResourceExists(nodeUnderTest.Name, mhcNamespace, remediationGVR), waitTime, "500ms").
					Should(Succeed())

				By("ensuring lease exist")
				lease := &coordv1.Lease{}
				Expect(k8sClient.Get(context.Background(), ctrl.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)).To(Succeed(), "lease not created")

				By("ensuring status is set")
				Eventually(func(g Gomega) {
					mhc = getMachineHealthCheck()
					g.Expect(*mhc.Status.ExpectedMachines).To(BeNumerically("==", len(workers.Items)))
					g.Expect(*mhc.Status.CurrentHealthy).To(BeNumerically("==", len(workers.Items)-1))
					g.Expect(mhc.Status.RemediationsAllowed).To(BeNumerically("==", 0))
					// TODO check conditions
				}, "10s", "500ms").Should(Succeed())

				By("waiting for healthy node condition")
				utils.WaitForNodeHealthyCondition(k8sClient, nodeUnderTest, v1.ConditionTrue)

				By("waiting for triggering remediation CR deletion, else cleanup fails")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(mhc), mhc)).To(Succeed())
					g.Expect(*mhc.Status.CurrentHealthy).To(BeNumerically("==", len(workers.Items)))
				}, "5m", "5s").Should(Succeed(), "CR not deleted")

				By("waiting for CR deletion (after finilizers are removed by snr) in order to trigger lease removal")
				Eventually(
					ensureRemediationResourceDoesNotExist(nodeUnderTest.Name, mhcNamespace, remediationGVR), "5m", "5s").
					Should(Succeed())

				By("ensuring lease removed")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(context.Background(), ctrl.ObjectKey{Name: leaseName, Namespace: leaseNs}, lease)
					g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
				}, "2m", "5s").Should(Succeed(), "lease not deleted")

			})
		})
	})
})

func getMachineHealthCheck() *v1beta1.MachineHealthCheck {
	mhc := &v1beta1.MachineHealthCheck{ObjectMeta: metav1.ObjectMeta{Name: mhcName, Namespace: mhcNamespace}}
	ExpectWithOffset(1, k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(mhc), mhc)).To(Succeed(), "failed to get MHC")
	return mhc
}
