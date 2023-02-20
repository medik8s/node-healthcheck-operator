package initializer

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/node-healthcheck-operator/controllers/console"
	"github.com/medik8s/node-healthcheck-operator/controllers/defaults"
	"github.com/medik8s/node-healthcheck-operator/controllers/rbac"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
)

// Initializer runs some bootstrapping code:
// - setup role aggregation
// - create default NHC
// - create console plugin
type initializer struct {
	cl     client.Client
	config *rest.Config
	logger logr.Logger
}

// New returns a new Initializer
func New(mgr ctrl.Manager, logger logr.Logger) *initializer {
	return &initializer{
		cl:     mgr.GetClient(),
		config: mgr.GetConfig(),
		logger: logger,
	}
}

// Start will start the Initializer
func (i *initializer) Start(ctx context.Context) error {

	ns, err := utils.GetDeploymentNamespace()
	if err != nil {
		return errors.Wrap(err, "unable to get the deployment namespace")
	}

	// TODO use give context
	if err = rbac.NewAggregation(i.cl, ns).CreateOrUpdateAggregation(); err != nil {
		return errors.Wrap(err, "failed to create or update RBAC aggregation role")
	}

	// TODO use give context
	if err = defaults.UpdateDefaultNHC(i.cl, ns, ctrl.Log.WithName("defaults")); err != nil {
		return errors.Wrap(err, "failed to update default NHC resource")
	}

	if err = console.CreateOrUpdatePlugin(ctx, i.cl, i.config, ns, ctrl.Log.WithName("console-plugin")); err != nil {
		return errors.Wrap(err, "failed to create or update the console plugin")
	}

	return nil
}
