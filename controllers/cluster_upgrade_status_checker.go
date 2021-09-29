package controllers

import (
	"context"
	"errors"
	"github.com/go-logr/logr"
	v1 "github.com/openshift/api/config/v1"
	clusterversion "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	gerrors "github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var unsupportedUpgradeCheckerErr = errors.New(
	"the cluster doesn't have any upgrade state representation." +
		" Currently only OpenShift/OKD is supported")

// clusterUpgradeChecker checks if the cluster is currently under upgrade.
// error should be thrown if it can't reliably determine if it's under upgrade or not.
type clusterUpgradeChecker interface {
	// check if the cluster is currently under upgrade.
	// error should be thrown if it can't reliably determine if it's under upgrade or not.
	check() (bool, error)
}

type openshiftClusterUpgradeStatusChecker struct {
	client                client.Client
	clusterVersionsClient clusterversion.ClusterVersionInterface
	logger                logr.Logger
}

func (o openshiftClusterUpgradeStatusChecker) check() (bool, error) {
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

func (n noopClusterUpgradeStatusChecker) check() (bool, error) {
	return false, nil
}

// newClusterUpgradeStatusChecker will return some implementation of a checker or err in case it can't
// reliably detect which implementation to use.
func newClusterUpgradeStatusChecker(mgr manager.Manager) (clusterUpgradeChecker, error) {
	openshift, err := isOnOpenshift(mgr.GetConfig(), mgr.GetLogger())
	if err != nil || !openshift {
		if errors.Is(err, unsupportedUpgradeCheckerErr) {
			return noopClusterUpgradeStatusChecker{}, nil
		}
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
		logger:                mgr.GetLogger(),
	}, nil
}

// isOpenshift returns true if the cluster has the openshift config group
func isOnOpenshift(config *rest.Config, logger logr.Logger) (bool, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return false, err
	}
	apiGroups, err := dc.ServerGroups()
	kind := schema.GroupVersionKind{Group: "config.openshift.io", Version: "v1", Kind: "ClusterVersion"}
	for _, apiGroup := range apiGroups.Groups {
		for _, supportedVersion := range apiGroup.Versions {
			if supportedVersion.GroupVersion == kind.GroupVersion().String() {
				return true, nil
			}
		}
	}
	return false, unsupportedUpgradeCheckerErr
}
