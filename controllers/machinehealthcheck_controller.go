package controllers

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/medik8s/node-healthcheck-operator/controllers/cluster"
	"github.com/medik8s/node-healthcheck-operator/controllers/mhc"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
	"github.com/openshift/api/machine/v1beta1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// MachineHealthCheckReconciler reconciles a MachineHealthCheck object
type MachineHealthCheckReconciler struct {
	client.Client
	Log                         logr.Logger
	Scheme                      *runtime.Scheme
	Recorder                    record.EventRecorder
	ClusterUpgradeStatusChecker cluster.UpgradeChecker
	MHCChecker                  mhc.Checker
}

// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get;list;watch
// +kubebuilder:rbac:groups=machine.openshift.io,resources=machinehealthchecks,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=machine.openshift.io,resources=machinehealthchecks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=machine.openshift.io,resources=machinehealthchecks/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *MachineHealthCheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("MachineHealthCheck", req.NamespacedName)

	// update MHCChecker status
	r.MHCChecker.UpdateStatus()

	// fetch mhc
	mhc := &v1beta1.MachineHealthCheck{}
	err := r.Get(ctx, req.NamespacedName, mhc)
	result := ctrl.Result{}
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("MachineHealthCheck not found", "name", req.Name, "namespace", req.Namespace)
			return result, nil
		}
		log.Error(err, "failed fetching MachineHealthCheck", "name", req.Name, "namespace", req.Namespace)
		return result, err
	}

	log.Info("reconciling MachineHealthCheck", "name", req.Name, "namespace", req.Namespace)

	return result, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MachineHealthCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.MachineHealthCheck{}).
		Watches(&source.Kind{Type: &v1.Node{}}, handler.EnqueueRequestsFromMapFunc(utils.MHCByNodeMapperFunc(mgr.GetClient(), mgr.GetLogger()))).
		Complete(r)
}
