package utils

import (
	"context"
	"strings"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
)

const nodeRebootedTimeout = 10 * time.Minute

func MakeNodeUnready(k8sClient ctrl.Client, clientSet *kubernetes.Clientset, node *v1.Node, namespace string, log logr.Logger) time.Time {
	log.Info("making node unready", "node name", node.GetName())
	// check if node is unready already
	Expect(k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(node), node)).To(Succeed())
	for _, cond := range node.Status.Conditions {
		if cond.Type == v1.NodeReady && cond.Status == v1.ConditionUnknown {
			log.Info("node is already unready", "node name", node.GetName())
			return cond.LastTransitionTime.Time
		}
	}
	Expect(modifyKubelet(clientSet, node, namespace, "stop", log)).To(Succeed())
	transitionTime := WaitForNodeHealthyCondition(k8sClient, node, v1.ConditionUnknown)
	log.Info("node is unready", "node name", node.GetName())
	return transitionTime
}

func modifyKubelet(clientSet *kubernetes.Clientset, node *v1.Node, namespace string, what string, log logr.Logger) error {
	cmd := "microdnf install util-linux -y && /usr/bin/nsenter -m/proc/1/ns/mnt /bin/systemctl " + what + " kubelet"
	_, err := RunCommandInCluster(clientSet, node.Name, namespace, cmd, log)
	if err != nil && strings.Contains(err.Error(), "connection refused") {
		log.Info("ignoring expected error when stopping kubelet", "error", err.Error())
		return nil
	}
	return err
}

func WaitForNodeHealthyCondition(k8sClient ctrl.Client, node *v1.Node, status v1.ConditionStatus) time.Time {
	var transitionTime time.Time
	Eventually(func() v1.ConditionStatus {
		Expect(k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(node), node)).To(Succeed())
		for _, cond := range node.Status.Conditions {
			if cond.Type == v1.NodeReady {
				transitionTime = cond.LastTransitionTime.Time
				return cond.Status
			}
		}
		return v1.ConditionStatus("failure")
	}, nodeRebootedTimeout, 1*time.Second).Should(Equal(status))
	return transitionTime
}
