package bootstrap

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/medik8s/node-healthcheck-operator/controllers/console"
	"github.com/medik8s/node-healthcheck-operator/controllers/defaults"
	"github.com/medik8s/node-healthcheck-operator/controllers/rbac"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
)

// Initialize runs some bootstrapping code:
// - setup role aggregation
// - create default NHC
// - create console plugin
func Initialize(ctx context.Context, mgr ctrl.Manager, log logr.Logger) error {

	ns, err := utils.GetDeploymentNamespace()
	if err != nil {
		return errors.Wrap(err, "unable to get the deployment namespace")
	}

	// TODO use give context
	if err = rbac.NewAggregation(mgr, ns).CreateOrUpdateAggregation(); err != nil {
		return errors.Wrap(err, "failed to create or update RBAC aggregation role")
	}

	// TODO use give context
	if err = defaults.CreateOrUpdateDefaultNHC(mgr, ns, ctrl.Log.WithName("defaults")); err != nil {
		return errors.Wrap(err, "failed to create or update a default NHC resource")
	}

	if err = console.CreateOrUpdateConsolePlugin(ctx, mgr, ns, ctrl.Log.WithName("console-plugin")); err != nil {
		return errors.Wrap(err, "failed to create or update the console plugin")
	}

	return nil
}
