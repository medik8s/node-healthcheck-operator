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
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeHealthCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&remediationv1alpha1.NodeHealthCheck{}).
		Watches(
			&source.Kind{Type: &v1.Node{}},
			handler.EnqueueRequestsFromMapFunc(utils.NHCByNodeMapperFunc(mgr.GetClient(), mgr.GetLogger())),
			builder.WithPredicates(
				predicate.Funcs{
					// we are just interested in create and update events
					DeleteFunc:  func(_ event.DeleteEvent) bool { return false },
					GenericFunc: func(_ event.GenericEvent) bool { return false },
				},
			),
		).
		Complete(r)
}

// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=remediation.medik8s.io,resources=nodehealthchecks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=remediation.medik8s.io,resources=nodehealthchecks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=remediation.medik8s.io,resources=nodehealthchecks/finalizers,verbs=update
// +kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get;list;watch
// +kubebuilder:rbac:groups=machine.openshift.io,resources=machines,verbs=get;list;watch
// +kubebuilder:rbac:groups=machine.openshift.io,resources=machinehealthchecks,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *NodeHealthCheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, returnErr error) {
	log := r.Log.WithValues("NodeHealthCheck name", req.Name)

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

	resourceManager := resources.NewManager(r.Client, ctx, r.Log, r.OnOpenShift)

	// always check if we need to patch status before we exit Reconcile
	nhcOrig := nhc.DeepCopy()
	defer func() {
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
	}()

	// set counters to zero for disabled NHC
	nhc.Status.ObservedNodes = 0
	nhc.Status.HealthyNodes = 0

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
	nhc.Status.ObservedNodes = len(nodes)

	// check nodes health
	healthyNodes, unhealthyNodes := r.checkNodesHealth(nodes, nhc)
	nhc.Status.HealthyNodes = len(healthyNodes)

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
		remediationCRs := resourceManager.GenerateAllRemediationCRs(&node, nhc)
		for _, remediationCR := range remediationCRs {
			if deleted, err := resourceManager.DeleteRemediationCR(remediationCR, nhc); err != nil {
				log.Error(err, "failed to delete remediation CR for healthy node", "node", node.Name)
				return result, err
			} else if deleted {
				log.Info("deleted remediation CR", "name", remediationCR.GetName(), "NHC name", nhc.Name)
				r.Recorder.Eventf(nhc, eventTypeNormal, eventReasonRemediationRemoved, "Deleted remediation CR for node %s", remediationCR.GetName())
			}

			// always update status, in case patching it failed during last reconcile
			resources.UpdateStatusNodeHealthy(&node, nhc)
		}
	}

	// check if we enough healthy nodes
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
		nextReconcile, err := r.remediate(&node, nhc, resourceManager)
		if err != nil {
			// don't try to remediate other nodes
			log.Error(err, "failed to start remediation")
			return result, err
		}
		if nextReconcile != nil {
			updateResultNextReconcile(&result, *nextReconcile)
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

func (r *NodeHealthCheckReconciler) checkNodesHealth(nodes []v1.Node, nhc *remediationv1alpha1.NodeHealthCheck) (healthy []v1.Node, unhealthy []v1.Node) {
	for _, node := range nodes {
		if r.isHealthy(nhc.Spec.UnhealthyConditions, node.Status.Conditions) {
			healthy = append(healthy, node)
		} else if r.MHCChecker.NeedIgnoreNode(&node) {
			// consider terminating nodes being handled by MHC as healthy, from NHC point of view
			healthy = append(healthy, node)
		} else {
			unhealthy = append(unhealthy, node)
		}
	}
	return
}

func (r *NodeHealthCheckReconciler) isHealthy(conditionTests []remediationv1alpha1.UnhealthyCondition, nodeConditions []v1.NodeCondition) bool {
	nodeConditionByType := make(map[v1.NodeConditionType]v1.NodeCondition)
	for _, nc := range nodeConditions {
		nodeConditionByType[nc.Type] = nc
	}

	for _, c := range conditionTests {
		n, exists := nodeConditionByType[c.Type]
		if !exists {
			continue
		}
		if n.Status == c.Status && currentTime().After(n.LastTransitionTime.Add(c.Duration.Duration)) {
			return false
		}
	}
	return true
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
	created, err := rm.CreateRemediationCR(remediationCR, nhc)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create remediation CR")
	}
	if created {
		r.Recorder.Event(nhc, eventTypeNormal, eventReasonRemediationCreated, fmt.Sprintf("Created remediation object for node %s", node.Name))
	}

	// always update status, in case patching it failed during last reconcile
	resources.UpdateStatusRemediationStarted(node, nhc, remediationCR)

	// CR already exists, check for timeout
	if !created {
		startedRemediation := resources.FindStatusRemediation(node, nhc, func(r *remediationv1alpha1.Remediation) bool {
			return r.Resource.GroupVersionKind() == remediationCR.GroupVersionKind()
		})
		if startedRemediation != nil && timeout != nil && startedRemediation.Started.Add(timeout.Duration).After(time.Now()) {
			now := metav1.Now()
			startedRemediation.TimedOut = &now

			// add time out annotation to remediation CR
			annotations := remediationCR.GetAnnotations()
			if annotations == nil {
				annotations = make(map[string]string, 1)
			}
			annotations[remediationTimedOutAnnotationkey] = metav1.Now().Format(time.RFC3339)
			remediationCR.SetAnnotations(annotations)
			if rm.UpdateRemediationCR(remediationCR); err != nil {
				return nil, errors.Wrapf(err, "failed to update remediation CR with timeout annotation")
			}

			// try next remediation asap
			return pointer.Duration(1 * time.Second), nil
		}
	}

	// TODO adapt to escalating remediation
	isAlert, nextReconcile := r.alertOldRemediationCR(remediationCR)
	if isAlert {
		metrics.ObserveNodeHealthCheckOldRemediationCR(node.Name, node.Namespace)
	}
	return nextReconcile, nil
}

func (r *NodeHealthCheckReconciler) isControlPlaneRemediationAllowed(node *v1.Node, nhc *remediationv1alpha1.NodeHealthCheck, rm resources.Manager) (bool, error) {
	if !utils.IsControlPlane(node) {
		return true, fmt.Errorf("%s isn't a control plane node", node.GetName())
	}
	// check all remediation CRs. If there already is one for another control plane node, skip remediation
	remediations, err := rm.ListRemediationCRs(nhc)
	if err != nil {
		return false, err
	}
	for _, remediation := range remediations {
		labels := remediation.GetLabels()
		if _, isControlPlane := labels[RemediationControlPlaneLabelKey]; isControlPlane {
			if remediation.GetName() != node.GetName() {
				return false, nil
			}
		}
	}
	return true, nil
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
	}

	log.Info("Patching NHC status", "new status", nhc.Status)
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

func updateResultNextReconcile(result *ctrl.Result, updatedRequeueAfter time.Duration) {
	if result.RequeueAfter == 0 || updatedRequeueAfter < result.RequeueAfter {
		result.RequeueAfter = updatedRequeueAfter
	}
}
