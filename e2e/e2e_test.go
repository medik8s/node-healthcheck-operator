package e2e

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	consolev1alpha1 "github.com/openshift/api/console/v1alpha1"
	"github.com/openshift/api/machine/v1beta1"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/console"
	"github.com/medik8s/node-healthcheck-operator/controllers/mhc"
	"github.com/medik8s/node-healthcheck-operator/e2e/utils"
)

const (
	remediationStartedTimeout = 10 * time.Minute
	nodeRebootedTimeout       = 10 * time.Minute
)

var _ = Describe("e2e", func() {
	var nodeUnderTest *v1.Node
	var testStart time.Time

	BeforeEach(func() {
		// randomly pick a host (or let the scheduler do it by running the blocking pod)
		// block the api port to make it go Ready Unknown
		if nodeUnderTest == nil {

			// find a worker node
			workers := &v1.NodeList{}
			selector := labels.NewSelector()
			reqCpRole, _ := labels.NewRequirement("node-role.kubernetes.io/control-plane", selection.DoesNotExist, []string{})
			reqMRole, _ := labels.NewRequirement("node-role.kubernetes.io/master", selection.DoesNotExist, []string{})
			selector = selector.Add(*reqCpRole, *reqMRole)
			Expect(k8sClient.List(context.Background(), workers, &ctrl.ListOptions{LabelSelector: selector})).ToNot(HaveOccurred())
			Expect(len(workers.Items)).To(BeNumerically(">=", 2))
			nodeUnderTest = &workers.Items[0]

			// save boot time
			testStart = time.Now()

		}

	})

	When("when the operator and the console plugin is deployed", func() {
		It("the plugin manifest should be served", func() {
			// console deployment is disabled on k8s
			if _, exists := os.LookupEnv("SKIP_FOR_K8S"); exists {
				Skip("skipping console plugin test as requested by $SKIP_FOR_K8S env var")
			}

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

	Context("with custom MHC", func() {
		var mhc *v1beta1.MachineHealthCheck
		BeforeEach(func() {
			// we have no MHC on k8s
			if _, exists := os.LookupEnv("SKIP_FOR_K8S"); exists {
				Skip("skipping MHC test as requested by $SKIP_FOR_K8S env var")
			}

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
			if _, exists := os.LookupEnv("SKIP_FOR_K8S"); exists {
				Skip("skipping MHC test as requested by $SKIP_FOR_K8S env var")
			}

			Expect(k8sClient.Delete(context.Background(), mhc)).To(Succeed())
			// ensure NHC reverts to enabled
			Eventually(func(g Gomega) {
				nhc := getConfig()
				g.Expect(meta.IsStatusConditionTrue(nhc.Status.Conditions, v1alpha1.ConditionTypeDisabled)).To(BeFalse(), "disabled condition should be false")
				g.Expect(nhc.Status.Phase).To(Equal(v1alpha1.PhaseEnabled), "phase should be Enabled")
			}, 3*time.Minute, 5*time.Second).Should(Succeed(), "NHC should be enabled")
		})

		It("should report disabled NHC", func() {
			Eventually(func(g Gomega) {
				nhc := getConfig()
				g.Expect(meta.IsStatusConditionTrue(nhc.Status.Conditions, v1alpha1.ConditionTypeDisabled)).To(BeTrue(), "disabled condition should be true")
				g.Expect(nhc.Status.Phase).To(Equal(v1alpha1.PhaseDisabled), "phase should be Disabled")
			}, 3*time.Minute, 5*time.Second).Should(Succeed(), "NHC should be disabled because of custom MHC")
		})
	})

	Context("with terminating node", func() {
		BeforeEach(func() {
			// on k8s, the node will be remediated because of missing MHC logic, so skip this test
			// heads up, that means the node is not unhealthy yet for following tests!
			if _, exists := os.LookupEnv("SKIP_FOR_K8S"); exists {
				Skip("skipping console plugin test as requested by $SKIP_FOR_K8S env var")
			}

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
			if _, exists := os.LookupEnv("SKIP_FOR_K8S"); exists {
				Skip("skipping MHC test as requested by $SKIP_FOR_K8S env var")
			}

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
				fetchRemediationResourceByName(nodeUnderTest.Name), remediationStartedTimeout, 30*time.Second).
				ShouldNot(Succeed())
		})
	})

	When("Node conditions meets the unhealthy criteria", func() {

		BeforeEach(func() {
			// on k8s, the node is not made unready yet
			if _, exists := os.LookupEnv("SKIP_FOR_K8S"); exists {
				makeNodeUnready(nodeUnderTest)
			}
		})

		It("Remediates a host and prevents config updates", func() {
			By("ensuring remediation CR exists")
			Eventually(
				fetchRemediationResourceByName(nodeUnderTest.Name), remediationStartedTimeout, 10*time.Second).
				Should(Succeed())

			// let's do some NHC validation tests here
			// wrap 1st webhook test in eventually in order to wait until webhook is up and running
			By("ensuring negative minHealthy update fails")
			Eventually(func() error {
				nhc := getConfig()
				negValue := intstr.FromInt(-1)
				nhc.Spec.MinHealthy = &negValue
				return k8sClient.Update(context.Background(), nhc)
			}, 20*time.Second, 5*time.Second).Should(MatchError(ContainSubstring("MinHealthy")), "negative minHealthy update should be prevented")

			By("ensuring selector update fails")
			nhc := getConfig()
			nhc.Spec.Selector = metav1.LabelSelector{
				MatchLabels: map[string]string{
					"foo": "bar",
				},
			}
			err := k8sClient.Update(context.Background(), nhc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(v1alpha1.OngoingRemediationError), "selector update should be prevented")

			By("ensuring config deletion fails")
			nhc = getConfig()
			err = k8sClient.Delete(context.Background(), nhc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(v1alpha1.OngoingRemediationError), "deletion should be prevented")

			By("ensuring minHealthy update succeeds")
			nhc = getConfig()
			newValue := intstr.FromString("52%")
			nhc.Spec.MinHealthy = &newValue
			err = k8sClient.Update(context.Background(), nhc)
			Expect(err).ToNot(HaveOccurred(), "minHealthy update should be allowed")

			// TODO node reboot doesn't work on k8s
			// investigate if this is something we can fix in SNR
			// not relevant for NHC itself, so ignore for now
			if _, exists := os.LookupEnv("SKIP_FOR_K8S"); !exists {
				By("waiting for reboot")
				Eventually(func() (time.Time, error) {
					bootTime, err := utils.GetBootTime(clientSet, nodeUnderTest.Name, testNsName, log)
					if bootTime != nil && err == nil {
						log.Info("got boot time", "time", *bootTime)
						return *bootTime, nil
					}
					log.Error(err, "failed to get boot time")
					return time.Time{}, err
				}, nodeRebootedTimeout, 30*time.Second).Should(
					BeTemporally(">", testStart),
				)
			} else {
				log.Info("skipping reboot check on k8s, doesn't work atm")
			}
		})
	})
})

func getConfig() *v1alpha1.NodeHealthCheck {
	nhcList := &v1alpha1.NodeHealthCheckList{}
	ExpectWithOffset(1, k8sClient.List(context.Background(), nhcList)).To(Succeed(), "failed to list NHCs")
	ExpectWithOffset(1, nhcList.Items).To(HaveLen(1), "less or more than 1 NHC found")
	return &nhcList.Items[0]
}

func fetchRemediationResourceByName(name string) func() error {
	return func() error {
		ns, err := getTemplateNS()
		if err != nil {
			return err
		}
		get, err := dynamicClient.Resource(remediationGVR).Namespace(ns).
			Get(context.Background(),
				name,
				metav1.GetOptions{})
		if err != nil {
			return err
		}
		log.Info("found remediation resource", "name", get.GetName())
		return nil
	}
}

func getTemplateNS() (string, error) {
	list, err := dynamicClient.Resource(remediationTemplateGVR).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	for _, t := range list.Items {
		if t.GetName() == "self-node-remediation-resource-deletion-template" {
			return t.GetNamespace(), err
		}
	}

	return "", fmt.Errorf("failed to find the default remediation template")
}

func makeNodeUnready(node *v1.Node) {
	modifyKubelet(node, "stop")
	waitForNodeHealthyCondition(node, v1.ConditionUnknown)
}

func modifyKubelet(node *v1.Node, what string) {
	cmd := "microdnf install util-linux -y && /usr/bin/nsenter -m/proc/1/ns/mnt /bin/systemctl " + what + " kubelet"
	_, err := utils.RunCommandInCluster(clientSet, node.Name, testNsName, cmd, log)
	if err != nil {
		log.Info("ignoring expected error when stopping kubelet", "error", err.Error())
	}
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
	}, 6*time.Minute, 15*time.Second).Should(Equal(status))
}
