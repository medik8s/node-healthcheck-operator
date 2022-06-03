package e2e

import (
	"context"
	"fmt"
	"time"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/mhc"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/api/machine/v1beta1"
	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	blockingPodName                = "api-blocker-pod"
	safeToAssumeremediationStarted = 10 * time.Minute
	safeToAssumeNodeRebootTimeout  = 180 * time.Second
	// keep this aligned with CI config!
	testNamespace = "default"
)

var _ = Describe("e2e", func() {
	var nodeUnderTest *v1.Node

	BeforeEach(func() {
		// randomly pick a host (or let the scheduler do it by running the blocking pod)
		// block the api port to make it go Ready Unknown
		if nodeUnderTest == nil {
			nodeName, err := makeNodeUnready(time.Minute, time.Minute*10)
			Expect(err).NotTo(HaveOccurred())
			nodeUnderTest, err = clientSet.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			// set terminating node condition now, to prevent remediation start before "with terminating node" test runs
			Expect(client.Get(context.Background(), ctrl.ObjectKeyFromObject(nodeUnderTest), nodeUnderTest)).To(Succeed())
			conditions := nodeUnderTest.Status.Conditions
			conditions = append(conditions, v1.NodeCondition{
				Type:   mhc.NodeConditionTerminating,
				Status: "True",
			})
			nodeUnderTest.Status.Conditions = conditions
			Expect(client.Status().Update(context.Background(), nodeUnderTest)).To(Succeed())
		}
	})

	AfterEach(func() {
		// keep it running for all tests
		//removeAPIBlockingPod()
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
			Expect(client.Create(context.Background(), mhc)).To(Succeed())
		})

		AfterEach(func() {
			Expect(client.Delete(context.Background(), mhc)).To(Succeed())
		})

		It("should report disabled NHC", func() {
			Eventually(func() (bool, error) {
				nhcList := &v1alpha1.NodeHealthCheckList{}
				Expect(client.List(context.Background(), nhcList)).To(Succeed())
				if len(nhcList.Items) != 1 {
					return false, errors.New("less or more than 1 NHC found")
				}
				return meta.IsStatusConditionTrue(nhcList.Items[0].Status.Conditions, v1alpha1.ConditionTypeDisabled), nil
			}, 1*time.Minute, 5*time.Second).Should(BeTrue(), "NHC should be disabled because of custom MHC")
		})
	})

	Context("with terminating node", func() {
		BeforeEach(func() {
			// ensure node is terminating
			Eventually(func() (bool, error) {
				if err := client.Get(context.Background(), ctrl.ObjectKeyFromObject(nodeUnderTest), nodeUnderTest); err != nil {
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
			Eventually(func() (bool, error) {
				nhcList := &v1alpha1.NodeHealthCheckList{}
				if err := client.List(context.Background(), nhcList); err != nil {
					return false, err
				}
				if len(nhcList.Items) != 1 {
					return false, errors.New("less or more than 1 NHC found")
				}
				return meta.IsStatusConditionTrue(nhcList.Items[0].Status.Conditions, v1alpha1.ConditionTypeDisabled), nil
			}, 1*time.Minute, 5*time.Second).Should(BeFalse(), "NHC should be enabled")

		})

		AfterEach(func() {
			Expect(client.Get(context.Background(), ctrl.ObjectKeyFromObject(nodeUnderTest), nodeUnderTest)).To(Succeed())
			conditions := nodeUnderTest.Status.Conditions
			for i, cond := range conditions {
				if cond.Type == mhc.NodeConditionTerminating {
					conditions = append(conditions[:i], conditions[i+1:]...)
					break
				}
			}
			nodeUnderTest.Status.Conditions = conditions
			Expect(client.Status().Update(context.Background(), nodeUnderTest)).To(Succeed())
		})

		It("should not remediate", func() {
			Consistently(
				fetchPPRByName(nodeUnderTest.Name), safeToAssumeremediationStarted, 30*time.Second).
				ShouldNot(Succeed())
		})
	})

	When("Node conditions meets the unhealthy criteria", func() {

		BeforeEach(func() {
			// ensure node is not terminating
			Eventually(func() (bool, error) {
				if err := client.Get(context.Background(), ctrl.ObjectKeyFromObject(nodeUnderTest), nodeUnderTest); err != nil {
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

		It("Remediates a host", func() {
			Eventually(
				fetchPPRByName(nodeUnderTest.Name), safeToAssumeremediationStarted, 10*time.Second).
				Should(Succeed())
			Eventually(
				nodeCreationTime(nodeUnderTest.Name), safeToAssumeNodeRebootTimeout+30*time.Second, 250*time.Millisecond).
				Should(BeTemporally(">", nodeUnderTest.GetCreationTimestamp().Time))
		})
	})
})

func nodeCreationTime(nodeName string) func() time.Time {
	return func() time.Time {
		n, err := clientSet.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		if err != nil {
			return time.Now()
		}
		return n.GetCreationTimestamp().Time
	}
}

func fetchPPRByName(name string) func() error {
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
		fmt.Fprintf(GinkgoWriter, "found a ppil object %v  that should remediate node %v\n", get.GetName(), name)
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
func makeNodeUnready(delayDuration, sleepDuration time.Duration) (string, error) {
	// run a privileged pod that blocks the api port

	directory := v1.HostPathDirectory
	var p = v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: blockingPodName},
		Spec: v1.PodSpec{
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
						Value: fmt.Sprintf("%v", delayDuration.Seconds()),
					},
					{
						Name:  "SLEEPDURATION",
						Value: fmt.Sprintf("%v", sleepDuration.Seconds()),
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
		Pods(testNamespace).
		Create(context.Background(), &p, metav1.CreateOptions{})
	if err != nil {
		return "", errors.Wrap(err, "Failed to run the api-blocker pod")
	}
	var runsOnNode string
	err = wait.Poll(5*time.Second, 60*time.Second, func() (done bool, err error) {
		get, err := clientSet.CoreV1().Pods(testNamespace).Get(context.Background(), blockingPodName, metav1.GetOptions{})
		fmt.Fprint(GinkgoWriter, "attempting to run a pod to block the api port\n")
		if err != nil {
			return false, err
		}
		if get.Status.Phase == v1.PodRunning {
			runsOnNode = get.Spec.NodeName
			fmt.Fprint(GinkgoWriter, "API blocker pod is running\n")
			return true, nil
		}
		return false, nil
	})
	return runsOnNode, err
}

func removeAPIBlockingPod() {
	clientSet.CoreV1().Pods(testNamespace).Delete(context.Background(), blockingPodName, metav1.DeleteOptions{})
}
