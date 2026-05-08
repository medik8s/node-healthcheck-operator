package resources

import (
	"fmt"

	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
)

type Target struct {
	Machine *machinev1beta1.Machine
	Node    *corev1.Node
	MHC     *machinev1beta1.MachineHealthCheck
}

func (t *Target) String() string {
	return fmt.Sprintf("%s/%s/%s/%s",
		t.MHC.GetNamespace(),
		t.MHC.GetName(),
		t.Machine.GetName(),
		t.nodeName(),
	)
}

func (t *Target) nodeName() string {
	if t.Node != nil {
		return t.Node.GetName()
	}
	return ""
}

func (m *manager) GetMHCTargets(mhc *machinev1beta1.MachineHealthCheck) ([]Target, error) {

	machines, err := m.getMachines(mhc)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get machines for selector")
	}
	if len(machines) == 0 {
		return nil, nil
	}

	var targets []Target
	for _, machine := range machines {
		machine := machine
		t := Target{
			MHC:     mhc,
			Machine: &machine,
		}
		node, err := m.getNodeFromMachine(machine)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, errors.Wrapf(err, "failed to get node for machine")
			}
			// a node with only a name represents a
			// not found node in the target, while a nil node
			// means that no noderef was set yet
			node.Name = machine.Status.NodeRef.Name
		}
		t.Node = node
		targets = append(targets, t)
	}

	return targets, nil
}

func (m *manager) getMachines(mhc *machinev1beta1.MachineHealthCheck) ([]machinev1beta1.Machine, error) {
	var machines machinev1beta1.MachineList
	selector, err := metav1.LabelSelectorAsSelector(&mhc.Spec.Selector)
	if err != nil {
		err = errors.Wrapf(err, "failed converting a selector from MHC selector")
		return []machinev1beta1.Machine{}, err
	}
	options := &client.ListOptions{
		LabelSelector: selector,
		Namespace:     mhc.GetNamespace(),
	}
	err = m.List(m.ctx, &machines, options)
	return machines.Items, err
}

func (m *manager) getNodeFromMachine(machine machinev1beta1.Machine) (*corev1.Node, error) {
	if machine.Status.NodeRef == nil {
		return nil, nil
	}

	node := &corev1.Node{}
	nodeKey := types.NamespacedName{
		Name: machine.Status.NodeRef.Name,
	}
	err := m.Get(m.ctx, nodeKey, node)
	return node, err
}
