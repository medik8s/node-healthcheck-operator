package initializer

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/node-healthcheck-operator/controllers/cluster"
	"github.com/medik8s/node-healthcheck-operator/controllers/console"
	"github.com/medik8s/node-healthcheck-operator/controllers/rbac"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
)

// Initializer runs some bootstrapping code:
// - setup role aggregation
// - create default NHC
// - create console plugin
type initializer struct {
	cl           client.Client
	capabilities cluster.Capabilities
	logger       logr.Logger
}

// New returns a new Initializer
func New(mgr ctrl.Manager, caps cluster.Capabilities, logger logr.Logger) *initializer {
	return &initializer{
		cl:           mgr.GetClient(),
		capabilities: caps,
		logger:       logger,
	}
}

// Start will start the Initializer
func (i *initializer) Start(ctx context.Context) error {

	ns, err := utils.GetDeploymentNamespace()
	if err != nil {
		return errors.Wrap(err, "unable to get the deployment namespace")
	}

	if err = rbac.NewAggregation(ctx, i.cl, ns).CreateOrUpdateAggregation(); err != nil {
		return errors.Wrap(err, "failed to create or update RBAC aggregation role")
	}

	if err = console.CreateOrUpdatePlugin(ctx, i.cl, i.capabilities, ns, ctrl.Log.WithName("console-plugin")); err != nil {
		return errors.Wrap(err, "failed to create or update the console plugin")
	}

	return nil
}
