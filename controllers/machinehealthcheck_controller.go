package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/api/machine/v1beta1"

	"github.com/medik8s/node-healthcheck-operator/controllers/cluster"
	"github.com/medik8s/node-healthcheck-operator/controllers/mhc"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
)

// MachineHealthCheckReconciler reconciles a MachineHealthCheck object
type MachineHealthCheckReconciler struct {
	client.Client
	Log                         logr.Logger
	Recorder                    record.EventRecorder
	ClusterUpgradeStatusChecker cluster.UpgradeChecker
	MHCChecker                  mhc.Checker
	ReconcileMHC                bool
}

// SetupWithManager sets up the controller with the Manager.
func (r *MachineHealthCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	bldr := ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.MachineHealthCheck{})

	if r.ReconcileMHC {
		// create machine - node name index
		if err := mgr.GetCache().IndexField(context.TODO(),
			&v1beta1.Machine{},
			utils.MachineNodeNameIndex,
			indexMachineByNodeName,
		); err != nil {
			return fmt.Errorf("error setting index fields: %v", err)
		}

		bldr = bldr.Watches(
			&source.Kind{Type: &v1.Node{}},
			handler.EnqueueRequestsFromMapFunc(utils.MHCByNodeMapperFunc(mgr.GetClient(), mgr.GetLogger())),
			builder.WithPredicates(
				predicate.Funcs{
					// check for modified conditions on updates in order to prevent unneeded reconciliations
					UpdateFunc: func(ev event.UpdateEvent) bool { return nodeUpdateNeedsReconcile(ev) },
					// MHC reconciler is interested in deleted nodes... not sure why TBH?
					DeleteFunc: func(_ event.DeleteEvent) bool { return true },
					// create (new nodes don't have correct conditions yet), and generic events are not interesting for now
					CreateFunc:  func(_ event.CreateEvent) bool { return false },
					GenericFunc: func(_ event.GenericEvent) bool { return false },
				},
			),
		)
		bldr = bldr.Watches(
			&source.Kind{Type: &v1beta1.Machine{}},
			handler.EnqueueRequestsFromMapFunc(utils.MHCByMachineMapperFunc(mgr.GetClient(), mgr.GetLogger())),
		)
	}

	return bldr.Complete(r)
}

func indexMachineByNodeName(object client.Object) []string {
	machine, ok := object.(*v1beta1.Machine)
	if !ok {
		msg := fmt.Sprintf("Expected a machine for indexing field, got: %T", object)
		ctrl.Log.WithName("machine indexer").Info(msg)
		return nil
	}

	if machine.Status.NodeRef != nil {
		return []string{machine.Status.NodeRef.Name}
	}

	return nil
}

// +kubebuilder:rbac:groups=machine.openshift.io,resources=machines,verbs=get;list;watch
// +kubebuilder:rbac:groups=machine.openshift.io,resources=machinehealthchecks,verbs=get;list;watch;patch;update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *MachineHealthCheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, returnErr error) {
	log := r.Log.WithValues("MachineHealthCheck name", req.Name)

	// get mhc
	mhc := &v1beta1.MachineHealthCheck{}
	err := r.Get(ctx, req.NamespacedName, mhc)
	if err != nil {
		log.Error(err, "failed getting Machine Health Check")
		if apierrors.IsNotFound(err) {
			return result, nil
		}
		return result, err
	}

	// update MHCChecker status
	if err = r.MHCChecker.UpdateStatus(); err != nil {
		return result, err
	}

	//resourceManager := resources.NewManager(r.Client, ctx, r.log, r.onOpenShift)

	// always check if we need to patch status before we exit Reconcile
	mhcOrig := mhc.DeepCopy()
	defer func() {
		patchErr := patchStatus(ctx, *r, log, mhc, mhcOrig)
		if patchErr != nil {
			log.Error(err, "failed to update status")
			// check if we have an error from the rest of the code already
			if returnErr != nil {
				returnErr = utilerrors.NewAggregate([]error{patchErr, returnErr})
			} else {
				returnErr = patchErr
			}
		}
	}()

	return result, nil
}
