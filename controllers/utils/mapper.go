package utils

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

const (
	MachineNodeNameIndex = "machineNodeNameIndex"
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
			node = nil
		}

		nhcList := &remediationv1alpha1.NodeHealthCheckList{}
		if err := c.List(context.Background(), nhcList, &client.ListOptions{}); err != nil {
			logger.Error(err, "mapper: failed to list NHCs")
			return requests
		}

		for _, nhc := range nhcList.Items {
			// when node is nil, it was deleted, and we need to queue all NHCs
			if node != nil {
				selector, err := metav1.LabelSelectorAsSelector(&nhc.Spec.Selector)
				if err != nil {
					logger.Error(err, "mapper: invalid node selector", "NHC name", nhc.GetName())
					continue
				}
				if !selector.Matches(labels.Set(node.GetLabels())) {
					continue
				}
			}
			logger.Info("adding NHC to reconcile queue for handling node", "node", o.GetName(), "NHC", nhc.GetName())
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: nhc.GetName()}})
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

// MHCByNodeMapperFunc return the Node-to-MHC mapper function
func MHCByNodeMapperFunc(c client.Client, logger logr.Logger) handler.MapFunc {
	delegate := func(o client.Object) []reconcile.Request {
		requests := make([]reconcile.Request, 0)

		node := &v1.Node{}
		if err := c.Get(context.Background(), client.ObjectKey{Name: o.GetName()}, node); err != nil {
			if !errors.IsNotFound(err) {
				node.Name = o.GetName()
				logger.Info("mapping deleted node", "node name", o.GetName())
			} else {
				logger.Error(err, "failed to get node", "node name", o.GetName())
				return requests
			}
		}

		machine, err := getMachineFromNode(c, node.Name)
		if machine == nil || err != nil {
			logger.Info("No-op: Unable to retrieve machine from node %q: %v", node.Name, err)
			return requests
		}

		mhcList := &machinev1beta1.MachineHealthCheckList{}
		if err := c.List(context.Background(), mhcList); err != nil {
			logger.Error(err, "No-op: Unable to list mhc")
			return requests
		}

		for _, mhc := range mhcList.Items {
			selector, err := metav1.LabelSelectorAsSelector(&mhc.Spec.Selector)
			if err != nil {
				logger.Error(err, "mapper: invalid machine selector", "MHC name", mhc.GetName())
				continue
			}

			// If the selector is empty, all machines are considered to match
			if selector.Empty() || selector.Matches(labels.Set(machine.GetLabels())) {
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
					Namespace: mhc.GetNamespace(),
					Name:      mhc.GetName(),
				}})
			}
		}
		return requests
	}
	return delegate
}

// MHCByMachineMapperFunc return the Machine-to-MHC mapper function
func MHCByMachineMapperFunc(c client.Client, logger logr.Logger) handler.MapFunc {
	delegate := func(o client.Object) []reconcile.Request {
		requests := make([]reconcile.Request, 0)

		machine := &machinev1beta1.Machine{}
		if err := c.Get(context.Background(), client.ObjectKey{Namespace: o.GetNamespace(), Name: o.GetName()}, machine); err != nil {
			logger.Error(err, "failed to get machine", "namespace", o.GetNamespace(), "name", o.GetName())
			return requests
		}

		mhcList := &machinev1beta1.MachineHealthCheckList{}
		if err := c.List(context.Background(), mhcList); err != nil {
			logger.Error(err, "No-op: Unable to list mhc")
			return requests
		}

		for _, mhc := range mhcList.Items {
			selector, err := metav1.LabelSelectorAsSelector(&mhc.Spec.Selector)
			if err != nil {
				logger.Error(err, "mapper: invalid machine selector", "MHC name", mhc.GetName())
				continue
			}

			// If the selector is empty, all machines are considered to match
			if selector.Empty() || selector.Matches(labels.Set(machine.GetLabels())) {
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
					Namespace: mhc.GetNamespace(),
					Name:      mhc.GetName(),
				}})
			}
		}
		return requests
	}
	return delegate
}

func getMachineFromNode(c client.Client, nodeName string) (*machinev1beta1.Machine, error) {
	machineList := &machinev1beta1.MachineList{}
	if err := c.List(
		context.TODO(),
		machineList,
		client.MatchingFields{MachineNodeNameIndex: nodeName},
	); err != nil {
		return nil, fmt.Errorf("failed getting machine list: %v", err)
	}
	if len(machineList.Items) != 1 {
		return nil, fmt.Errorf("expecting one machine for node %v, got: %v", nodeName, machineList.Items)
	}
	return &machineList.Items[0], nil
}
