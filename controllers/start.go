package controllers

import (
	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/medik8s/node-healthcheck-operator/controllers/cluster"
	"github.com/medik8s/node-healthcheck-operator/controllers/defaults"
	"github.com/medik8s/node-healthcheck-operator/controllers/mhc"
	"github.com/medik8s/node-healthcheck-operator/controllers/rbac"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
)

// NewNodeHealthcheckController setups the Node Health Check controller
// with the controller manager and ensures at least one NHC resource exists.
// If there is no NHC resource it will create one with the name in #DefaultCRName
// that works with poison-pill template by the name in #DefaultPoisonPillTemplateName
// on the same namespace this controller is deployed.
func NewNodeHealthcheckController(mgr manager.Manager, log logr.Logger) error {

	upgradeChecker, err := cluster.NewClusterUpgradeStatusChecker(mgr)
	if err != nil {
		return errors.Wrap(err, "unable initialize cluster upgrade checker")
	}

	mhcChecker, err := mhc.NewMHCChecker(mgr)
	if err != nil {
		return errors.Wrap(err, "unable initialize MHC checker")
	}

	if err := (&NodeHealthCheckReconciler{
		Client: mgr.GetClient(),
		// the dynamic client is here only because the fake client from client-go
		// couldn't List correctly unstructuredList. The fake dynamic client works.
		// See https://github.com/kubernetes-sigs/controller-runtime/issues/702
		DynamicClient:               dynamic.NewForConfigOrDie(mgr.GetConfig()),
		Log:                         ctrl.Log.WithName("controllers").WithName("NodeHealthCheck"),
		Scheme:                      mgr.GetScheme(),
		recorder:                    mgr.GetEventRecorderFor("NodeHealthCheck"),
		clusterUpgradeStatusChecker: upgradeChecker,
		mhcChecker:                  mhcChecker,
	}).SetupWithManager(mgr); err != nil {
		return errors.Wrap(err, "unable to create controller")
	}

	ns, err := utils.GetDeploymentNamespace()
	if err != nil {
		return errors.Wrap(err, "unable to get the deployment namespace")
	}

	if err = rbac.NewAggregation(mgr, ns).CreateOrUpdateAggregation(); err != nil {
		return errors.Wrap(err, "failed to create or update RBAC aggregation role")
	}

	if err = defaults.CreateDefaultNHC(mgr, ns, log); err != nil {
		return errors.Wrap(err, "failed to create a default NHC resource")
	}

	return nil
}
