package utils

import (
	"context"

	"github.com/go-logr/logr"

	v1 "k8s.io/api/core/v1"
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
			logger.Error(err, "failed to get node", "node name", o.GetName())
			return requests
		}

		nhcList := &remediationv1alpha1.NodeHealthCheckList{}
		if err := c.List(context.Background(), nhcList, &client.ListOptions{}); err != nil {
			logger.Error(err, "failed to list NHCs")
			return requests
		}

		for _, nhc := range nhcList.Items {
			selector, err := metav1.LabelSelectorAsSelector(&nhc.Spec.Selector)
			if err != nil {
				logger.Error(err, "invalid node selector", "NHC name", nhc.GetName())
				continue
			}

			if selector.Matches(labels.Set(node.GetLabels())) {
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: nhc.GetName()}})
			}
		}
		return requests
	}
	return delegate
}
