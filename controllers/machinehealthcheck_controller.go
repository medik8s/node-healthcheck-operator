package controllers

import (
	"context"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/api/machine/v1beta1"

	"github.com/medik8s/node-healthcheck-operator/controllers/cluster"
	"github.com/medik8s/node-healthcheck-operator/controllers/mhc"
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

// +kubebuilder:rbac:groups=machine.openshift.io,resources=machinehealthchecks,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *MachineHealthCheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	//log := r.Log.WithValues("MachineHealthCheck", req.NamespacedName)

	// update MHCChecker status
	r.MHCChecker.UpdateStatus()
	result := ctrl.Result{}

	// fetch mhc
	//mhc := &v1beta1.MachineHealthCheck{}
	//err := r.Get(ctx, req.NamespacedName, mhc)
	//if err != nil {
	//	if apierrors.IsNotFound(err) {
	//		log.Info("MachineHealthCheck not found", "name", req.Name, "namespace", req.Namespace)
	//		return result, nil
	//	}
	//	log.Error(err, "failed fetching MachineHealthCheck", "name", req.Name, "namespace", req.Namespace)
	//	return result, err
	//}
	//
	//log.Info("reconciling MachineHealthCheck", "name", req.Name, "namespace", req.Namespace)

	return result, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MachineHealthCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.MachineHealthCheck{}).
		Complete(r)
}
