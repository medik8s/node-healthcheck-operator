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
	"github.com/medik8s/node-healthcheck-operator/controllers/featuregates"
)

const (
	MachineNodeNameIndex = "machineNodeNameIndex"
)

// WatchType is used to differentiate watch CR logic between NHC and MHC.
type WatchType string

const (
	NHC WatchType = "NHC"
	MHC WatchType = "MHC"
)

// NHCByNodeMapperFunc return the Node-to-NHC mapper function
func NHCByNodeMapperFunc(c client.Client, logger logr.Logger) handler.MapFunc {
	// This closure is meant to fetch all NHC to fill the reconcile queue.
	// If we have multiple nhc then it is possible that we fetch nhc objects that
	// are unrelated to this node. Its even possible that the node still doesn't
	// have the right labels set to be picked up by the nhc selector.
	delegate := func(ctx context.Context, o client.Object) []reconcile.Request {
		requests := make([]reconcile.Request, 0)

		node := &v1.Node{}
		if err := c.Get(ctx, client.ObjectKey{Name: o.GetName()}, node); err != nil {
			if !errors.IsNotFound(err) {
				logger.Error(err, "mapper: failed to get node", "node name", o.GetName())
			}
			node = nil
		}

		nhcList := &remediationv1alpha1.NodeHealthCheckList{}
		if err := c.List(ctx, nhcList, &client.ListOptions{}); err != nil {
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
	delegate := func(ctx context.Context, o client.Object) []reconcile.Request {
		requests := make([]reconcile.Request, 0)
		nhcList := &remediationv1alpha1.NodeHealthCheckList{}
		if err := c.List(ctx, nhcList, &client.ListOptions{}); err != nil {
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

// RemediationCRMapperFunc return the RemediationCR-to-NHC/MHC mapper function
func RemediationCRMapperFunc(logger logr.Logger, watchType WatchType) handler.MapFunc {
	// This closure is meant to get the NHC for the given remediation CR
	delegate := func(ctx context.Context, o client.Object) []reconcile.Request {
		requests := make([]reconcile.Request, 0)
		for _, owner := range o.GetOwnerReferences() {
			logger.Info("Request info", "owner ref", owner)
			if isCrNeedWatching(owner, watchType) {
				logger.Info(fmt.Sprintf("mapper: found %s for remediation CR", watchType), fmt.Sprintf("%s Name", watchType), owner.Name, "Remediation CR Name", o.GetName(), "Remediation CR Kind", o.GetObjectKind().GroupVersionKind().Kind)
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: owner.Name}})
				return requests
			}
		}
		logger.Info(fmt.Sprintf("mapper: didn't find %s for remediation CR", watchType), "Remediation CR Name", o.GetName(), "Remediation CR Kind", o.GetObjectKind().GroupVersionKind().Kind)
		return requests
	}
	return delegate
}

func isCrNeedWatching(owner metav1.OwnerReference, watchType WatchType) bool {
	if watchType == NHC {
		return owner.Kind == "NodeHealthCheck" && owner.APIVersion == remediationv1alpha1.GroupVersion.String()
	} else {
		return owner.Kind == "Machine" && owner.APIVersion == machinev1beta1.GroupVersion.String()
	}
}

// NHCByRemediationTemplateCRMapperFunc return the RemediationTemplateCR-to-NHC mapper function
func NHCByRemediationTemplateCRMapperFunc(c client.Client, logger logr.Logger) handler.MapFunc {
	// This closure is meant to get the NHC for the given remediation template CR
	delegate := func(ctx context.Context, o client.Object) []reconcile.Request {
		requests := make([]reconcile.Request, 0)

		nhcList := &remediationv1alpha1.NodeHealthCheckList{}
		if err := c.List(ctx, nhcList, &client.ListOptions{}); err != nil {
			logger.Error(err, "mapper: failed to list NHCs")
			return requests
		}

		templateMatches := func(nhcTemplate v1.ObjectReference) bool {
			return nhcTemplate.Kind == o.GetObjectKind().GroupVersionKind().Kind && nhcTemplate.Name == o.GetName()
		}

		for _, nhc := range nhcList.Items {
			match := false
			if nhc.Spec.RemediationTemplate != nil {
				match = templateMatches(*nhc.Spec.RemediationTemplate)
			} else {
				for _, template := range nhc.Spec.EscalatingRemediations {
					if templateMatches(template.RemediationTemplate) {
						match = true
						break
					}
				}
			}
			if match {
				logger.Info("adding NHC to reconcile queue for handling remediation template", "template", o.GetName(), "NHC", nhc.GetName())
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: nhc.GetName()}})
			}
		}
		return requests

	}
	return delegate
}

// MHCByRemediationTemplateCRMapperFunc return the RemediationTemplateCR-to-MHC mapper function
func MHCByRemediationTemplateCRMapperFunc(c client.Client, logger logr.Logger) handler.MapFunc {
	// This closure is meant to get the MHC for the given remediation template CR
	delegate := func(ctx context.Context, o client.Object) []reconcile.Request {
		requests := make([]reconcile.Request, 0)

		mhcList := &machinev1beta1.MachineHealthCheckList{}
		if err := c.List(ctx, mhcList, &client.ListOptions{}); err != nil {
			logger.Error(err, "mapper: failed to list MHCs")
			return requests
		}

		templateMatches := func(mhcTemplate v1.ObjectReference) bool {
			return mhcTemplate.Kind == o.GetObjectKind().GroupVersionKind().Kind && mhcTemplate.Name == o.GetName()
		}

		for _, mhc := range mhcList.Items {
			match := false
			if mhc.Spec.RemediationTemplate != nil {
				match = templateMatches(*mhc.Spec.RemediationTemplate)
			}
			if match {
				logger.Info("adding MHC to reconcile queue for handling remediation template", "template", o.GetName(), "MHC", mhc.GetName())
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: mhc.GetName()}})
			}
		}
		return requests

	}
	return delegate
}

// MHCByNodeMapperFunc return the Node-to-MHC mapper function
func MHCByNodeMapperFunc(c client.Client, logger logr.Logger, featureGates featuregates.Accessor) handler.MapFunc {
	delegate := func(ctx context.Context, o client.Object) []reconcile.Request {
		requests := make([]reconcile.Request, 0)

		// only queue MHCs if the MAO MHC controller is disabled
		if !featureGates.IsMachineAPIOperatorMHCDisabled() {
			return requests
		}

		node := &v1.Node{}
		if err := c.Get(ctx, client.ObjectKey{Name: o.GetName()}, node); err != nil {
			if errors.IsNotFound(err) {
				node = &v1.Node{}
				node.Name = o.GetName()
				logger.Info("mapping deleted node", "node name", o.GetName())
			} else {
				logger.Error(err, "failed to get node", "node name", o.GetName())
				return requests
			}
		}

		machine, err := getMachineFromNode(ctx, c, node.Name)
		if err != nil {
			logger.Error(err, "No-op: Unable to retrieve machine from node", "node", node.Name)
			return requests
		}

		mhcList := &machinev1beta1.MachineHealthCheckList{}
		if err := c.List(ctx, mhcList); err != nil {
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
func MHCByMachineMapperFunc(c client.Client, logger logr.Logger, featureGates featuregates.Accessor) handler.MapFunc {
	delegate := func(ctx context.Context, o client.Object) []reconcile.Request {
		requests := make([]reconcile.Request, 0)

		// only queue MHCs if the MAO MHC controller is disabled
		if !featureGates.IsMachineAPIOperatorMHCDisabled() {
			return requests
		}

		machine := &machinev1beta1.Machine{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: o.GetNamespace(), Name: o.GetName()}, machine); err != nil {
			logger.Error(err, "failed to get machine", "namespace", o.GetNamespace(), "name", o.GetName())
			return requests
		}

		mhcList := &machinev1beta1.MachineHealthCheckList{}
		if err := c.List(ctx, mhcList); err != nil {
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

// MHCByFeatureGateEventMapperFunc return the FeatureGateEvent-to-NHC mapper function
func MHCByFeatureGateEventMapperFunc(c client.Client, logger logr.Logger, featureGates featuregates.Accessor) handler.MapFunc {
	delegate := func(ctx context.Context, o client.Object) []reconcile.Request {
		requests := make([]reconcile.Request, 0)

		// only queue MHCs if the MAO MHC controller is disabled
		if !featureGates.IsMachineAPIOperatorMHCDisabled() {
			return requests
		}

		mhcList := &machinev1beta1.MachineHealthCheckList{}
		if err := c.List(ctx, mhcList); err != nil {
			logger.Error(err, "No-op: Unable to list mhc")
			return requests
		}
		logger.Info("adding all MHCs to reconcile queue for handling feature gate event")
		for _, mhc := range mhcList.Items {
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: mhc.GetNamespace(),
				Name:      mhc.GetName(),
			}})
		}
		return requests
	}
	return delegate
}

func getMachineFromNode(ctx context.Context, c client.Client, nodeName string) (*machinev1beta1.Machine, error) {
	machineList := &machinev1beta1.MachineList{}
	if err := c.List(ctx, machineList, client.MatchingFields{MachineNodeNameIndex: nodeName}); err != nil {
		return nil, fmt.Errorf("failed getting machine list: %v", err)
	}
	if len(machineList.Items) != 1 {
		return nil, fmt.Errorf("expecting one machine for node %v, got: %v", nodeName, machineList.Items)
	}
	return &machineList.Items[0], nil
}
