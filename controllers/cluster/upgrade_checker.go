package cluster

import (
	"context"

	"github.com/go-logr/logr"
	gerrors "github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	v1 "github.com/openshift/api/config/v1"
	clusterversion "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
)

const (
	// currentMachineConfigAnnotationKey is used to fetch current targetConfigVersionHash
	currentMachineConfigAnnotationKey = "machineconfiguration.openshift.io/currentConfig"
	// desiredMachineConfigAnnotationKey is used to indicate the version a node should be updating to
	desiredMachineConfigAnnotationKey = "machineconfiguration.openshift.io/desiredConfig"
)

// UpgradeChecker checks if the cluster is currently under upgrade.
// error should be thrown if it can't reliably determine if it's under upgrade or not.
type UpgradeChecker interface {
	// Check if the cluster is currently under upgrade.
	// error should be thrown if it can't reliably determine if it's under upgrade or not.
	Check(nodesToBeRemediated []corev1.Node) (bool, error)
}

type openshiftClusterUpgradeStatusChecker struct {
	client                client.Client
	clusterVersionsClient clusterversion.ClusterVersionInterface
	logger                logr.Logger
}

// force implementation of interface
var _ UpgradeChecker = &openshiftClusterUpgradeStatusChecker{}

func (o *openshiftClusterUpgradeStatusChecker) Check(nodesToBeRemediated []corev1.Node) (bool, error) {
	if o.isNodeUpgrading(nodesToBeRemediated) {
		return true, nil
	}

	cvs, err := o.clusterVersionsClient.List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return false, gerrors.Wrap(err, "failed to check for Openshift cluster upgrade status")
	}
	for _, cv := range cvs.Items {
		for _, condition := range cv.Status.Conditions {
			if condition.Type == v1.OperatorProgressing && condition.Status == v1.ConditionTrue {
				o.logger.V(5).Info("cluster looks like is under and upgrade", "clusterversion.status.conditions.progressing", condition)
				return true, nil
			}
		}
	}
	return false, nil
}

func (o *openshiftClusterUpgradeStatusChecker) isNodeUpgrading(nodesToBeRemediated []corev1.Node) bool {
	// We can identify an HCP (Hosted Control Plane) cluster upgrade by checking annotations of the CP node.
	for _, node := range nodesToBeRemediated {
		if len(node.Annotations) == 0 || len(node.Annotations[desiredMachineConfigAnnotationKey]) == 0 {
			continue
		}

		if node.Annotations[currentMachineConfigAnnotationKey] != node.Annotations[desiredMachineConfigAnnotationKey] {
			return true
		}
	}
	return false
}

type noopClusterUpgradeStatusChecker struct {
}

// force implementation of interface
var _ UpgradeChecker = &noopClusterUpgradeStatusChecker{}

func (n *noopClusterUpgradeStatusChecker) Check([]corev1.Node) (bool, error) {
	return false, nil
}

// NewClusterUpgradeStatusChecker will return some implementation of a checker or err in case it can't
// reliably detect which implementation to use.
func NewClusterUpgradeStatusChecker(mgr manager.Manager, caps Capabilities) (UpgradeChecker, error) {
	if !caps.IsOnOpenshift {
		return &noopClusterUpgradeStatusChecker{}, nil
	}
	checker, err := newOpenshiftClusterUpgradeChecker(mgr)
	if err != nil {
		return nil, err
	}
	return checker, nil
}

func newOpenshiftClusterUpgradeChecker(mgr manager.Manager) (*openshiftClusterUpgradeStatusChecker, error) {
	configV1Client, err := clusterversion.NewForConfig(mgr.GetConfig())
	if err != nil {
		return nil, gerrors.Wrap(err, "failed to create a client to Openshift ClusterVersion objects")
	}
	return &openshiftClusterUpgradeStatusChecker{
		client:                mgr.GetClient(),
		clusterVersionsClient: configV1Client.ClusterVersions(),
		logger:                mgr.GetLogger().WithName("OpenshiftClusterUpgradeChecker"),
	}, nil
}
