package utils

import (
	"context"

	"github.com/go-logr/logr"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

// NHCByNodeMapperFunc return the Node-to-NHC mapper function
func NHCByNodeMapperFunc(c client.Client, logger logr.Logger) handler.MapFunc {
	// This closure is meant to fetch all NHC to fill the reconcile queue.
	// If we have multiple nhc then it is possible that we fetch nhc objects that
	// are unrelated to this node. Its even possible that the node still doesn't
	// have the right labels set to be picked up by the nhc selector.
	delegate := func(o client.Object) []reconcile.Request {
		requests := make([]reconcile.Request, 0)

		node := &v1.Node{}
		if err := c.Get(context.Background(), client.ObjectKey{Name: o.GetName()}, node); err != nil {
			if !errors.IsNotFound(err) {
				logger.Error(err, "mapper: failed to get node", "node name", o.GetName())
			}
			return requests
		}

		nhcList := &remediationv1alpha1.NodeHealthCheckList{}
		if err := c.List(context.Background(), nhcList, &client.ListOptions{}); err != nil {
			logger.Error(err, "mapper: failed to list NHCs")
			return requests
		}

		for _, nhc := range nhcList.Items {
			selector, err := metav1.LabelSelectorAsSelector(&nhc.Spec.Selector)
			if err != nil {
				logger.Error(err, "mapper: invalid node selector", "NHC name", nhc.GetName())
				continue
			}

			if selector.Matches(labels.Set(node.GetLabels())) {
				logger.Info("adding NHC to reconcile queue for handling node", "node", node.GetName(), "NHC", nhc.GetName())
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: nhc.GetName()}})
			}
		}
		return requests
	}
	return delegate
}

// NHCByMHCEventMapperFunc return the MHC-event-to-NHC mapper function
func NHCByMHCEventMapperFunc(c client.Client, logger logr.Logger) handler.MapFunc {
	delegate := func(o client.Object) []reconcile.Request {
		requests := make([]reconcile.Request, 0)
		nhcList := &remediationv1alpha1.NodeHealthCheckList{}
		if err := c.List(context.Background(), nhcList, &client.ListOptions{}); err != nil {
			logger.Error(err, "mapper: failed to list NHCs")
			return requests
		}
		logger.Info("adding all NHCs to reconcile queue for handling MHC event")
		for _, nhc := range nhcList.Items {
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: nhc.GetName()}})
		}
		return requests
	}
	return delegate
}

// NHCByRemediationCRMapperFunc return the RemediationCR-to-NHC mapper function
func NHCByRemediationCRMapperFunc(logger logr.Logger) handler.MapFunc {
	// This closure is meant to get the NHC for the given remediation CR
	delegate := func(o client.Object) []reconcile.Request {
		requests := make([]reconcile.Request, 0)
		for _, owner := range o.GetOwnerReferences() {
			if owner.Kind == "NodeHealthCheck" && owner.APIVersion == remediationv1alpha1.GroupVersion.String() {
				logger.Info("mapper: found NHC for remediation CR", "NHC Name", owner.Name, "Remediation CR Name", o.GetName(), "Remediation CR Kind", o.GetObjectKind().GroupVersionKind().Kind)
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: owner.Name}})
				return requests
			}
		}
		logger.Info("mapper: didn't find NHC for remediation CR", "Remediation CR Name", o.GetName(), "Remediation CR Kind", o.GetObjectKind().GroupVersionKind().Kind)
		return requests
	}
	return delegate
}
