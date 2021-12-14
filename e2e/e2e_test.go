package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/pointer"
)

const (
	blockingPodName               = "api-blocker-pod"
	safeToAssumeNodeRebootTimeout = 180 * time.Second
	// keep this aligned with CI config!
	testNamespace = "default"
)

var _ = Describe("e2e", func() {
	var nodeUnderTest *v1.Node

	BeforeEach(func() {
		// randomly pick a host (or let the scheduler do it by running the blocking pod)
		// block the api port to make it go Ready Unknown
		nodeName, err := makeNodeUnready(time.Minute * 10)
		Expect(err).NotTo(HaveOccurred())
		nodeUnderTest, err = clientSet.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

	})

	AfterEach(func() {
		removeAPIBlockingPod()
	})
	When("Node conditions meets the unhealthy criteria", func() {
		It("Remediates a host", func() {
			Eventually(
				fetchPPRByName(nodeUnderTest.Name), 10*time.Minute, 10*time.Second).
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
		ns, err := getPPRTemplateNS()
		if err != nil {
			return err
		}
		get, err := dynamicClient.Resource(poisonPillRemediationGVR).Namespace(ns).
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

func getPPRTemplateNS() (string, error) {
	list, err := dynamicClient.Resource(poisonPillTemplateGVR).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	for _, t := range list.Items {
		if t.GetName() == "poison-pill-default-template" {
			return t.GetNamespace(), err
		}
	}

	return "", fmt.Errorf("failed to find the default poison-pill template")
}

//makeNodeUnready puts a node in an unready condition by disrupting the network
// for the duration passed
func makeNodeUnready(duration time.Duration) (string, error) {
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
				Env: []v1.EnvVar{{
					Name:  "SLEEPDURATION",
					Value: fmt.Sprintf("%v", duration.Seconds()),
				}},
				Name:  "main",
				Image: "registry.access.redhat.com/ubi8/ubi-minimal",
				Command: []string{
					"/bin/bash",
					"-c",
					`#!/bin/bash -ex
microdnf install iptables
port=$(awk -F[\:] '/server\:/ {print $NF}' /etc/kubernetes/kubeconfig 2>/dev/null || awk -F[\:] '/server\:/ {print $NF}' /etc/kubernetes/kubelet.conf)
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
