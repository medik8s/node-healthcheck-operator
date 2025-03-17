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
	commonannotations "github.com/medik8s/common/pkg/annotations"
	commonconditions "github.com/medik8s/common/pkg/conditions"
	"github.com/medik8s/common/pkg/etcd"
	commonevents "github.com/medik8s/common/pkg/events"
	commonlabels "github.com/medik8s/common/pkg/labels"
	"github.com/medik8s/common/pkg/lease"
	"github.com/medik8s/common/pkg/nodes"
	"github.com/pkg/errors"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
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
	oldRemediationCRAnnotationKey = "nodehealthcheck.medik8s.io/old-remediation-cr-flag"
	remediationCRAlertTimeout     = time.Hour * 48
	eventReasonNoTemplateLeft     = "NoTemplateLeft"
	enabledMessage                = "No issues found, NodeHealthCheck is enabled."

	// RemediationControlPlaneLabelKey is the label key to put on remediation CRs for control plane nodes
	RemediationControlPlaneLabelKey = "remediation.medik8s.io/isControlPlaneNode"
)

var (
	clusterUpgradeRequeueAfter       = 1 * time.Minute
	templateNotFoundRequeueAfter     = 15 * time.Second
	logWhenCRPendingDeletionDuration = 10 * time.Second
	currentTime                      = func() time.Time { return time.Now() }
)

// NodeHealthCheckReconciler reconciles a NodeHealthCheck object
type NodeHealthCheckReconciler struct {
	client.Client
	Log                         logr.Logger
	Recorder                    record.EventRecorder
	ClusterUpgradeStatusChecker cluster.UpgradeChecker
	MHCChecker                  mhc.Checker
	Capabilities                cluster.Capabilities
	MHCEvents                   chan event.GenericEvent
	controller                  controller.Controller
	WatchManager                resources.WatchManager
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeHealthCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	controller, err := ctrl.NewControllerManagedBy(mgr).
		For(&remediationv1alpha1.NodeHealthCheck{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			&v1.Node{},
			handler.EnqueueRequestsFromMapFunc(utils.NHCByNodeMapperFunc(mgr.GetClient(), mgr.GetLogger())),
			builder.WithPredicates(
				predicate.Funcs{
					// check for modified conditions on updates in order to prevent unneeded reconciliations
					UpdateFunc: func(ev event.UpdateEvent) bool { return nodeUpdateNeedsReconcile(ev) },
					// potentially delete orphaned remediation CRs when new node will have new name
					DeleteFunc: func(_ event.DeleteEvent) bool { return true },
					// create (new nodes don't have correct conditions yet), and generic events are not interesting for now
					CreateFunc:  func(_ event.CreateEvent) bool { return false },
					GenericFunc: func(_ event.GenericEvent) bool { return false },
				},
			),
		).
		WatchesRawSource(
			&source.Channel{Source: r.MHCEvents},
			handler.EnqueueRequestsFromMapFunc(utils.NHCByMHCEventMapperFunc(mgr.GetClient(), mgr.GetLogger())),
		).
		Build(r)

	if err != nil {
		return err
	}
	r.WatchManager.SetController(controller)
	return nil
}

// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=remediation.medik8s.io,resources=nodehealthchecks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=remediation.medik8s.io,resources=nodehealthchecks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=remediation.medik8s.io,resources=nodehealthchecks/finalizers,verbs=update
// +kubebuilder:rbac:groups=config.openshift.io,resources=clusterversions,verbs=get;list;watch
// +kubebuilder:rbac:groups=machine.openshift.io,resources=machines,verbs=get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;update;patch;watch;create;delete
// +kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;create

// for the etcd check of github.com/medik8s/common/pkg/etcd
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *NodeHealthCheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, returnErr error) {
	log := r.Log.WithValues("NodeHealthCheck name", req.Name)
	log.Info("reconciling")
	// get nhc
	nhc := &remediationv1alpha1.NodeHealthCheck{}
	err := r.Get(ctx, req.NamespacedName, nhc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("NodeHealthCheck CR not found", "name", req.Name)
			return result, nil
		}
		log.Error(err, "failed to get NodeHealthCheck CR", "name", req.Name)
		return result, err
	}

	leaseHolderIdent := fmt.Sprintf("NodeHealthCheck-%s", nhc.GetName())
	leaseManager, err := resources.NewLeaseManager(r.Client, leaseHolderIdent, log)
	if err != nil {
		return result, err
	}
	resourceManager := resources.NewManager(r.Client, ctx, r.Log, r.Capabilities.HasMachineAPI, leaseManager, r.Recorder)

	// always check if we need to patch status before we exit Reconcile
	nhcOrig := nhc.DeepCopy()
	defer func() {
		patchErr := r.patchStatus(ctx, log, nhc, nhcOrig)
		if patchErr != nil {
			log.Error(patchErr, "failed to update status")
		}
		returnErr = utilerrors.NewAggregate([]error{patchErr, returnErr})
		log.Info("reconcile end", "error", returnErr, "requeue", result.Requeue, "requeuAfter", result.RequeueAfter)
	}()

	// set counters to zero for disabled NHC
	nhc.Status.ObservedNodes = pointer.Int(0)
	nhc.Status.HealthyNodes = pointer.Int(0)
	//clear deprecated field before it's removed from the API
	nhc.Status.InFlightRemediations = nil

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
			commonevents.WarningEvent(r.Recorder, nhc, utils.EventReasonDisabled, "Custom MachineHealthCheck(s) detected, disabling NodeHealthCheck to avoid conflicts")
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
			commonevents.WarningEventf(r.Recorder, nhc, utils.EventReasonDisabled, "Disabling NHC. Reason: %s, Message: %s", reason, message)
		}
		if reason == remediationv1alpha1.ConditionReasonDisabledTemplateNotFound {
			// requeue for checking back if template exists later
			result.RequeueAfter = templateNotFoundRequeueAfter
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
		commonevents.NormalEvent(r.Recorder, nhc, utils.EventReasonEnabled, enabledMessage)
	}

	// add watches for template and remediation CRs
	if err = r.WatchManager.AddWatchesNhc(resourceManager, nhc); err != nil {
		return result, err
	}

	// select nodes using the nhc.selector
	selectedNodes, err := resourceManager.GetNodes(nhc.Spec.Selector)
	if err != nil {
		return result, err
	}

	// check nodes health
	notMatchingNodes, soonMatchingNodes, matchingNodes, requeueAfter := r.checkNodeConditions(selectedNodes, nhc)
	updateRequeueAfter(&result, requeueAfter)

	// TODO consider setting Disabled condition?
	if r.isClusterUpgrading(matchingNodes) {
		msg := "Postponing potential remediations because of ongoing cluster upgrade"
		log.Info(msg)
		commonevents.NormalEvent(r.Recorder, nhc, utils.EventReasonRemediationSkipped, msg)
		result.RequeueAfter = clusterUpgradeRequeueAfter
		return result, nil
	}

	if len(nhc.Spec.PauseRequests) > 0 {
		// some actors want to pause remediation.
		msg := "Postponing potential remediations because of pause requests"
		log.Info(msg)
		commonevents.NormalEvent(r.Recorder, nhc, utils.EventReasonRemediationSkipped, msg)
		return result, nil
	}

	// Delete orphaned CRs: they have no node, and Succeeded and NodeNameChangeExpected conditions set to True.
	// This happens e.g. on cloud providers with Machine Deletion remediation: the broken node will be deleted and
	// a new node created, with a new name, and no relationship to the old node
	if err = r.deleteOrphanedRemediationCRs(nhc, append(notMatchingNodes, append(soonMatchingNodes, matchingNodes...)...), resourceManager, log); err != nil {
		return result, err
	}

	// Delete remediation CRs for healthy nodes
	// Don't do this for nodes which soon match unhealthy conditions, because they might just have switched from one unhealthy condition to another,
	// but the timeout of the new condition didn't expire yet.
	// (e.g. from Ready=Unknown to Ready=False)
	healthyCount := 0
	for _, node := range notMatchingNodes {
		log.Info("handling healthy node", "node", node.GetName())
		remediationCRs, err := resourceManager.HandleHealthyNode(node.GetName(), node.GetName(), nhc)
		if err != nil {
			log.Error(err, "failed to handle healthy node", "node", node.Name)
			return result, err
		}

		// only consider nodes without remediation CRs as healthy
		if len(remediationCRs) == 0 {
			resources.UpdateStatusNodeHealthy(node.GetName(), nhc)
			healthyCount++
			continue
		}

		// set conditions healthy timestamp
		conditionsHealthyTimestamp := resources.UpdateStatusNodeConditionsHealthy(node.GetName(), nhc, currentTime())
		if conditionsHealthyTimestamp != nil {
			// warn about pending CRs when all CRs have been deleted for some time already but still exist
			doLog := true
			logThreshold := conditionsHealthyTimestamp.Add(-logWhenCRPendingDeletionDuration)
			for _, cr := range remediationCRs {
				if cr.GetDeletionTimestamp() == nil || cr.GetDeletionTimestamp().After(logThreshold) {
					doLog = false
					// requeue when we need to log
					var logIn time.Duration
					if cr.GetDeletionTimestamp() != nil {
						logIn = cr.GetDeletionTimestamp().Sub(logThreshold) + time.Second
					} else {
						logIn = logWhenCRPendingDeletionDuration + time.Second
					}
					updateRequeueAfter(&result, &logIn)
				}
			}
			if doLog {
				log.Info("Node conditions don't match unhealthy condition anymore, but node has remediation CR(s) with pending deletion, considering node as unhealthy")
			}
		}
	}

	nhc.Status.ObservedNodes = pointer.Int(len(selectedNodes))
	nhc.Status.HealthyNodes = &healthyCount

	// log currently unhealthy nodes with only soon unhealthy conditions left
	for _, node := range soonMatchingNodes {
		for _, unhealthy := range nhc.Status.UnhealthyNodes {
			if unhealthy.Name == node.GetName() {
				log.Info("Ignoring node, because it was unhealthy, and is likely to be unhealthy again.", "node", node.GetName())
			}
		}
	}

	// we are done in case we don't have unhealthy nodes
	if len(matchingNodes) == 0 {
		return result, nil
	}

	// check if we have enough healthy nodes
	skipRemediation := false
	if minHealthy, err := intstr.GetScaledValueFromIntOrPercent(nhc.Spec.MinHealthy, len(selectedNodes), true); err != nil {
		log.Error(err, "failed to calculate min healthy allowed nodes",
			"minHealthy", nhc.Spec.MinHealthy, "observedNodes", nhc.Status.ObservedNodes)
		return result, err
	} else if *nhc.Status.HealthyNodes < minHealthy {
		msg := fmt.Sprintf("Skipped remediation because the number of healthy nodes selected by the selector is %d and should equal or exceed %d", *nhc.Status.HealthyNodes, minHealthy)
		log.Info(msg)
		commonevents.WarningEvent(r.Recorder, nhc, utils.EventReasonRemediationSkipped, msg)
		skipRemediation = true
	}

	// remediate unhealthy nodes
	for _, node := range matchingNodes {

		// update unhealthy node in status
		resources.UpdateStatusNodeUnhealthy(&node, nhc)
		if skipRemediation {
			continue
		}

		if r.isNodeRemediationExcluded(&node) {
			msg := fmt.Sprintf("Remediation skipped on node %s: node has Exclude from Remediation label", node.GetName())
			log.Info(msg)
			commonevents.WarningEvent(r.Recorder, nhc, utils.EventReasonRemediationSkipped, msg)
			continue
		}

		log.Info("handling unhealthy node", "node", node.GetName())
		requeueAfter, err := r.remediate(ctx, &node, nhc, resourceManager)
		if err != nil {
			// don't try to remediate other nodes
			log.Error(err, "failed to start remediation")
			return result, err
		}
		updateRequeueAfter(&result, requeueAfter)

		// check if we need to alert about a very old remediation CR
		remediationCRs, err := resourceManager.ListRemediationCRs(utils.GetAllRemediationTemplates(nhc), func(cr unstructured.Unstructured) bool {
			return utils.GetNodeNameFromCR(cr) == node.GetName() && resources.IsOwner(&cr, nhc)
		})
		for _, remediationCR := range remediationCRs {
			isAlert, requeueAfter := r.alertOldRemediationCR(&remediationCR)
			if isAlert {
				metrics.ObserveNodeHealthCheckOldRemediationCR(node.Name, node.Namespace)
			}
			updateRequeueAfter(&result, requeueAfter)
		}
	}

	return result, nil
}

func (r *NodeHealthCheckReconciler) isClusterUpgrading(nodesToBeRemediated []v1.Node) bool {
	clusterUpgrading, err := r.ClusterUpgradeStatusChecker.Check(nodesToBeRemediated)
	if err != nil {
		// if we can't reliably tell if the cluster is upgrading then just continue with remediation.
		// TODO finer error handling may help to decide otherwise here.
		r.Log.Error(err, "failed to check if the cluster is upgrading. Proceed with remediation as if it is not upgrading")
	}
	return clusterUpgrading
}

func (r *NodeHealthCheckReconciler) checkNodeConditions(nodes []v1.Node, nhc *remediationv1alpha1.NodeHealthCheck) (notMatchingNodes, soonMatchingNodes, matchingNodes []v1.Node, requeueAfter *time.Duration) {
	for _, node := range nodes {
		node := node
		if matchesUnhealthyConditions, thisRequeueAfter := r.matchesUnhealthyConditions(nhc, &node); !matchesUnhealthyConditions {
			if thisRequeueAfter != nil && *thisRequeueAfter > 0 {
				soonMatchingNodes = append(soonMatchingNodes, node)
				requeueAfter = utils.MinRequeueDuration(requeueAfter, thisRequeueAfter)
			} else {
				notMatchingNodes = append(notMatchingNodes, node)
			}
		} else if r.MHCChecker.NeedIgnoreNode(&node) {
			// consider terminating nodes being handled by MHC as healthy, from NHC point of view
			notMatchingNodes = append(notMatchingNodes, node)
		} else {
			matchingNodes = append(matchingNodes, node)
		}
	}
	return
}

func (r *NodeHealthCheckReconciler) matchesUnhealthyConditions(nhc *remediationv1alpha1.NodeHealthCheck, node *v1.Node) (bool, *time.Duration) {
	nodeConditionByType := make(map[v1.NodeConditionType]v1.NodeCondition)
	for _, nc := range node.Status.Conditions {
		nodeConditionByType[nc.Type] = nc
	}

	isNodeRemediating := r.isRemediatingNode(nhc.Status.UnhealthyNodes, node.GetName())
	var expiresAfter *time.Duration
	for _, c := range nhc.Spec.UnhealthyConditions {
		n, exists := nodeConditionByType[c.Type]
		if !exists {
			continue
		}
		if n.Status == c.Status {
			now := currentTime()
			if now.After(n.LastTransitionTime.Add(c.Duration.Duration)) {
				// unhealthy condition duration expired, node is unhealthy
				r.Log.Info("Node matches unhealthy condition", "node", node.GetName(), "condition type", c.Type, "condition status", c.Status)
				//In case a remediation is already created no need to repeat this event
				if !isNodeRemediating {
					commonevents.NormalEventf(r.Recorder, nhc, utils.EventReasonDetectedUnhealthy, "Node matches unhealthy condition. Node %q, condition type %q, condition status %q", node.GetName(), c.Type, c.Status)
				}
				return true, nil
			} else {
				// unhealthy condition duration not expired yet, node is healthy. Requeue when duration expires
				thisExpiresAfter := n.LastTransitionTime.Add(c.Duration.Duration).Sub(now)
				r.Log.Info("Node is going to match unhealthy condition", "node", node.GetName(), "condition type", c.Type, "condition status", c.Status, "duration left", thisExpiresAfter)
				expiresAfter = utils.MinRequeueDuration(expiresAfter, pointer.Duration(thisExpiresAfter+1*time.Second))
			}
		}
	}
	return false, expiresAfter
}

func (r *NodeHealthCheckReconciler) deleteOrphanedRemediationCRs(nhc *remediationv1alpha1.NodeHealthCheck, allNodes []v1.Node, rm resources.Manager, log logr.Logger) error {
	orphanedRemediationCRs, err := rm.ListRemediationCRs(utils.GetAllRemediationTemplates(nhc), func(cr unstructured.Unstructured) bool {
		// skip already deleted CRs
		if cr.GetDeletionTimestamp() != nil {
			return false
		}

		// skip CRs we don't own
		if !resources.IsOwner(&cr, nhc) {
			return false
		}

		// check conditions
		// for some remediators (e.g. MDR) node deletion is expected. If so, wait until they are succeeded.
		// for all other remediators, we can delete the CRs immediately after node deletion
		permanentNodeDeletionExpectedCondition := getCondition(&cr, commonconditions.PermanentNodeDeletionExpectedType, log)
		permanentNodeDeletionExpected := permanentNodeDeletionExpectedCondition != nil && permanentNodeDeletionExpectedCondition.Status == metav1.ConditionTrue
		succeededCondition := getCondition(&cr, commonconditions.SucceededType, log)
		succeeded := succeededCondition != nil && succeededCondition.Status == metav1.ConditionTrue
		if permanentNodeDeletionExpected && !succeeded {
			// node deletion is expected, but remediation not succeeded yet
			return false
		}

		// check if node exists
		for _, node := range allNodes {
			if node.GetName() == utils.GetNodeNameFromCR(cr) {
				// node still exists
				return false
			}
		}

		return true
	})
	if err != nil {
		log.Error(err, "failed to check for orphaned remediation CRs")
		return err
	}

	if len(orphanedRemediationCRs) == 0 {
		return nil
	}

	log.Info("Going to delete orphaned remediation CRs", "count", len(orphanedRemediationCRs))
	for _, cr := range orphanedRemediationCRs {
		nodeName := utils.GetNodeNameFromCR(cr)
		// do some housekeeping first. When the CRs are deleted, we never get back here...
		if err := rm.CleanUp(nodeName); err != nil {
			log.Error(err, "failed to clean up orphaned node", "node", nodeName)
			return err
		}
		resources.UpdateStatusNodeHealthy(nodeName, nhc)

		if deleted, err := rm.DeleteRemediationCR(&cr, nhc); err != nil {
			log.Error(err, "failed to delete orphaned remediation CR", "name", cr.GetName())
			return err
		} else if deleted {
			log.Info("deleted orphaned remediation CR", "name", cr.GetName(), "for deleted node", nodeName)
		}

	}
	return nil
}

func (r *NodeHealthCheckReconciler) remediate(ctx context.Context, node *v1.Node, nhc *remediationv1alpha1.NodeHealthCheck, rm resources.Manager) (*time.Duration, error) {

	log := utils.GetLogWithNHC(r.Log, nhc)

	// prevent remediation of more than 1 control plane node at a time!
	isControlPlaneNode := nodes.IsControlPlane(node)
	if isControlPlaneNode {
		if isAllowed, err := r.isControlPlaneRemediationAllowed(ctx, node, nhc, rm); err != nil {
			return nil, errors.Wrapf(err, "failed to check if control plane remediation is allowed")
		} else if !isAllowed {
			log.Info("skipping remediation for preventing control plane / etcd quorum loss, going to retry in a minute", "node", node.GetName())
			commonevents.WarningEventf(r.Recorder, nhc, utils.EventReasonRemediationSkipped, "Skipping remediation of %s for preventing control plane / etcd quorum loss, going to retry in a minute", node.GetName())
			return pointer.Duration(1 * time.Minute), nil
		}
	}
	// generate remediation CR
	currentTemplate, timeout, err := rm.GetCurrentTemplateWithTimeout(node, nhc)
	if err != nil {
		if _, ok := err.(resources.NoTemplateLeftError); ok {
			log.Error(err, "Remediation timed out, and no template left to try")
			commonevents.WarningEventf(r.Recorder, nhc, eventReasonNoTemplateLeft, "Remediation timed out, and no template left to try. %s", err.Error())
			// there is nothing we can do about this
			return nil, nil
		}
		return nil, errors.Wrapf(err, "failed to get current template")
	}
	generatedRemediationCR, err := rm.GenerateRemediationCRForNode(node, nhc, currentTemplate)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to generate remediation CR")
	}

	if isControlPlaneNode {
		labels := generatedRemediationCR.GetLabels()
		labels[RemediationControlPlaneLabelKey] = ""
		generatedRemediationCR.SetLabels(labels)
	}

	currentRemediationDuration, previousRemediationsDuration := utils.GetRemediationDuration(nhc, generatedRemediationCR)

	// create remediation CR
	created, leaseRequeueIn, remediationCR, err := rm.CreateRemediationCR(generatedRemediationCR, nhc, &node.Name, currentRemediationDuration, previousRemediationsDuration)

	if err != nil {
		// An unhealthy node exists, but remediation couldn't be created because lease wasn't obtained
		if _, isLeaseAlreadyTaken := err.(lease.AlreadyHeldError); isLeaseAlreadyTaken {
			msg := fmt.Sprintf("Skipped remediation of node: %s, because node lease is already taken", node.GetName())
			log.Info(msg)
			commonevents.WarningEvent(r.Recorder, nhc, utils.EventReasonRemediationSkipped, msg)
			return leaseRequeueIn, nil
		}

		// Lease is overdue
		if _, isLeaseOverDue := err.(resources.LeaseOverDueError); isLeaseOverDue {
			now := currentTime()
			// add timeout annotation to remediation CR if it not Succeeded yet
			if !remediationSucceeded(remediationCR, log) {
				if timeOutErr := r.addTimeOutAnnotation(rm, remediationCR, metav1.Time{Time: now}); timeOutErr != nil {
					return nil, timeOutErr
				}
			} else {
				log.Info("skipping timeout annotation on remediation CR: Succeeded condition is True", "CR name", remediationCR.GetName())
			}
			startedRemediation := resources.FindStatusRemediation(node, nhc, func(r *remediationv1alpha1.Remediation) bool {
				return r.Resource.GroupVersionKind() == remediationCR.GroupVersionKind() &&
					(r.TemplateName == "" || r.TemplateName == currentTemplate.GetName())
			})

			if startedRemediation == nil {
				// should not have happened, seems last status update failed
				return nil, errors.New("failed to find started remediation in status for handling overdue lease")
			}

			// update status (important to do this after CR update, else we won't retry that update in case of error)
			startedRemediation.TimedOut = &metav1.Time{Time: now}
			return nil, nil
		}

		if _, ok := err.(resources.RemediationCRNotOwned); ok {
			// CR exists but not owned by us, nothing to do
			return nil, nil
		}
		return nil, errors.Wrapf(err, "failed to create remediation CR")
	}

	// always update status, in case patching it failed during last reconcile
	resources.UpdateStatusRemediationStarted(node, nhc, remediationCR)

	// ensure to provide correct metrics in case the CR existed already after a pod restart
	metrics.ObserveNodeHealthCheckRemediationCreated(node.GetName(), remediationCR.GetNamespace(), remediationCR.GetKind())

	if created {
		commonevents.NormalEventf(r.Recorder, nhc, utils.EventReasonRemediationCreated, "Created remediation object for node %s", node.Name)
		var requeueIn *time.Duration
		if timeout != nil {
			// come back when timeout expires
			requeueIn = pointer.Duration(*timeout + 1*time.Second)
		}
		return utils.MinRequeueDuration(leaseRequeueIn, requeueIn), nil
	}
	// CR already exists, check for timeout in case we need to
	if timeout == nil {
		// no timeout set for classic remediation
		// nothing to do anymore here
		return leaseRequeueIn, nil
	}

	startedRemediation := resources.FindStatusRemediation(node, nhc, func(r *remediationv1alpha1.Remediation) bool {
		return r.Resource.GroupVersionKind() == remediationCR.GroupVersionKind() &&
			(r.TemplateName == "" || r.TemplateName == currentTemplate.GetName())
	})

	if startedRemediation == nil {
		// should not have happened, seems last status update failed
		return nil, errors.New("failed to find started remediation in status for handling timeout")
	}

	if startedRemediation.TimedOut != nil {
		// timeout handled already: should not have happened, but ok. Just reconcile again asap for trying the next template
		return nil, errors.New("unexpected timout found on started remediation in status")
	}

	now := metav1.Time{Time: currentTime()}
	timeoutAt := getTimeoutAt(startedRemediation, timeout)
	timedOut := now.After(timeoutAt)

	failed := remediationFailed(remediationCR, log)

	if !timedOut && !failed {
		// not timed out yet, come back when we do so
		return utils.MinRequeueDuration(leaseRequeueIn, pointer.Duration(timeoutAt.Sub(now.Time))), nil
	}

	// handle timeout and failure
	if timedOut {
		log.Info("remediation timed out")
	} else if failed {
		log.Info("remediation failed")
	}

	// add timeout annotation to remediation CR if it not Succeeded yet
	if !remediationSucceeded(remediationCR, log) {
		if err := r.addTimeOutAnnotation(rm, remediationCR, now); err != nil {
			return nil, err
		}
	} else {
		log.Info("skipping timeout annotation on remediation CR: Succeeded condition is True", "CR name", remediationCR.GetName())
	}
	// update status (important to do this after CR update, else we won't retry that update in case of error)
	startedRemediation.TimedOut = &now

	// try next remediation asap
	return pointer.Duration(1 * time.Second), nil
}

func (r *NodeHealthCheckReconciler) addTimeOutAnnotation(rm resources.Manager, remediationCR *unstructured.Unstructured, now metav1.Time) error {
	annotations := remediationCR.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 1)
	}
	annotations[commonannotations.NhcTimedOut] = now.Format(time.RFC3339)
	remediationCR.SetAnnotations(annotations)
	if err := rm.UpdateRemediationCR(remediationCR); err != nil {
		return errors.Wrapf(err, "failed to update remediation CR with timeout annotation")
	}
	return nil
}

func (r *NodeHealthCheckReconciler) isControlPlaneRemediationAllowed(ctx context.Context, node *v1.Node, nhc *remediationv1alpha1.NodeHealthCheck, rm resources.Manager) (bool, error) {
	if !nodes.IsControlPlane(node) {
		return true, fmt.Errorf("%s isn't a control plane node", node.GetName())
	}

	// check all remediation CRs. If there already is one for another control plane node, skip remediation
	controlPlaneRemediationCRs, err := rm.ListRemediationCRs(utils.GetAllRemediationTemplates(nhc), func(cr unstructured.Unstructured) bool {
		_, isControlPlane := cr.GetLabels()[RemediationControlPlaneLabelKey]
		return isControlPlane
	})
	if err != nil {
		return false, err
	}
	// if there is a control plane remediation CR for this node already, we can continue with the remediation process
	for _, cr := range controlPlaneRemediationCRs {
		nodeName := utils.GetNodeNameFromCR(cr)
		if nodeName == node.GetName() {
			return true, nil
		}
		r.Log.Info("ongoing control plane remediation", "node", nodeName)
	}
	// if there is a control plane remediation CR for another cp node, don't start remediation for this node
	if len(controlPlaneRemediationCRs) > 0 {
		return false, nil
	}

	// no ongoing control plane remediation, check etcd quorum
	if !r.Capabilities.IsOnOpenshift {
		// etcd quorum PDB is only installed in OpenShift
		return true, nil
	}
	var allowed bool
	if allowed, err = etcd.IsEtcdDisruptionAllowed(ctx, r.Client, r.Log, node); err != nil {
		return false, err
	}
	return allowed, nil
}

func (r *NodeHealthCheckReconciler) patchStatus(ctx context.Context, log logr.Logger, nhc, nhcOrig *remediationv1alpha1.NodeHealthCheck) error {

	// calculate phase and reason
	disabledCondition := meta.FindStatusCondition(nhc.Status.Conditions, remediationv1alpha1.ConditionTypeDisabled)
	if disabledCondition != nil && disabledCondition.Status == metav1.ConditionTrue {
		nhc.Status.Phase = remediationv1alpha1.PhaseDisabled
		nhc.Status.Reason = fmt.Sprintf("NHC is disabled: %s: %s", disabledCondition.Reason, disabledCondition.Message)
	} else if len(nhc.Spec.PauseRequests) > 0 {
		nhc.Status.Phase = remediationv1alpha1.PhasePaused
		nhc.Status.Reason = fmt.Sprintf("NHC is paused: %s", strings.Join(nhc.Spec.PauseRequests, ","))
	} else if r.isRemediating(nhc.Status.UnhealthyNodes) {
		nhc.Status.Phase = remediationv1alpha1.PhaseRemediating
		nhc.Status.Reason = fmt.Sprintf("NHC is remediating %v nodes", len(nhc.Status.UnhealthyNodes))
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

	// only update lastUpdate when there were other changes
	now := metav1.Now()
	nhc.Status.LastUpdateTime = &now

	if err := r.Client.Status().Patch(ctx, nhc, mergeFrom); err != nil {
		return err
	}

	// Wait until the cache is updated in order to prevent reading a stale status in the next reconcile
	// and making wrong decisions based on it. The chance to run into this is very low, because we use RequeueAfter
	// with a minimum delay of 1 second everywhere instead of Requeue: true, but this needs to be fixed because
	// it bypasses the controller's rate limiter!
	err := wait.PollWithContext(ctx, 200*time.Millisecond, 5*time.Second, func(ctx context.Context) (bool, error) {
		tmpNhc := &remediationv1alpha1.NodeHealthCheck{}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(nhc), tmpNhc); err != nil {
			if apierrors.IsNotFound(err) {
				// nothing we can do anymore
				return true, nil
			}
			return false, nil
		}
		return tmpNhc.Status.LastUpdateTime != nil && (tmpNhc.Status.LastUpdateTime.Equal(nhc.Status.LastUpdateTime) || tmpNhc.Status.LastUpdateTime.After(nhc.Status.LastUpdateTime.Time)), nil
	})
	if err != nil {
		return errors.Wrapf(err, "failed to wait for updated cache after status patch")
	}
	return nil
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

func (r *NodeHealthCheckReconciler) isNodeRemediationExcluded(node *v1.Node) bool {
	if nodeLabels := node.GetLabels(); nodeLabels == nil {
		return false
	} else {
		_, isNodeExcluded := nodeLabels[commonlabels.ExcludeFromRemediation]
		return isNodeExcluded
	}
}

func (r *NodeHealthCheckReconciler) isRemediating(unhealthyNodes []*remediationv1alpha1.UnhealthyNode) bool {
	for _, unhealthyNode := range unhealthyNodes {
		if len(unhealthyNode.Remediations) > 0 {
			return true
		}
	}
	return false
}

func (r *NodeHealthCheckReconciler) isRemediatingNode(unhealthyNodes []*remediationv1alpha1.UnhealthyNode, nodeName string) bool {
	for _, unhealthyNode := range unhealthyNodes {
		if nodeName == unhealthyNode.Name && len(unhealthyNode.Remediations) > 0 {
			return true
		}
	}
	return false
}

func getTimeoutAt(remediation *remediationv1alpha1.Remediation, configuredTimeout *time.Duration) time.Time {
	return remediation.Started.Add(*configuredTimeout)
}

func remediationFailed(remediationCR *unstructured.Unstructured, log logr.Logger) bool {
	succeededCondition := getCondition(remediationCR, commonconditions.SucceededType, log)
	return succeededCondition != nil && succeededCondition.Status == metav1.ConditionFalse
}

func remediationSucceeded(remediationCR *unstructured.Unstructured, log logr.Logger) bool {
	succeededCondition := getCondition(remediationCR, commonconditions.SucceededType, log)
	return succeededCondition != nil && succeededCondition.Status == metav1.ConditionTrue
}

func getCondition(remediationCR *unstructured.Unstructured, conditionType string, log logr.Logger) *metav1.Condition {
	if conditions, found, _ := unstructured.NestedSlice(remediationCR.Object, "status", "conditions"); found {
		for _, condition := range conditions {
			if condition, ok := condition.(map[string]interface{}); ok {
				if condType, found, _ := unstructured.NestedString(condition, "type"); found && condType == conditionType {
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
					log.Info("found condition", "type", cond.Type, "status", cond.Status, "reason", cond.Reason, "message", cond.Message, "lastTransition", cond.LastTransitionTime.UTC().Format(time.RFC3339))
					return cond
				}
			}
		}
	}
	return nil
}

// updateRequeueAfter updates the requeueAfter field of the result if newRequeueAfter is lower than the current value.
func updateRequeueAfter(result *ctrl.Result, newRequeueAfter *time.Duration) {
	if newRequeueAfter == nil {
		return
	}
	if result.RequeueAfter == 0 || *newRequeueAfter < result.RequeueAfter {
		result.RequeueAfter = *newRequeueAfter
	}
}
