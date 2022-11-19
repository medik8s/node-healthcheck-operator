package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"

	consolev1alpha1 "github.com/openshift/api/console/v1alpha1"
	"github.com/openshift/api/machine/v1beta1"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/console"
	"github.com/medik8s/node-healthcheck-operator/controllers/mhc"
	"github.com/medik8s/node-healthcheck-operator/e2e/utils"
)

const (
	blockingPodName           = "api-blocker-pod"
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
			req, _ := labels.NewRequirement("node-role.kubernetes.io/worker", selection.Exists, []string{})
			selector = selector.Add(*req)
			Expect(k8sClient.List(context.Background(), workers, &ctrl.ListOptions{LabelSelector: selector})).ToNot(HaveOccurred())
			Expect(len(workers.Items)).To(BeNumerically(">=", 2))
			nodeUnderTest = &workers.Items[0]
			err := makeNodeUnready(nodeUnderTest.Name)
			Expect(err).NotTo(HaveOccurred())

			// save boot time
			testStart = time.Now()

			// set terminating node condition now, to prevent remediation start before "with terminating node" test runs
			Expect(k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(nodeUnderTest), nodeUnderTest)).To(Succeed())
			conditions := nodeUnderTest.Status.Conditions
			conditions = append(conditions, v1.NodeCondition{
				Type:   mhc.NodeConditionTerminating,
				Status: "True",
			})
			nodeUnderTest.Status.Conditions = conditions
			Expect(k8sClient.Status().Update(context.Background(), nodeUnderTest)).To(Succeed())
		}

	})

	AfterEach(func() {
		// keep it running for all tests
		//removeAPIBlockingPod()
	})

	When("when the operator and the console plugin is deployed", func() {
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

	Context("with custom MHC", func() {
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
			// ensure node is terminating
			Eventually(func() (bool, error) {
				if err := k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(nodeUnderTest), nodeUnderTest); err != nil {
					return false, err
				}
				for _, cond := range nodeUnderTest.Status.Conditions {
					if cond.Type == mhc.NodeConditionTerminating {
						return true, nil
					}
				}
				return false, nil
			}, 1*time.Minute, 5*time.Second).Should(BeTrue(), "node should not be terminating")

			// ensure NHC is not disabled from previous test
			Eventually(func(g Gomega) {
				nhc := getConfig()
				g.Expect(meta.IsStatusConditionTrue(nhc.Status.Conditions, v1alpha1.ConditionTypeDisabled)).To(BeFalse(), "disabled condition should be false")
				g.Expect(nhc.Status.Phase).To(Equal(v1alpha1.PhaseEnabled), "phase should be Enabled")
			}, 3*time.Minute, 5*time.Second).Should(Succeed(), "NHC should be enabled")

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
				fetchRemediationResourceByName(nodeUnderTest.Name), remediationStartedTimeout, 30*time.Second).
				ShouldNot(Succeed())
		})
	})

	When("Node conditions meets the unhealthy criteria", func() {

		BeforeEach(func() {
			// ensure node is not terminating
			Eventually(func() (bool, error) {
				if err := k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(nodeUnderTest), nodeUnderTest); err != nil {
					return false, err
				}
				for _, cond := range nodeUnderTest.Status.Conditions {
					if cond.Type == mhc.NodeConditionTerminating {
						return true, nil
					}
				}
				return false, nil
			}, 1*time.Minute, 5*time.Second).Should(BeFalse(), "node should not be terminating")
		})

		It("Remediates a host and prevents config updates", func() {
			By("ensuring remediation CR exists")
			Eventually(
				fetchRemediationResourceByName(nodeUnderTest.Name), remediationStartedTimeout, 10*time.Second).
				Should(Succeed())

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

//makeNodeUnready puts a node in an unready condition by disrupting the network
// for the duration passed
func makeNodeUnready(nodeName string) error {
	// run a privileged pod that blocks the api port

	directory := v1.HostPathDirectory
	var p = v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: blockingPodName},
		Spec: v1.PodSpec{
			NodeName: nodeName,
			// for running iptables in the host namespace
			HostNetwork: true,
			SecurityContext: &v1.PodSecurityContext{
				RunAsUser:  pointer.Int64Ptr(0),
				RunAsGroup: pointer.Int64Ptr(0),
			},
			Containers: []v1.Container{{
				Env: []v1.EnvVar{
					{
						Name:  "DELAYDURATION",
						Value: fmt.Sprintf("%v", time.Minute.Seconds()),
					},
					{
						Name:  "SLEEPDURATION",
						Value: fmt.Sprintf("%v", 10*time.Minute.Seconds()),
					},
				},
				Name:  "main",
				Image: "registry.access.redhat.com/ubi8/ubi-minimal",
				Command: []string{
					"/bin/bash",
					"-c",
					`#!/bin/bash -ex
microdnf install iptables
port=$(awk -F[\:] '/server\:/ {print $NF}' /etc/kubernetes/kubeconfig 2>/dev/null || awk -F[\:] '/server\:/ {print $NF}' /etc/kubernetes/kubelet.conf)
sleep ${DELAYDURATION}
iptables -A OUTPUT -p tcp --dport ${port} -j REJECT
sleep ${SLEEPDURATION}
iptables -D OUTPUT -p tcp --dport ${port} -j REJECT
sleep infinity
`,
				},
				VolumeMounts: []v1.VolumeMount{{
					Name:      "etckube",
					MountPath: "/etc/kubernetes",
				}},
				SecurityContext: &v1.SecurityContext{
					Privileged:               pointer.BoolPtr(true),
					AllowPrivilegeEscalation: pointer.BoolPtr(true),
				},
			}},
			Volumes: []v1.Volume{{
				Name: "etckube",
				VolumeSource: v1.VolumeSource{
					HostPath: &v1.HostPathVolumeSource{
						Path: "/etc/kubernetes",
						Type: &directory,
					},
				},
			}},
		},
	}

	_, err := clientSet.CoreV1().
		Pods(testNsName).
		Create(context.Background(), &p, metav1.CreateOptions{})
	if err != nil {
		return errors.Wrap(err, "Failed to run the api-blocker pod")
	}
	err = wait.Poll(5*time.Second, 60*time.Second, func() (done bool, err error) {
		get, err := clientSet.CoreV1().Pods(testNsName).Get(context.Background(), blockingPodName, metav1.GetOptions{})
		log.Info("attempting to run a pod to block the api port")
		if err != nil {
			return false, err
		}
		if get.Status.Phase == v1.PodRunning {
			log.Info("API blocker pod is running")
			return true, nil
		}
		return false, nil
	})
	return err
}

func removeAPIBlockingPod() {
	clientSet.CoreV1().Pods(testNsName).Delete(context.Background(), blockingPodName, metav1.DeleteOptions{})
}
