package cluster

import (
	"context"
	"errors"

	"github.com/go-logr/logr"
	gerrors "github.com/pkg/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	v1 "github.com/openshift/api/config/v1"
	clusterversion "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
)

var unsupportedUpgradeCheckerErr = errors.New(
	"the cluster doesn't have any upgrade state representation." +
		" Currently only OpenShift/OKD is supported")

// UpgradeChecker checks if the cluster is currently under upgrade.
// error should be thrown if it can't reliably determine if it's under upgrade or not.
type UpgradeChecker interface {
	// Check if the cluster is currently under upgrade.
	// error should be thrown if it can't reliably determine if it's under upgrade or not.
	Check() (bool, error)
}

type openshiftClusterUpgradeStatusChecker struct {
	client                client.Client
	clusterVersionsClient clusterversion.ClusterVersionInterface
	logger                logr.Logger
}

// force implementation of interface
var _ UpgradeChecker = &openshiftClusterUpgradeStatusChecker{}

func (o *openshiftClusterUpgradeStatusChecker) Check() (bool, error) {
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

type noopClusterUpgradeStatusChecker struct {
}

// force implementation of interface
var _ UpgradeChecker = &noopClusterUpgradeStatusChecker{}

func (n *noopClusterUpgradeStatusChecker) Check() (bool, error) {
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
