/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/cluster"
	"github.com/medik8s/node-healthcheck-operator/controllers/mhc"
	"github.com/medik8s/node-healthcheck-operator/controllers/resources"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
	"github.com/medik8s/node-healthcheck-operator/metrics"
)

const (
	oldRemediationCRAnnotationKey    = "nodehealthcheck.medik8s.io/old-remediation-cr-flag"
	remediationTimedOutAnnotationkey = "remediation.medik8s.io/nhc-timed-out"
	remediationCRAlertTimeout        = time.Hour * 48
	eventReasonRemediationCreated    = "RemediationCreated"
	eventReasonRemediationSkipped    = "RemediationSkipped"
	eventReasonRemediationRemoved    = "RemediationRemoved"
	eventReasonNoTemplateLeft        = "NoTemplateLeft"
	eventReasonDisabled              = "Disabled"
	eventReasonEnabled               = "Enabled"
	eventTypeNormal                  = "Normal"
	eventTypeWarning                 = "Warning"
	enabledMessage                   = "No issues found, NodeHealthCheck is enabled."
	conditionTypeSucceeded           = "Succeeded"

	// RemediationControlPlaneLabelKey is the label key to put on remediation CRs for control plane nodes
	RemediationControlPlaneLabelKey = "remediation.medik8s.io/isControlPlaneNode"
)

var (
	clusterUpgradeRequeueAfter = 1 * time.Minute
	currentTime                = func() time.Time { return time.Now() }
)

// NodeHealthCheckReconciler reconciles a NodeHealthCheck object
type NodeHealthCheckReconciler struct {
	client.Client
	Log                         logr.Logger
	Scheme                      *runtime.Scheme
	Recorder                    record.EventRecorder
	ClusterUpgradeStatusChecker cluster.UpgradeChecker
	MHCChecker                  mhc.Checker
	OnOpenShift                 bool
	ctrl                        controller.Controller
	watches                     map[string]struct{}
	watchesLock                 sync.Mutex
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeHealthCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctrl, err := ctrl.NewControllerManagedBy(mgr).
		For(&remediationv1alpha1.NodeHealthCheck{}).
		Watches(
			&source.Kind{Type: &v1.Node{}},
			handler.EnqueueRequestsFromMapFunc(utils.NHCByNodeMapperFunc(mgr.GetClient(), mgr.GetLogger())),
			builder.WithPredicates(
				predicate.Funcs{
					// check for modified conditions on updates in order to prevent unneeded reconciliations
					UpdateFunc: func(ev event.UpdateEvent) bool { return nodeUpdateNeedsReconcile(ev) },
					// create (new nodes don't have correct conditions yet), delete and generic events are not interesting for now
					CreateFunc:  func(_ event.CreateEvent) bool { return false },
					DeleteFunc:  func(_ event.DeleteEvent) bool { return false },
					GenericFunc: func(_ event.GenericEvent) bool { return false },
				},
			),
		).
		Build(r)

	if err != nil {
		return err
	}
	r.ctrl = ctrl
	r.watches = make(map[string]struct{})
	return nil
}

func nodeUpdateNeedsReconcile(ev event.UpdateEvent) bool {
	var oldNode *v1.Node
	var newNode *v1.Node
	var ok bool
	if oldNode, ok = ev.ObjectOld.(*v1.Node); !ok {
		return false
	}
	if newNode, ok = ev.ObjectNew.(*v1.Node); !ok {
		return false
	}
	needsReconcile := conditionsNeedReconcile(oldNode.Status.Conditions, newNode.Status.Conditions)
	//ctrl.Log.Info("NODE UPDATE", "node", newNode.GetName(), "needs reconcile", needsReconcile)
	return needsReconcile
}

func conditionsNeedReconcile(oldConditions, newConditions []v1.NodeCondition) bool {
	// Check if the Ready condition exists on the new node.
	// If not, the node was just created and hasn't updated its status yet
	readyConditionFound := false
	for _, cond := range newConditions {
		if cond.Type == v1.NodeReady {
			readyConditionFound = true
			break
		}
	}
	if !readyConditionFound {
		return false
	}

	// Check if conditions changed
	if len(oldConditions) != len(newConditions) {
		return true
	}
	for _, condOld := range oldConditions {
		conditionFound := false
		for _, condNew := range newConditions {
			if condOld.Type == condNew.Type {
				if condOld.Status != condNew.Status {
					return true
				}
				conditionFound = true
			}
		}
		if !conditionFound {
			return true
		}
	}
	return false
}

// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=remediation.medik8s.io,resources=nodehealthchecks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=remediation.medik8s.io,resources=nodehealthchecks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=remediation.medik8s.io,resources=nodehealthchecks/finalizers,verbs=update
// +kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get;list;watch
// +kubebuilder:rbac:groups=machine.openshift.io,resources=machines,verbs=get;list;watch
// +kubebuilder:rbac:groups=machine.openshift.io,resources=machinehealthchecks,verbs=get;list;watch
// +kubebuilder:rbac:groups="coordination.k8s.io",resources=leases,verbs=get;list;update;patch;watch;create;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;create

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *NodeHealthCheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, returnErr error) {
	log := r.Log.WithValues("NodeHealthCheck name", req.Name)
	log.Info("reconciling")
	// get nhc
	nhc := &remediationv1alpha1.NodeHealthCheck{}
	err := r.Get(ctx, req.NamespacedName, nhc)
	result = ctrl.Result{}
	if err != nil {
		log.Error(err, "failed getting Node Health Check", "object", nhc)
		if apierrors.IsNotFound(err) {
			return result, nil
		}
		return result, err
	}

	leaseManager, err := resources.NewLeaseManager(r.Client, log)
	if err != nil {
		return ctrl.Result{}, err
	}
	resourceManager := resources.NewManager(r.Client, ctx, r.Log, r.OnOpenShift, leaseManager)

	// always check if we need to patch status before we exit Reconcile
	nhcOrig := nhc.DeepCopy()
	var finalRequeueAfter *time.Duration
	defer func() {
		finalRequeueAfter = utils.MinRequeueDuration(&result.RequeueAfter, finalRequeueAfter)
		if finalRequeueAfter != nil {
			result.RequeueAfter = *finalRequeueAfter
		}
		patchErr := r.patchStatus(nhc, nhcOrig)
		if patchErr != nil {
			log.Error(err, "failed to update status")
			// check if we have an error from the rest of the code already
			if returnErr != nil {
				returnErr = utilerrors.NewAggregate([]error{patchErr, returnErr})
			} else {
				returnErr = patchErr
			}
		}
		log.Info("reconcile end", "error", returnErr, "requeue", result.Requeue, "requeuAfter", result.RequeueAfter)
	}()

	defer func() {
		var leaseRequeue time.Duration
		leaseRequeue, returnErr = leaseManager.UpdateReconcileResults(ctx, nhc, result.RequeueAfter, returnErr)
		finalRequeueAfter = utils.MinRequeueDuration(&leaseRequeue, finalRequeueAfter)
	}()

	// set counters to zero for disabled NHC
	nhc.Status.ObservedNodes = pointer.Int(0)
	nhc.Status.HealthyNodes = pointer.Int(0)

	// check if we need to disable NHC because of existing MHCs
	if disable := r.MHCChecker.NeedDisableNHC(); disable {
		// update status if needed
		if !utils.IsConditionTrue(nhc.Status.Conditions, remediationv1alpha1.ConditionTypeDisabled, remediationv1alpha1.ConditionReasonDisabledMHC) {
			log.Info("disabling NHC in order to avoid conflict with custom MHCs configured in the cluster")
			meta.SetStatusCondition(&nhc.Status.Conditions, metav1.Condition{
				Type:    remediationv1alpha1.ConditionTypeDisabled,
				Status:  metav1.ConditionTrue,
				Reason:  remediationv1alpha1.ConditionReasonDisabledMHC,
				Message: "Custom MachineHealthCheck(s) detected, disabling NodeHealthCheck to avoid conflicts",
			})
			r.Recorder.Eventf(nhc, eventTypeWarning, eventReasonDisabled, "Custom MachineHealthCheck(s) detected, disabling NodeHealthCheck to avoid conflicts")
		}
		// stop reconciling
		// TODO either requeue here, or trigger reconcile in mhc.Checker.UpdateStatus() for quicker NHC status updates
		return result, nil
	}

	// check if we need to disable NHC because of missing or misconfigured template CRs
	if valid, reason, message, err := resourceManager.ValidateTemplates(nhc); err != nil {
		log.Error(err, "failed to validate template")
		return result, err
	} else if !valid {
		if !utils.IsConditionTrue(nhc.Status.Conditions, remediationv1alpha1.ConditionTypeDisabled, reason) {
			log.Info("disabling NHC", "reason", reason, "message", message)
			meta.SetStatusCondition(&nhc.Status.Conditions, metav1.Condition{
				Type:    remediationv1alpha1.ConditionTypeDisabled,
				Status:  metav1.ConditionTrue,
				Reason:  reason,
				Message: message,
			})
			r.Recorder.Eventf(nhc, eventTypeWarning, eventReasonDisabled, "Disabling NHC. Reason: %s, Message: %s", reason, message)
		}
		if reason == remediationv1alpha1.ConditionReasonDisabledTemplateNotFound {
			// requeue for checking back if template exists later
			result.RequeueAfter = 15 * time.Second
		}
		return result, nil
	}

	// all checks passed, update status if needed
	if !meta.IsStatusConditionFalse(nhc.Status.Conditions, remediationv1alpha1.ConditionTypeDisabled) {
		log.Info("enabling NHC, valid config, no conflicting MHC configured in the cluster")
		meta.SetStatusCondition(&nhc.Status.Conditions, metav1.Condition{
			Type:    remediationv1alpha1.ConditionTypeDisabled,
			Status:  metav1.ConditionFalse,
			Reason:  remediationv1alpha1.ConditionReasonEnabled,
			Message: enabledMessage,
		})
		r.Recorder.Eventf(nhc, eventTypeNormal, eventReasonEnabled, enabledMessage)
	}

	// select nodes using the nhc.selector
	nodes, err := resourceManager.GetNodes(nhc.Spec.Selector)
	if err != nil {
		return result, err
	}
	nhc.Status.ObservedNodes = pointer.Int(len(nodes))

	// check nodes health
	healthyNodes, unhealthyNodes, requeueAfter := r.checkNodesHealth(nodes, nhc)
	finalRequeueAfter = utils.MinRequeueDuration(finalRequeueAfter, requeueAfter)
	nhc.Status.HealthyNodes = pointer.Int(len(healthyNodes))

	// TODO consider setting Disabled condition?
	if r.isClusterUpgrading() {
		msg := "Postponing potential remediations because of ongoing cluster upgrade"
		log.Info(msg)
		r.Recorder.Event(nhc, eventTypeNormal, eventReasonRemediationSkipped, msg)
		result.RequeueAfter = clusterUpgradeRequeueAfter
		return result, nil
	}

	if len(nhc.Spec.PauseRequests) > 0 {
		// some actors want to pause remediation.
		msg := "Postponing potential remediations because of pause requests"
		log.Info(msg)
		r.Recorder.Event(nhc, eventTypeNormal, eventReasonRemediationSkipped, msg)
		return result, nil
	}

	// delete remediation CRs for healthy nodes
	for _, node := range healthyNodes {
		log.Info("handling healthy node", "node", node.GetName())
		remediationCRs, err := resourceManager.ListRemediationCRs(nhc, func(cr unstructured.Unstructured) bool {
			return cr.GetName() == node.GetName()
		})
		if err != nil {
			log.Error(err, "failed to get remediation CRs for healthy node", "node", node.Name)
			return result, err
		}
		for _, remediationCR := range remediationCRs {
			if deleted, err := resourceManager.DeleteRemediationCR(&remediationCR, nhc); err != nil {
				log.Error(err, "failed to delete remediation CR for healthy node", "node", node.Name)
				return result, err
			} else if deleted {
				log.Info("deleted remediation CR", "name", remediationCR.GetName())
				r.Recorder.Eventf(nhc, eventTypeNormal, eventReasonRemediationRemoved, "Deleted remediation CR for node %s", remediationCR.GetName())
			}

			// always update status, in case patching it failed during last reconcile
			resources.UpdateStatusNodeHealthy(&node, nhc)
		}
	}

	// we are done in case we don't have unhealthy nodes
	if len(unhealthyNodes) == 0 {
		return result, nil
	}

	// check if we have enough healthy nodes
	if minHealthy, err := intstr.GetScaledValueFromIntOrPercent(nhc.Spec.MinHealthy, len(nodes), true); err != nil {
		log.Error(err, "failed to calculate min healthy allowed nodes",
			"minHealthy", nhc.Spec.MinHealthy, "observedNodes", nhc.Status.ObservedNodes)
		return result, err
	} else if len(healthyNodes) < minHealthy {
		msg := fmt.Sprintf("Skipped remediation because the number of healthy nodes selected by the selector is %d and should equal or exceed %d", len(healthyNodes), minHealthy)
		log.Info(msg)
		r.Recorder.Event(nhc, eventTypeWarning, eventReasonRemediationSkipped, msg)
		return
	}

	// remediate unhealthy nodes
	for _, node := range unhealthyNodes {
		log.Info("handling unhealthy node", "node", node.GetName())
		requeueAfter, err := r.remediate(&node, nhc, resourceManager)
		if err != nil {
			// don't try to remediate other nodes
			log.Error(err, "failed to start remediation")
			return result, err
		}
		finalRequeueAfter = utils.MinRequeueDuration(finalRequeueAfter, requeueAfter)

		// check if we need to alert about a very old remediation CR
		remediationCRs, err := resourceManager.ListRemediationCRs(nhc, func(cr unstructured.Unstructured) bool {
			return cr.GetName() == node.GetName()
		})
		for _, remediationCR := range remediationCRs {
			isAlert, requeueAfter := r.alertOldRemediationCR(&remediationCR)
			if isAlert {
				metrics.ObserveNodeHealthCheckOldRemediationCR(node.Name, node.Namespace)
			}
			finalRequeueAfter = utils.MinRequeueDuration(finalRequeueAfter, requeueAfter)
		}
	}

	return result, nil
}

func (r *NodeHealthCheckReconciler) isClusterUpgrading() bool {
	clusterUpgrading, err := r.ClusterUpgradeStatusChecker.Check()
	if err != nil {
		// if we can't reliably tell if the cluster is upgrading then just continue with remediation.
		// TODO finer error handling may help to decide otherwise here.
		r.Log.Error(err, "failed to check if the cluster is upgrading. Proceed with remediation as if it is not upgrading")
	}
	return clusterUpgrading
}

func (r *NodeHealthCheckReconciler) checkNodesHealth(nodes []v1.Node, nhc *remediationv1alpha1.NodeHealthCheck) (healthy []v1.Node, unhealthy []v1.Node, requeueAfter *time.Duration) {
	for _, node := range nodes {
		if isHealthy, thisRequeueAfter := r.isHealthy(nhc.Spec.UnhealthyConditions, node.Status.Conditions); isHealthy {
			healthy = append(healthy, node)
			requeueAfter = utils.MinRequeueDuration(requeueAfter, thisRequeueAfter)
		} else if r.MHCChecker.NeedIgnoreNode(&node) {
			// consider terminating nodes being handled by MHC as healthy, from NHC point of view
			healthy = append(healthy, node)
		} else {
			unhealthy = append(unhealthy, node)
		}
	}
	return
}

func (r *NodeHealthCheckReconciler) isHealthy(conditionTests []remediationv1alpha1.UnhealthyCondition, nodeConditions []v1.NodeCondition) (bool, *time.Duration) {
	nodeConditionByType := make(map[v1.NodeConditionType]v1.NodeCondition)
	for _, nc := range nodeConditions {
		nodeConditionByType[nc.Type] = nc
	}

	for _, c := range conditionTests {
		n, exists := nodeConditionByType[c.Type]
		if !exists {
			continue
		}
		if n.Status == c.Status {
			now := currentTime()
			if now.After(n.LastTransitionTime.Add(c.Duration.Duration)) {
				// unhealthy condition duration expired, node is unhealthy
				return false, nil
			} else {
				// unhealthy condition duration not expired yet, node is healthy. Requeue when duration expires
				expiresAfter := n.LastTransitionTime.Add(c.Duration.Duration).Sub(now) + 1*time.Second
				return true, &expiresAfter
			}
		}
	}
	return true, nil
}

func (r *NodeHealthCheckReconciler) remediate(node *v1.Node, nhc *remediationv1alpha1.NodeHealthCheck, rm resources.Manager) (*time.Duration, error) {

	log := utils.GetLogWithNHC(r.Log, nhc)

	// prevent remediation of more than 1 control plane node at a time!
	isControlPlaneNode := utils.IsControlPlane(node)
	if isControlPlaneNode {
		if isAllowed, err := r.isControlPlaneRemediationAllowed(node, nhc, rm); err != nil {
			return nil, errors.Wrapf(err, "failed to check if control plane remediation is allowed")
		} else if !isAllowed {
			log.Info("skipping remediation for preventing control plane quorum loss", "node", node.GetName())
			r.Recorder.Event(nhc, eventTypeWarning, eventReasonRemediationSkipped, fmt.Sprintf("skipping remediation of %s for preventing control plane quorum loss", node.GetName()))
			return nil, nil
		}
	}

	// generate remediation CR
	currentTemplate, timeout, err := rm.GetCurrentTemplateWithTimeout(node, nhc)
	if err != nil {
		if _, ok := err.(resources.NoTemplateLeftError); ok {
			log.Error(err, "Remediation timed out, and no template left to try")
			r.Recorder.Event(nhc, eventTypeWarning, eventReasonNoTemplateLeft, fmt.Sprintf("Remediation timed out, and no template left to try. %s", err.Error()))
			// there is nothing we can do about this
			return nil, nil
		}
		return nil, errors.Wrapf(err, "failed to get current template")
	}
	remediationCR, err := rm.GenerateRemediationCR(node, nhc, currentTemplate)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to generate remediation CR")
	}

	if isControlPlaneNode {
		labels := remediationCR.GetLabels()
		labels[RemediationControlPlaneLabelKey] = ""
		remediationCR.SetLabels(labels)
	}

	// create remediation CR
	created, leaseRequeueTimeout, err := rm.CreateRemediationCR(remediationCR, nhc)
	if err != nil {
		if _, ok := err.(resources.RemediationCRNotOwned); ok {
			// CR exists but not owned by us, nothing to do
			return nil, nil
		}
		return nil, errors.Wrapf(err, "failed to create remediation CR")
	} else {
		isLeaseObtained := leaseRequeueTimeout != nil && created
		isCrAlreadyExist := !created && leaseRequeueTimeout == nil
		if !isLeaseObtained && !isCrAlreadyExist {
			resources.UpdateStatusNodeUnhealthy(node, nhc)
			return leaseRequeueTimeout, nil
		}
	}

	// always update status, in case patching it failed during last reconcile
	resources.UpdateStatusRemediationStarted(node, nhc, remediationCR)

	if created {
		r.Recorder.Event(nhc, eventTypeNormal, eventReasonRemediationCreated, fmt.Sprintf("Created remediation object for node %s", node.Name))
		var requeueIn *time.Duration
		if timeout != nil {
			// come back when timeout expires
			requeueIn = pointer.Duration(*timeout + 1*time.Second)
		}
		return utils.MinRequeueDuration(leaseRequeueTimeout, requeueIn), nil
	}
	// CR already exists, check for timeout in case we need to
	if timeout == nil {
		// no timeout set for classic remediation
		// nothing to do anymore here
		return leaseRequeueTimeout, nil
	}

	// Having a timeout also means we are using escalating remediations, for which we need to look at the "Succeeded"
	// condition, which can accelerate switching to the next remediator.
	// So let's start watching the CRs, so we don't need to poll.
	if err = r.addWatch(remediationCR); err != nil {
		return nil, errors.Wrapf(err, "failed to add watch for %s", remediationCR.GroupVersionKind().String())
	}

	startedRemediation := resources.FindStatusRemediation(node, nhc, func(r *remediationv1alpha1.Remediation) bool {
		return r.Resource.GroupVersionKind() == remediationCR.GroupVersionKind()
	})

	if startedRemediation == nil {
		// should not have happened, seems last status update failed
		// retry asap
		return pointer.Duration(1 * time.Second), nil
	}

	if startedRemediation.TimedOut != nil {
		// timeout handled already: should not have happened, but ok. Just reconcile again asap for trying the next template
		return pointer.Duration(1 * time.Second), nil
	}

	now := metav1.Time{Time: currentTime()}
	timeoutAt := getTimeoutAt(startedRemediation, timeout)
	timedOut := now.After(timeoutAt)

	failed := remediationFailed(remediationCR, log)

	if !timedOut && !failed {
		// not timed out yet, come back when we do so
		return utils.MinRequeueDuration(leaseRequeueTimeout, pointer.Duration(timeoutAt.Sub(now.Time))), nil
	}

	// handle timeout and failure
	if timedOut {
		log.Info("remediation timed out")
	} else if failed {
		log.Info("remediation failed")
	}

	// add timeout annotation to remediation CR
	annotations := remediationCR.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 1)
	}
	annotations[remediationTimedOutAnnotationkey] = now.Format(time.RFC3339)
	remediationCR.SetAnnotations(annotations)
	if err = rm.UpdateRemediationCR(remediationCR); err != nil {
		return nil, errors.Wrapf(err, "failed to update remediation CR with timeout annotation")
	}

	// update status (important to do this after CR update, else we won't retry that update in case of error)
	startedRemediation.TimedOut = &now

	// try next remediation asap
	return pointer.Duration(1 * time.Second), nil
}

func (r *NodeHealthCheckReconciler) isControlPlaneRemediationAllowed(node *v1.Node, nhc *remediationv1alpha1.NodeHealthCheck, rm resources.Manager) (bool, error) {
	if !utils.IsControlPlane(node) {
		return true, fmt.Errorf("%s isn't a control plane node", node.GetName())
	}

	// check all remediation CRs. If there already is one for another control plane node, skip remediation
	controlPlaneRemediationCRs, err := rm.ListRemediationCRs(nhc, func(cr unstructured.Unstructured) bool {
		_, isControlPlane := cr.GetLabels()[RemediationControlPlaneLabelKey]
		return isControlPlane && cr.GetName() != node.GetName()
	})
	if err != nil {
		return false, err
	}
	return len(controlPlaneRemediationCRs) == 0, nil
}

func (r *NodeHealthCheckReconciler) patchStatus(nhc, nhcOrig *remediationv1alpha1.NodeHealthCheck) error {

	log := utils.GetLogWithNHC(r.Log, nhc)

	// calculate phase and reason
	disabledCondition := meta.FindStatusCondition(nhc.Status.Conditions, remediationv1alpha1.ConditionTypeDisabled)
	if disabledCondition != nil && disabledCondition.Status == metav1.ConditionTrue {
		nhc.Status.Phase = remediationv1alpha1.PhaseDisabled
		nhc.Status.Reason = fmt.Sprintf("NHC is disabled: %s: %s", disabledCondition.Reason, disabledCondition.Message)
	} else if len(nhc.Spec.PauseRequests) > 0 {
		nhc.Status.Phase = remediationv1alpha1.PhasePaused
		nhc.Status.Reason = fmt.Sprintf("NHC is paused: %s", strings.Join(nhc.Spec.PauseRequests, ","))
	} else if len(nhc.Status.InFlightRemediations) > 0 {
		nhc.Status.Phase = remediationv1alpha1.PhaseRemediating
		nhc.Status.Reason = fmt.Sprintf("NHC is remediating %v nodes", len(nhc.Status.InFlightRemediations))
	} else {
		nhc.Status.Phase = remediationv1alpha1.PhaseEnabled
		nhc.Status.Reason = "NHC is enabled, no ongoing remediation"
	}

	mergeFrom := client.MergeFrom(nhcOrig)

	// check if there are any changes.
	// reflect.DeepEqual does not work, it has many false positives!
	if patchBytes, err := mergeFrom.Data(nhc); err != nil {
		log.Error(err, "failed to create patch")
		return err
	} else if string(patchBytes) == "{}" {
		// no change
		return nil
	} else {
		log.Info("Patching NHC status", "new status", nhc.Status, "patch", string(patchBytes))
	}

	return r.Client.Status().Patch(context.Background(), nhc, mergeFrom)
}

func (r *NodeHealthCheckReconciler) alertOldRemediationCR(remediationCR *unstructured.Unstructured) (bool, *time.Duration) {

	isSendAlert := false
	var nextReconcile *time.Duration = nil
	//verify remediationCR is old
	now := currentTime()
	if currentTime().After(remediationCR.GetCreationTimestamp().Add(remediationCRAlertTimeout)) {
		var remediationCrAnnotations map[string]string
		if remediationCrAnnotations = remediationCR.GetAnnotations(); remediationCrAnnotations == nil {
			remediationCrAnnotations = map[string]string{}
		}
		//verify this is the first alert for this remediationCR
		if _, isAlertedSent := remediationCrAnnotations[oldRemediationCRAnnotationKey]; !isAlertedSent {
			remediationCrAnnotations[oldRemediationCRAnnotationKey] = "flagon"
			remediationCR.SetAnnotations(remediationCrAnnotations)
			if err := r.Client.Update(context.TODO(), remediationCR); err == nil {
				isSendAlert = true
				r.Log.Info("old remediation, going to alert!")
			} else {
				r.Log.Error(err, "Setting `old remediationCR` annotation on remediation CR %s: failed to update: %v", remediationCR.GetName(), err)
			}
		}
	} else {
		calcNextReconcile := remediationCRAlertTimeout - now.Sub(remediationCR.GetCreationTimestamp().Time) + time.Minute
		nextReconcile = &calcNextReconcile
	}
	return isSendAlert, nextReconcile

}

func (r *NodeHealthCheckReconciler) addWatch(remediationCR *unstructured.Unstructured) error {
	r.watchesLock.Lock()
	defer r.watchesLock.Unlock()

	key := remediationCR.GroupVersionKind().String()
	if _, exists := r.watches[key]; exists {
		// already watching
		return nil
	}
	if err := r.ctrl.Watch(
		&source.Kind{Type: remediationCR},
		handler.EnqueueRequestsFromMapFunc(utils.NHCByRemediationCRMapperFunc(r.Log)),
		predicate.Funcs{
			// we are just interested in update events for now
			CreateFunc:  func(_ event.CreateEvent) bool { return false },
			DeleteFunc:  func(_ event.DeleteEvent) bool { return false },
			GenericFunc: func(_ event.GenericEvent) bool { return false },
		},
	); err != nil {
		return err
	}
	r.watches[key] = struct{}{}
	return nil
}

func getTimeoutAt(remediation *remediationv1alpha1.Remediation, configuredTimeout *time.Duration) time.Time {
	return remediation.Started.Add(*configuredTimeout)
}

func remediationFailed(remediationCR *unstructured.Unstructured, log logr.Logger) bool {
	succeededCondition := getSucceededCondition(remediationCR, log)
	return succeededCondition != nil && succeededCondition.Status == metav1.ConditionFalse
}

func getSucceededCondition(remediationCR *unstructured.Unstructured, log logr.Logger) *metav1.Condition {
	if conditions, found, _ := unstructured.NestedSlice(remediationCR.Object, "status", "conditions"); found {
		for _, condition := range conditions {
			if condition, ok := condition.(map[string]interface{}); ok {
				if condType, found, _ := unstructured.NestedString(condition, "type"); found && condType == conditionTypeSucceeded {
					condStatus, _, _ := unstructured.NestedString(condition, "status")
					var condLastTransition time.Time
					if condLastTransitionString, foundLastTransition, _ := unstructured.NestedString(condition, "lastTransitionTime"); foundLastTransition {
						condLastTransition, _ = time.Parse(time.RFC3339, condLastTransitionString)
					}
					cond := &metav1.Condition{
						Type:               condType,
						Status:             metav1.ConditionStatus(condStatus),
						LastTransitionTime: metav1.Time{Time: condLastTransition},
					}
					log.Info("found succeeded condition", "status", cond.Status, "reason", cond.Reason, "message", cond.Message, "lastTransition", cond.LastTransitionTime.UTC().Format(time.RFC3339))
					return cond
				}
			}
		}
	}
	return nil
}
