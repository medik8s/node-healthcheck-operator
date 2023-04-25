package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	consolev1alpha1 "github.com/openshift/api/console/v1alpha1"
	"github.com/openshift/api/machine/v1beta1"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/console"
	"github.com/medik8s/node-healthcheck-operator/controllers/mhc"
	nhcUtils "github.com/medik8s/node-healthcheck-operator/controllers/utils"
	"github.com/medik8s/node-healthcheck-operator/e2e/utils"
)

const (
	remediationStartedTimeout = 2 * time.Minute
	nodeRebootedTimeout       = 10 * time.Minute
	nhcName                   = "test-nhc"
)

var (
	labelOcpOnly = Label("OCP-ONLY")
)

var _ = Describe("e2e", func() {
	var nodeUnderTest *v1.Node
	var node2 *v1.Node
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
							Key:      nhcUtils.ControlPlaneRoleLabel,
							Operator: metav1.LabelSelectorOpDoesNotExist,
						},
						{
							Key:      nhcUtils.MasterRoleLabel,
							Operator: metav1.LabelSelectorOpDoesNotExist,
						},
					},
				},
				RemediationTemplate: &v1.ObjectReference{
					Kind:       "SelfNodeRemediationTemplate",
					APIVersion: "self-node-remediation.medik8s.io/v1alpha1",
					Name:       "self-node-remediation-resource-deletion-template",
					Namespace:  operatorNsName,
				},
				UnhealthyConditions: []v1alpha1.UnhealthyCondition{
					{
						Type:     "Ready",
						Status:   "False",
						Duration: metav1.Duration{Duration: 10 * time.Second},
					},
					{
						Type:     "Ready",
						Status:   "Unknown",
						Duration: metav1.Duration{Duration: 10 * time.Second},
					},
				},
			},
		}

		if nodeUnderTest == nil {
			// find a worker node
			workers := &v1.NodeList{}
			selector := labels.NewSelector()
			reqCpRole, _ := labels.NewRequirement("node-role.kubernetes.io/control-plane", selection.DoesNotExist, []string{})
			reqMRole, _ := labels.NewRequirement("node-role.kubernetes.io/master", selection.DoesNotExist, []string{})
			selector = selector.Add(*reqCpRole, *reqMRole)
			Expect(k8sClient.List(context.Background(), workers, &ctrl.ListOptions{LabelSelector: selector})).ToNot(HaveOccurred())
			Expect(len(workers.Items)).To(BeNumerically(">=", 3))
			nodeUnderTest = &workers.Items[1]
			node2 = &workers.Items[2]
		}
	})

	JustBeforeEach(func() {
		// create NHC
		DeferCleanup(func() {
			Expect(k8sClient.Delete(context.Background(), nhc)).To(Succeed())
		})
		Expect(k8sClient.Create(context.Background(), nhc)).To(Succeed())
	})

	When("when the operator and the console plugin is deployed", labelOcpOnly, func() {
		It("the plugin manifest should be served", func() {
			By("getting the ConsolePlugin")
			plugin := &consolev1alpha1.ConsolePlugin{}
			Expect(k8sClient.Get(context.Background(), ctrl.ObjectKey{Name: console.PluginName}, plugin)).To(Succeed(), "failed to get ConsolePlugin")

			By("getting the plugin Service")
			svc := &v1.Service{}
			Expect(k8sClient.Get(context.Background(), ctrl.ObjectKey{Namespace: plugin.Spec.Service.Namespace, Name: plugin.Spec.Service.Name}, svc)).To(Succeed(), "failed to get plugin Service")

			By("getting the console manifest")
			manifestUrl := fmt.Sprintf("https://%s:%d/%s/plugin-manifest.json", svc.Spec.ClusterIP, svc.Spec.Ports[0].Port, plugin.Spec.Service.BasePath)
			cmd := fmt.Sprintf("curl -k %s", manifestUrl)
			output, err := utils.RunCommandInCluster(clientSet, nodeUnderTest.Name, testNsName, cmd, log)
			Expect(err).ToNot(HaveOccurred())
			log.Info("got manifest (stripped)", "manifest", output[:100])
			Expect(output).To(ContainSubstring(console.PluginName), "failed to get correct plugin manifest")
		})
	})

	Context("with custom MHC", labelOcpOnly, func() {
		var mhc *v1beta1.MachineHealthCheck
		BeforeEach(func() {
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
			Expect(k8sClient.Create(context.Background(), mhc)).To(Succeed())
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(context.Background(), mhc)).To(Succeed())
			// ensure NHC reverts to enabled
			Eventually(func(g Gomega) {
				nhc = getConfig()
				g.Expect(meta.IsStatusConditionTrue(nhc.Status.Conditions, v1alpha1.ConditionTypeDisabled)).To(BeFalse(), "disabled condition should be false")
				g.Expect(nhc.Status.Phase).To(Equal(v1alpha1.PhaseEnabled), "phase should be Enabled")
			}, 3*time.Minute, 5*time.Second).Should(Succeed(), "NHC should be enabled")
		})

		It("should report disabled NHC", func() {
			Eventually(func(g Gomega) {
				nhc = getConfig()
				g.Expect(meta.IsStatusConditionTrue(nhc.Status.Conditions, v1alpha1.ConditionTypeDisabled)).To(BeTrue(), "disabled condition should be true")
				g.Expect(nhc.Status.Phase).To(Equal(v1alpha1.PhaseDisabled), "phase should be Disabled")
			}, 3*time.Minute, 5*time.Second).Should(Succeed(), "NHC should be disabled because of custom MHC")
		})
	})

	Context("with terminating node", labelOcpOnly, func() {
		BeforeEach(func() {
			Expect(k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(nodeUnderTest), nodeUnderTest)).To(Succeed())
			conditions := nodeUnderTest.Status.Conditions
			conditions = append(conditions, v1.NodeCondition{
				Type:   mhc.NodeConditionTerminating,
				Status: "True",
			})
			nodeUnderTest.Status.Conditions = conditions
			Expect(k8sClient.Status().Update(context.Background(), nodeUnderTest)).To(Succeed())

			makeNodeUnready(nodeUnderTest)
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
		})

		It("should not remediate", func() {
			Consistently(
				fetchRemediationResourceByName(nodeUnderTest.Name, remediationTemplateGVR, remediationGVR), remediationStartedTimeout, 30*time.Second).
				ShouldNot(Succeed())
		})
	})

	When("Node conditions meets the unhealthy criteria", func() {

		Context("with classic remediation config", func() {

			It("Remediates a host and prevents config updates", func() {
				By("making node unhealthy")
				makeNodeUnready(nodeUnderTest)

				By("ensuring remediation CR exists")
				Eventually(
					fetchRemediationResourceByName(nodeUnderTest.Name, remediationTemplateGVR, remediationGVR), remediationStartedTimeout, 5*time.Second).
					Should(Succeed())

				By("ensuring status is set")
				Eventually(func(g Gomega) {
					nhc = getConfig()
					g.Expect(nhc.Status.InFlightRemediations).To(HaveLen(1))
					g.Expect(nhc.Status.UnhealthyNodes).To(HaveLen(1))
					g.Expect(nhc.Status.UnhealthyNodes[0].Remediations).To(HaveLen(1))
					g.Expect(nhc.Status.Phase).To(Equal(v1alpha1.PhaseRemediating))
				}, "10s", "2s").Should(Succeed())

				// let's do some NHC validation tests here
				// wrap 1st webhook test in eventually in order to wait until webhook is up and running
				By("ensuring negative minHealthy update fails")
				nhc = getConfig()
				negValue := intstr.FromInt(-1)
				nhc.Spec.MinHealthy = &negValue
				Expect(k8sClient.Update(context.Background(), nhc)).To(MatchError(ContainSubstring("MinHealthy")), "negative minHealthy update should be prevented")

				By("ensuring selector update fails")
				nhc = getConfig()
				nhc.Spec.Selector = metav1.LabelSelector{
					MatchLabels: map[string]string{
						"foo": "bar",
					},
				}
				Expect(k8sClient.Update(context.Background(), nhc)).To(MatchError(ContainSubstring(v1alpha1.OngoingRemediationError)), "selector update should be prevented")

				By("ensuring config deletion fails")
				nhc = getConfig()
				Expect(k8sClient.Delete(context.Background(), nhc)).To(MatchError(ContainSubstring(v1alpha1.OngoingRemediationError)), "deletion should be prevented")

				By("ensuring minHealthy update succeeds")
				nhc = getConfig()
				newValue := intstr.FromString("10%")
				nhc.Spec.MinHealthy = &newValue
				Expect(k8sClient.Update(context.Background(), nhc)).To(Succeed(), "minHealthy update should be allowed")

				By("waiting for healthy node")
				waitForNodeHealthyCondition(nodeUnderTest, v1.ConditionTrue)

			})
		})

		Context("with escalating remediation config", func() {

			firstTimeout := metav1.Duration{Duration: 1 * time.Minute}

			BeforeEach(func() {

				nodeUnderTest = node2

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
						Timeout: firstTimeout,
					},
					{
						RemediationTemplate: v1.ObjectReference{
							Kind:       "SelfNodeRemediationTemplate",
							APIVersion: "self-node-remediation.medik8s.io/v1alpha1",
							Name:       "self-node-remediation-resource-deletion-template",
							Namespace:  operatorNsName,
						},
						Order:   5,
						Timeout: metav1.Duration{Duration: 5 * time.Minute},
					},
				}
			})

			It("Remediates a host", func() {
				By("making node unhealthy")
				makeNodeUnready(nodeUnderTest)

				By("ensuring 1st remediation CR exists")
				Eventually(
					fetchRemediationResourceByName(nodeUnderTest.Name,
						schema.GroupVersionResource{
							Group:    dummyRemediationTemplateGVK.Group,
							Version:  dummyRemediationTemplateGVK.Version,
							Resource: strings.ToLower(dummyRemediationTemplateGVK.Kind) + "s",
						},
						schema.GroupVersionResource{
							Group:    dummyRemediationGVK.Group,
							Version:  dummyRemediationGVK.Version,
							Resource: strings.ToLower(dummyRemediationGVK.Kind) + "s",
						},
					), remediationStartedTimeout, 5*time.Second).
					Should(Succeed())

				By("waiting and checking 1st remediation timed out")
				var nhc *v1alpha1.NodeHealthCheck
				Eventually(func() *metav1.Time {
					nhc = getConfig()
					log.Info("checking timeout", "node", nhc.Status.UnhealthyNodes[0].Name, "remediation kind", nhc.Status.UnhealthyNodes[0].Remediations[0].Resource.Kind, "timedOut", nhc.Status.UnhealthyNodes[0].Remediations[0].TimedOut)
					return nhc.Status.UnhealthyNodes[0].Remediations[0].TimedOut
				}, firstTimeout.Duration+20*time.Second, 5*time.Second).ShouldNot(BeNil(), "1st remediation should have timed out")

				By("ensuring 2nd remediation CR exists")
				Eventually(
					fetchRemediationResourceByName(nodeUnderTest.Name, remediationTemplateGVR, remediationGVR), remediationStartedTimeout, 5*time.Second).
					Should(Succeed())

				By("ensuring status is set")
				Eventually(func(g Gomega) {
					nhc = getConfig()
					g.Expect(nhc.Status.InFlightRemediations).To(HaveLen(1))
					g.Expect(nhc.Status.UnhealthyNodes).To(HaveLen(1))
					g.Expect(nhc.Status.UnhealthyNodes[0].Remediations).To(HaveLen(2))
					g.Expect(nhc.Status.Phase).To(Equal(v1alpha1.PhaseRemediating))
				}, "10s", "2s").Should(Succeed())

				By("waiting for healthy node")
				waitForNodeHealthyCondition(nodeUnderTest, v1.ConditionTrue)
			})
		})

	})
})

func getConfig() *v1alpha1.NodeHealthCheck {
	nhc := &v1alpha1.NodeHealthCheck{ObjectMeta: metav1.ObjectMeta{Name: nhcName}}
	ExpectWithOffset(1, k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(nhc), nhc)).To(Succeed(), "failed to get NHC")
	return nhc
}

func fetchRemediationResourceByName(name string, remediationTemplateResource, remediationResource schema.GroupVersionResource) func() error {
	return func() error {
		ns := getTemplateNS(remediationTemplateResource)
		rem, err := dynamicClient.Resource(remediationResource).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			log.Info("didn't find remediation resource yet", "name", name)
			return err
		}
		log.Info("found remediation resource", "name", rem.GetName())
		return nil
	}
}

func getTemplateNS(templateResource schema.GroupVersionResource) string {
	list, err := dynamicClient.Resource(templateResource).List(context.Background(), metav1.ListOptions{})
	Expect(err).ToNot(HaveOccurred())
	Expect(list.Items).ToNot(BeEmpty())
	// just use the 1st template for simplicity...
	return list.Items[0].GetNamespace()
}

func makeNodeUnready(node *v1.Node) {
	log.Info("making node unready", "node name", node.GetName())
	// check if node is unready already
	Expect(k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(node), node)).To(Succeed())
	for _, cond := range node.Status.Conditions {
		if cond.Type == v1.NodeReady && cond.Status == v1.ConditionUnknown {
			log.Info("node is already unready", "node name", node.GetName())
			return
		}
	}
	Expect(modifyKubelet(node, "stop")).To(Succeed())
	waitForNodeHealthyCondition(node, v1.ConditionUnknown)
	log.Info("node is unready", "node name", node.GetName())
}

func modifyKubelet(node *v1.Node, what string) error {
	cmd := "microdnf install util-linux -y && /usr/bin/nsenter -m/proc/1/ns/mnt /bin/systemctl " + what + " kubelet"
	_, err := utils.RunCommandInCluster(clientSet, node.Name, testNsName, cmd, log)
	if err != nil && strings.Contains(err.Error(), "connection refused") {
		log.Info("ignoring expected error when stopping kubelet", "error", err.Error())
		return nil
	}
	return err
}

func waitForNodeHealthyCondition(node *v1.Node, status v1.ConditionStatus) {
	Eventually(func() v1.ConditionStatus {
		Expect(k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(node), node)).To(Succeed())
		for _, cond := range node.Status.Conditions {
			if cond.Type == v1.NodeReady {
				return cond.Status
			}
		}
		return v1.ConditionStatus("failure")
	}, nodeRebootedTimeout, 15*time.Second).Should(Equal(status))
}
