package controllers

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	commonevents "github.com/medik8s/common/pkg/events"
	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/api/machine/v1beta1"

	"github.com/medik8s/node-healthcheck-operator/controllers/cluster"
	"github.com/medik8s/node-healthcheck-operator/controllers/featuregates"
	"github.com/medik8s/node-healthcheck-operator/controllers/mhc"
	"github.com/medik8s/node-healthcheck-operator/controllers/resources"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils/annotations"
	"github.com/medik8s/node-healthcheck-operator/metrics"
)

const (
	defaultNodeStartupTimeout = 10 * time.Minute
)

var (
	// We allow users to disable the nodeStartupTimeout by setting the duration to 0.
	disabledNodeStartupTimeout = metav1.Duration{Duration: 0}
)

// MachineHealthCheckReconciler reconciles a MachineHealthCheck object
type MachineHealthCheckReconciler struct {
	client.Client
	Log                            logr.Logger
	Recorder                       record.EventRecorder
	ClusterUpgradeStatusChecker    cluster.UpgradeChecker
	MHCChecker                     mhc.Checker
	FeatureGateMHCControllerEvents <-chan event.GenericEvent
	FeatureGates                   featuregates.Accessor
	WatchManager                   resources.WatchManager
}

// SetupWithManager sets up the controller with the Manager.
func (r *MachineHealthCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	bldr := ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.MachineHealthCheck{})

	// create machine - node name index
	if err := mgr.GetCache().IndexField(context.TODO(),
		&v1beta1.Machine{},
		utils.MachineNodeNameIndex,
		indexMachineByNodeName,
	); err != nil {
		return fmt.Errorf("error setting index fields: %v", err)
	}

	bldr = bldr.Watches(
		&corev1.Node{},
		handler.EnqueueRequestsFromMapFunc(utils.MHCByNodeMapperFunc(mgr.GetClient(), mgr.GetLogger(), r.FeatureGates)),
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
		&v1beta1.Machine{},
		handler.EnqueueRequestsFromMapFunc(utils.MHCByMachineMapperFunc(mgr.GetClient(), mgr.GetLogger(), r.FeatureGates)),
	)
	bldr = bldr.WatchesRawSource(
		&source.Channel{Source: r.FeatureGateMHCControllerEvents},
		handler.EnqueueRequestsFromMapFunc(utils.MHCByFeatureGateEventMapperFunc(mgr.GetClient(), mgr.GetLogger(), r.FeatureGates)),
	)

	if controller, err := bldr.Build(r); err == nil {
		r.WatchManager.SetController(controller)
		return nil
	} else {
		return err
	}
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
// +kubebuilder:rbac:groups=machine.openshift.io,resources=machinehealthchecks/status,verbs=get;update;patch

// for the feature gate accessor
// +kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get;list;watch
// +kubebuilder:rbac:groups=config.openshift.io,resources=featuregates,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *MachineHealthCheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, returnErr error) {
	log := r.Log.WithValues("MachineHealthCheck name", req.Name)

	// update MHCChecker status
	if err := r.MHCChecker.UpdateStatus(ctx); err != nil {
		return result, err
	}

	// check if we need to reconcile MHCs
	if !r.FeatureGates.IsMachineAPIOperatorMHCDisabled() {
		return
	}

	log.Info("Reconciling MachineHealthCheck in NodeHealthCheck operator!")
	mhc := &v1beta1.MachineHealthCheck{}
	err := r.Get(ctx, req.NamespacedName, mhc)
	if err != nil {
		log.Error(err, "failed to get MachineHealthCheck")
		if apierrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// In the event that this was a deletion, we need to remove the associated metric label
			metrics.DeleteMachineHealthCheckNodesCovered(req.NamespacedName.Name, req.NamespacedName.Namespace)
			// We also need to revert short circuiting of such object so it doesn't overflow to a new object.
			metrics.ObserveMachineHealthCheckShortCircuitDisabled(req.NamespacedName.Name, req.NamespacedName.Namespace)
			return result, nil
		}
		return result, err
	}

	leaseHolderIdent := fmt.Sprintf("MachineHealthCheck-%s", mhc.GetName())
	leaseManager, err := resources.NewLeaseManager(r.Client, leaseHolderIdent, log)
	if err != nil {
		return result, err
	}
	resourceManager := resources.NewManager(r.Client, ctx, r.Log, true, leaseManager, r.Recorder)

	// always check if we need to patch status before we exit Reconcile
	mhcOrig := mhc.DeepCopy()
	defer func() {
		patchErr := patchStatus(ctx, *r, log, mhc, mhcOrig)
		if patchErr != nil {
			log.Error(err, "failed to update status")
		}
		returnErr = utilerrors.NewAggregate([]error{patchErr, returnErr})
	}()

	// Return early if the object is paused
	if annotations.HasMHCPausedAnnotation(mhc) {
		log.Info("Reconciliation is paused")
		return result, nil
	}

	// For now we only support MHCs with RemediationTemplate set
	if mhc.Spec.RemediationTemplate == nil {
		log.Error(fmt.Errorf("empty RemediationTemplate"), "NodeHealthCheck only supports MachineHealthChecks with a RemediationTemplate")
		// TODO event, status...?
		return result, nil
	}

	// TODO add more checks like in NHC?!
	if err = r.WatchManager.AddWatchesMhc(resourceManager, mhc); err != nil {
		return result, err
	}

	log.Info("Reconciling")

	// get targets
	targets, err := resourceManager.GetMHCTargets(mhc)
	if err != nil {
		log.Error(err, "failed to get targets")
		return result, err
	}
	totalTargets := len(targets)
	metrics.ObserveMachineHealthCheckNodesCovered(mhc.Name, mhc.Namespace, totalTargets)

	// health check all targets and reconcile mhc status
	healthy, needRemediation, requeueIn, errList := r.checkHealth(targets)

	healthyCount := 0
	for _, healthyTarget := range healthy {
		log.Info("handling healthy target", "target", healthyTarget.String())
		if remainingCRs, err := resourceManager.HandleHealthyNode(healthyTarget.Node.GetName(), healthyTarget.Machine.GetName(), mhc); err != nil {
			log.Error(err, "failed to handle healthy target", "target", healthyTarget.String())
			return result, err
		} else if len(remainingCRs) > 0 {
			result.Requeue = true
		}
		// TODO consider to align with NHC, where nodes count as healthy only when all CRs are actually gone (e.g. remediator removed finalizer)
		healthyCount++
	}

	mhc.Status.CurrentHealthy = &healthyCount
	mhc.Status.ExpectedMachines = &totalTargets
	unhealthyCount := totalTargets - healthyCount

	// check if we didn't exceed max unhealthy nodes
	maxUnhealthy, err := getMaxUnhealthy(mhc, log)
	if err != nil {
		log.Error(err, "failed to calculate max unhealthy allowed machines",
			"maxUnhealthy", mhc.Spec.MaxUnhealthy, "total targets", totalTargets)
		return result, err
	}
	mhc.Status.RemediationsAllowed = int32(getNrRemediationsAllowed(unhealthyCount, maxUnhealthy))

	if !isRemediationsAllowed(unhealthyCount, maxUnhealthy) {

		msg := fmt.Sprintf("Skipped remediation because the number of not started or unhealthy machines selected by the selector exceeds maxUnhealthy (total: %v, unhealthy: %v, maxUnhealthy: %v)",
			totalTargets, unhealthyCount, mhc.Spec.MaxUnhealthy)
		log.Info(msg)
		commonevents.WarningEvent(r.Recorder, mhc, utils.EventReasonRemediationSkipped, msg)
		metrics.ObserveMachineHealthCheckShortCircuitEnabled(mhc.Name, mhc.Namespace)

		// Remediation not allowed, the number of not started or unhealthy machines exceeds maxUnhealthy
		mhc.Status.RemediationsAllowed = 0
		utils.SetMachineCondition(mhc, &v1beta1.Condition{
			Type:     v1beta1.RemediationAllowedCondition,
			Status:   corev1.ConditionFalse,
			Severity: v1beta1.ConditionSeverityWarning,
			Reason:   v1beta1.TooManyUnhealthyReason,
			Message:  msg,
		})

		// no need to requeue if we don't have targets, or healthy nodes only
		if totalTargets == 0 || unhealthyCount == 0 {
			return reconcile.Result{}, nil
		}

		result.Requeue = true
		return result, nil
	}

	log.Info("Remediations are allowed", "total targets", totalTargets, "max unhealthy", mhc.Spec.MaxUnhealthy, "unhealthy targets", unhealthyCount)
	metrics.ObserveMachineHealthCheckShortCircuitDisabled(mhc.Name, mhc.Namespace)

	utils.SetMachineCondition(mhc, &v1beta1.Condition{
		Type:   v1beta1.RemediationAllowedCondition,
		Status: corev1.ConditionTrue,
	})

	remediationErrors := r.remediateAll(needRemediation, resourceManager)
	errList = append(errList, remediationErrors...)
	if len(errList) > 0 {
		requeueError := utilerrors.NewAggregate(errList)
		log.V(3).Info("Reconciling: there were errors, requeuing", "errors", requeueError)
		return result, requeueError
	}

	if requeueIn > 0 {
		log.V(3).Info("Reconciling: some targets might go unhealthy. Ensuring a requeue happens", "requeue in", requeueIn.String())
		if !result.Requeue {
			result.RequeueAfter = requeueIn
		}
		return result, nil
	}

	log.V(3).Info("Reconciling: no more targets meet unhealthy criteria")
	return result, nil
}

func (r *MachineHealthCheckReconciler) checkHealth(targets []resources.Target) (healthy []resources.Target, needRemediation []resources.Target, overallRequeueIn time.Duration, errList []error) {

	for _, t := range targets {
		log := r.Log.WithValues("target", t.String())
		log.Info("Reconciling: health checking")
		needsRemediation, requeueIn, err := r.needsRemediation(t)
		if err != nil {
			log.Error(err, "Reconciling: failed health checking")
			errList = append(errList, err)
			continue
		}

		if needsRemediation {
			needRemediation = append(needRemediation, t)
			continue
		}

		if requeueIn > 0 {
			log.Info("Reconciling: is likely to go unhealthy", "next check", requeueIn)
			nodeName := "unknown"
			if t.Node != nil {
				nodeName = t.Node.GetName()
			}
			commonevents.NormalEventf(r.Recorder, t.MHC, utils.EventReasonDetectedUnhealthy,
				"Machine %v has unhealthy node %v", t.Machine.GetName(), nodeName,
			)
			overallRequeueIn = *utils.MinRequeueDuration(&overallRequeueIn, &requeueIn)
			continue
		}

		healthy = append(healthy, t)
	}

	return
}

func (r *MachineHealthCheckReconciler) needsRemediation(t resources.Target) (bool, time.Duration, error) {
	now := time.Now()
	log := r.Log.WithValues("target", t.String())

	// machine has failed
	phase := pointer.StringDeref(t.Machine.Status.Phase, "")
	if phase == v1beta1.PhaseFailed {
		log.Info("machine is unhealthy", "phase", phase)
		return true, time.Duration(0), nil
	}

	// the node has not been set yet
	if t.Node == nil {
		nodeStartupTimeout := getNodeStartupTimeout(t.MHC)
		if nodeStartupTimeout.Seconds() == disabledNodeStartupTimeout.Seconds() {
			// Startup timeout is disabled so no need to go any further.
			// No node yet to check conditions, can return early here.
			return false, 0, nil
		}

		// status not updated yet
		if t.Machine.Status.LastUpdated == nil {
			return false, nodeStartupTimeout, nil
		}
		if t.Machine.Status.LastUpdated.Add(nodeStartupTimeout).Before(now) {
			log.V(3).Info("machine is unhealthy, it has no node after timeout", "nodeStartupTimeout", nodeStartupTimeout.String())
			return true, time.Duration(0), nil
		}
		durationUnhealthy := now.Sub(t.Machine.Status.LastUpdated.Time)
		requeueIn := nodeStartupTimeout - durationUnhealthy + time.Second
		return false, requeueIn, nil
	}

	// the node does not exist
	if t.Node != nil && t.Node.UID == "" {
		return true, time.Duration(0), nil
	}

	// check node conditions
	// diverting from MHC code here and reusing NHC code
	healthy, requeueIn := utils.IsHealthyMHC(t.MHC.Spec.UnhealthyConditions, t.Node.Status.Conditions, currentTime())
	if !healthy {
		log.Info("node is unhealthy")
		return true, time.Duration(0), nil
	}
	if requeueIn != nil {
		return false, *requeueIn, nil
	}
	return false, time.Duration(0), nil
}

func getNodeStartupTimeout(mhc *v1beta1.MachineHealthCheck) time.Duration {
	if mhc.Spec.NodeStartupTimeout != nil {
		return mhc.Spec.NodeStartupTimeout.Duration
	}
	return defaultNodeStartupTimeout
}

func (r *MachineHealthCheckReconciler) remediateAll(targets []resources.Target, rm resources.Manager) []error {
	var errList []error
	for _, t := range targets {
		r.Log.Info("Reconciling: meet unhealthy criteria, triggers remediation", "target", t.String())
		if err := r.remediate(t, rm); err != nil {
			r.Log.Error(err, "Reconciling: error external remediating", "target", t.String())
			errList = append(errList, err)
		}
	}
	return errList
}

func (r *MachineHealthCheckReconciler) remediate(target resources.Target, rm resources.Manager) error {

	// ignore machines with deletion timestamp
	if target.Machine.DeletionTimestamp != nil {
		return nil
	}

	// diverting from original MHC code here, because we can reuse NHC code for creating the remediation CR

	// TODO add control plane max remediation check!?

	// generate remediation CR
	template, err := rm.GetTemplate(target.MHC)
	if err != nil {
		return errors.Wrapf(err, "failed to get remediation template")
	}

	var nodeNamePtr *string
	if target.Node != nil && target.Node.ResourceVersion != "" {
		nodeNamePtr = &target.Node.Name
	}

	// TODO add control plane label

	// create remediation CR
	remediationCR, err := rm.GenerateRemediationCRForMachine(target.Machine, target.MHC, template, pointer.StringDeref(nodeNamePtr, ""))
	if err != nil {
		return errors.Wrapf(err, "failed to generate remediation CR")
	}

	created, _, _, err := rm.CreateRemediationCR(remediationCR, target.MHC, nodeNamePtr, utils.DefaultRemediationDuration, 0)
	if err != nil {
		if _, ok := err.(resources.RemediationCRNotOwned); ok {
			// CR exists but not owned by us, nothing to do
			return nil
		}
		return errors.Wrapf(err, "failed to create remediation CR")
	}
	if created {
		commonevents.NormalEventf(r.Recorder, target.MHC, utils.EventReasonRemediationCreated,
			"Created remediation object for machine %s with node %s", target.Machine.GetName(), target.Node.GetName())
	}
	return nil
}

func getMaxUnhealthy(mhc *v1beta1.MachineHealthCheck, log logr.Logger) (int, error) {
	total := pointer.IntDeref(mhc.Status.ExpectedMachines, 0)
	if mhc.Spec.MaxUnhealthy == nil {
		// This value should be defaulted, but if not, 100% is the default
		return total, nil
	}
	if maxUnhealthy, err := getValueFromIntOrPercent(mhc.Spec.MaxUnhealthy, total, false); err != nil {
		log.Error(err, "failed to calculate max unhealthy allowed machines",
			"maxUnhealthy", mhc.Spec.MaxUnhealthy, "total targets", total)
		return 0, err
	} else if maxUnhealthy < 0 {
		return 0, nil
	} else {
		return maxUnhealthy, nil
	}
}

// getValueFromIntOrPercent returns the integer number value based on the
// percentage of the total or absolute number dependent on the IntOrString given
//
// The following code is copied from https://github.com/kubernetes/apimachinery/blob/1a505bc60c6dfb15cb18a8cdbfa01db042156fe2/pkg/util/intstr/intstr.go#L154-L185
// But fixed so that string values aren't always assumed to be percentages
// See https://github.com/kubernetes/kubernetes/issues/89082 for details
func getValueFromIntOrPercent(intOrPercent *intstr.IntOrString, total int, roundUp bool) (int, error) {
	if intOrPercent == nil {
		return 0, errors.New("nil value for IntOrString")
	}
	value, isPercent, err := getIntOrPercentValue(intOrPercent)
	if err != nil {
		return 0, fmt.Errorf("invalid value for IntOrString: %v", err)
	}
	if isPercent {
		if roundUp {
			value = int(math.Ceil(float64(value) * (float64(total)) / 100))
		} else {
			value = int(math.Floor(float64(value) * (float64(total)) / 100))
		}
	}
	return value, nil
}

// getIntOrPercentValue returns the integer value of the IntOrString and
// determines if this value is a percentage or absolute number
//
// The following code is copied from https://github.com/kubernetes/apimachinery/blob/1a505bc60c6dfb15cb18a8cdbfa01db042156fe2/pkg/util/intstr/intstr.go#L154-L185
// But fixed so that string values aren't always assumed to be percentages
// See https://github.com/kubernetes/kubernetes/issues/89082 for details
func getIntOrPercentValue(intOrStr *intstr.IntOrString) (int, bool, error) {
	switch intOrStr.Type {
	case intstr.Int:
		return intOrStr.IntValue(), false, nil
	case intstr.String:
		isPercent := false
		s := intOrStr.StrVal
		if strings.HasSuffix(s, "%") {
			isPercent = true
			s = strings.TrimSuffix(intOrStr.StrVal, "%")
		}
		v, err := strconv.Atoi(s)
		if err != nil {
			return 0, isPercent, fmt.Errorf("invalid value %q: %v", intOrStr.StrVal, err)
		}
		return v, isPercent, nil
	}
	return 0, false, fmt.Errorf("invalid type: neither int nor percentage")
}

func getNrRemediationsAllowed(unhealthy, maxUnhealthy int) int {
	allowed := maxUnhealthy - unhealthy
	if allowed < 0 {
		return 0
	}
	return allowed
}

func isRemediationsAllowed(unhealthy, maxUnhealthy int) bool {
	// getNrRemediationsAllowed returns 0 also when we have more unhealthy machines than allowed.
	// since 0 is the corner case where remediation is just still allowed, we can't use it here
	allowed := maxUnhealthy - unhealthy
	return allowed >= 0
}
