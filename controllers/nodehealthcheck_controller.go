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
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

// NodeHealthCheckReconciler reconciles a NodeHealthCheck object
type NodeHealthCheckReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core,resources=nodes,verbs=watch
// +kubebuilder:rbac:groups=remediation.medik8s.io,resources=nodehealthchecks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=remediation.medik8s.io,resources=nodehealthchecks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=remediation.medik8s.io,resources=nodehealthchecks/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the NodeHealthCheck object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (r *NodeHealthCheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("NodeHealthCheck", req.NamespacedName)

	// fetch nhc
	nhc := remediationv1alpha1.NodeHealthCheck{}
	err := r.Get(ctx, req.NamespacedName, &nhc)
	if err != nil {
		log.Error(err, "failed fetching Node Health Check", "object", nhc)
		return ctrl.Result{}, err
	}
	// select nodes using the nhc.selector
	nodes, err := r.fetchNodes(ctx, nhc.Spec.Selector)
	if err != nil {
		return ctrl.Result{}, err
	}

	// check nodes health
	unhealthy, err := r.checkNodesHealth(nodes, nhc)
	if err != nil {
		return ctrl.Result{}, err
	}

	// after loop
	updatedNhc := *nhc.DeepCopy()
	updatedNhc.Status.ObservedNodes = len(nodes)
	updatedNhc.Status.HealthyNodes = len(nodes) - len(unhealthy)

	maxUnhealthy, err := r.getMaxUnhealthy(updatedNhc)
	if err != nil {
		log.Error(err, "failed to calculate max unhealthy allowed nodes",
			"maxUnhealthy", nhc.Spec.MaxUnhealthy, "observedNodes", nhc.Status.ObservedNodes)
		return ctrl.Result{}, err
	}

	if len(unhealthy) <= maxUnhealthy {
		// trigger remediation per node
		for _, n := range unhealthy {
			err := r.remediate(n, nhc)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// TODO because backoff functionality is in question updating the remediation time is excluded
	// update mhc.status.triggeredRemediations map with the current remediation time per node
	err = r.patchStatus(nhc, updatedNhc)
	if err != nil {
		log.Error(err, "failed to patch NHC status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *NodeHealthCheckReconciler) fetchNodes(ctx context.Context, labelSelector metav1.LabelSelector) ([]v1.Node, error) {
	var nodes v1.NodeList
	selector, err := metav1.LabelSelectorAsSelector(&labelSelector)
	if err != nil {
		err = errors.Wrapf(err, "failed converting a selector from NHC selector")
		return []v1.Node{}, err
	}
	err = r.List(
		ctx,
		&nodes,
		&client.ListOptions{LabelSelector: selector},
	)
	return nodes.Items, err
}

func (r *NodeHealthCheckReconciler) checkNodesHealth(nodes []v1.Node, nhc remediationv1alpha1.NodeHealthCheck) (map[string]v1.Node, error) {
	unhealthy := make(map[string]v1.Node)
	for _, n := range nodes {
		if isHealthy(nhc.Spec.UnhealthyConditions, n.Status.Conditions) {
			err := r.markHealthy(n, nhc)
			if err != nil {
				return nil, err
			}
		} else {
			unhealthy[n.Name] = n
		}
	}
	return unhealthy, nil
}

func (r *NodeHealthCheckReconciler) markHealthy(n v1.Node, nhc remediationv1alpha1.NodeHealthCheck) error {
	cr, err := r.generateRemediationCR(n, nhc)
	if err != nil {
		return err
	}

	r.Log.Info("node seems healthy", "Node name", n.Name)

	err = r.Client.Delete(context.Background(), cr, &client.DeleteOptions{})
	// if the node is already healthy then there is no remediation object for it
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	r.Log.Info("deleted node external remediation object", "Node name", n.Name)
	return nil
}

func (r *NodeHealthCheckReconciler) getMaxUnhealthy(nhc remediationv1alpha1.NodeHealthCheck) (int, error) {
	if nhc.Spec.MaxUnhealthy.Type == 0 {
		return nhc.Spec.MaxUnhealthy.IntValue(), nil
	}
	return intstr.GetValueFromIntOrPercent(nhc.Spec.MaxUnhealthy, nhc.Status.ObservedNodes, false)
}

func isHealthy(conditionTests []remediationv1alpha1.UnhealthyCondition, nodeConditions []v1.NodeCondition) bool {
	now := time.Now()
	nodeConditionByType := make(map[v1.NodeConditionType]v1.NodeCondition)
	for _, nc := range nodeConditions {
		nodeConditionByType[nc.Type] = nc
	}

	for _, c := range conditionTests {
		n, exists := nodeConditionByType[c.Type]
		if !exists {
			continue
		}
		if n.Status == c.Status && now.After(n.LastTransitionTime.Add(c.Duration.Duration)) {
			return false
		}
	}
	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeHealthCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&remediationv1alpha1.NodeHealthCheck{}).
		Watches(&source.Kind{Type: &v1.Node{}}, handler.EnqueueRequestsFromMapFunc(allNHCHandler(mgr.GetClient()))).
		Complete(r)
}

func allNHCHandler(c client.Client) handler.MapFunc {
	// This closure is meant to fetch all NHC to fill the reconcile queue.
	// If we have multiple nhc then it is possible that we fetch nhc objects that
	// are unrelated to this node. Its even possible that the node still doesn't
	// have the right labels set to be picked up by the nhc selector.
	delegate := func(o client.Object) []reconcile.Request {
		var nhcList remediationv1alpha1.NodeHealthCheckList
		err := c.List(context.Background(), &nhcList, &client.ListOptions{})
		if err != nil {
			return nil
		}
		var r []reconcile.Request
		for _, n := range nhcList.Items {
			r = append(r, reconcile.Request{NamespacedName: types.NamespacedName{Name: n.GetName()}})
		}
		return r
	}
	return delegate
}

// shouldBackoff backs off if spec.backoff defined and the last time remediation was triggered
// meets the criteria of the backoff
func (r *NodeHealthCheckReconciler) shouldBackoff(n v1.Node, nhc remediationv1alpha1.NodeHealthCheck) bool {
	if nhc.Spec.Backoff == nil {
		return false
	}

	now := time.Now()
	remediationTimes := nhc.Status.TriggeredRemediations[n.Name]
	if remediationTimes != nil && len(remediationTimes) > 1 {
		// if we are passed the time limit then backoff
		firstRemediationTime := remediationTimes[0]
		if now.After(firstRemediationTime.Add(nhc.Spec.Backoff.Limit.Duration)) {
			return true
		}

		// exponential backoff - wait twice the period of time between last 2 attempts
		secondLastRemediationTime := remediationTimes[len(remediationTimes)-1]
		lastRemediationTime := remediationTimes[len(remediationTimes)]
		lastPeriod := lastRemediationTime.Sub(secondLastRemediationTime.Time)
		if now.Before(lastRemediationTime.Add(lastPeriod * 2)) {
			return true
		}
	}
	return false
}

func (r *NodeHealthCheckReconciler) remediate(n v1.Node, nhc remediationv1alpha1.NodeHealthCheck) error {
	cr, err := r.generateRemediationCR(n, nhc)
	if err != nil {
		return err
	}
	r.Log.Info("node seems unhealthy. Creating an external remediation object",
		"nodeName", n.Name, "CR name", cr.GetName(), "CR gvk", cr.GroupVersionKind())
	err = r.Client.Create(context.Background(), cr, &client.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		r.Log.Error(err, "failed to create an external remediation object")
		return err
	}
	return nil
}

func (r *NodeHealthCheckReconciler) generateRemediationCR(n v1.Node, nhc remediationv1alpha1.NodeHealthCheck) (*unstructured.Unstructured, error) {
	t, err := r.fetchTemplate(nhc)
	if err != nil {
		return nil, err
	}

	templateSpec, found, err := unstructured.NestedMap(t.Object, "spec", "template")
	if !found {
		return nil, errors.Errorf("missing Spec.Template on %v %q", t.GroupVersionKind(), t.GetName())
	} else if err != nil {
		return nil, errors.Wrapf(err, "failed to retrieve Spec.Template map on %v %q", t.GroupVersionKind(), t.GetName())
	}

	u := unstructured.Unstructured{Object: templateSpec}
	u.SetName(n.Name)
	u.SetNamespace(t.GetNamespace())
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   t.GroupVersionKind().Group,
		Version: t.GroupVersionKind().Version,
		Kind:    strings.TrimSuffix(t.GetKind(), "Template"),
	})
	u.SetOwnerReferences([]metav1.OwnerReference{
		{
			Kind:               n.Kind,
			Name:               n.Name,
			UID:                n.UID,
			Controller:         pointer.BoolPtr(false),
		},
	})
	u.SetLabels(map[string]string{
		"app.kubernetes.io/part-of": "node-healthcheck-controller",
	})
	u.SetResourceVersion("")
	u.SetFinalizers(nil)
	u.SetUID("")
	u.SetSelfLink("")
	return &u, nil
}

func (r *NodeHealthCheckReconciler) fetchTemplate(nhc remediationv1alpha1.NodeHealthCheck) (*unstructured.Unstructured, error) {
	t := nhc.Spec.ExternalRemediationTemplate.DeepCopy()
	obj := new(unstructured.Unstructured)
	obj.SetAPIVersion(t.APIVersion)
	obj.SetGroupVersionKind(t.GroupVersionKind())
	obj.SetName(t.Name)
	key := client.ObjectKey{Name: obj.GetName(), Namespace: t.Namespace}
	if err := r.Client.Get(context.Background(), key, obj); err != nil {
		return nil, errors.Wrapf(err, "failed to retrieve %s external object %q/%q", obj.GetKind(), key.Namespace, key.Name)
	}
	return obj, nil
}

func (r *NodeHealthCheckReconciler) patchStatus(nhc remediationv1alpha1.NodeHealthCheck, updatedNHC remediationv1alpha1.NodeHealthCheck) error {
	// all values to be patched expected to be updated on the current nhc.status
	patch := client.MergeFrom(nhc.DeepCopy())
	r.Log.Info("Patching NHC object", "patch", patch, "to", updatedNHC)
	return r.Client.Status().Patch(context.Background(), &updatedNHC, patch, &client.PatchOptions{})
}
