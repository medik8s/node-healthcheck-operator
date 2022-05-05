package bootstrap

import (
	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/medik8s/node-healthcheck-operator/controllers/defaults"
	"github.com/medik8s/node-healthcheck-operator/controllers/rbac"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
)

// Initialize runs some bootstrapping code:
// - setup role aggregation
// - create default NHC
func Initialize(mgr ctrl.Manager, log logr.Logger) error {

	ns, err := utils.GetDeploymentNamespace()
	if err != nil {
		return errors.Wrap(err, "unable to get the deployment namespace")
	}

	if err = rbac.NewAggregation(mgr, ns).CreateOrUpdateAggregation(); err != nil {
		return errors.Wrap(err, "failed to create or update RBAC aggregation role")
	}

	if err = defaults.CreateDefaultNHC(mgr, ns, ctrl.Log.WithName("defaults")); err != nil {
		return errors.Wrap(err, "failed to create a default NHC resource")
	}

	return nil
}
