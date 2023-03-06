package resources

import (
	"context"
	"strings"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

const (
	templateSuffix    = "Template"
	machineAnnotation = "machine.openshift.io/machine"
)

type Manager interface {
	GetTemplate(templateRef *corev1.ObjectReference) (*unstructured.Unstructured, error)
	ValidateTemplates(nhc *remediationv1alpha1.NodeHealthCheck) (valid bool, reason string, message string, err error)
	GenerateRemediationCRBase(gvk schema.GroupVersionKind) *unstructured.Unstructured
	GenerateRemediationCRBaseNamed(gvk schema.GroupVersionKind, namespace string, name string) *unstructured.Unstructured
	GenerateRemediationCR(node *corev1.Node, nhc *remediationv1alpha1.NodeHealthCheck, template *unstructured.Unstructured) (*unstructured.Unstructured, error)
	CreateRemediationCR(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) (bool, error)
	DeleteRemediationCR(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) (bool, error)
	GetNodes(labelSelector metav1.LabelSelector) ([]corev1.Node, error)
	GetOwnedInflightRemediations(nhc *remediationv1alpha1.NodeHealthCheck) (map[string]metav1.Time, error)
	GetAllInflightRemediations(nhc *remediationv1alpha1.NodeHealthCheck) ([]unstructured.Unstructured, error)
}

type manager struct {
	client.Client
	ctx         context.Context
	log         logr.Logger
	onOpenshift bool
}

var _ Manager = &manager{}

func NewManager(c client.Client, ctx context.Context, log logr.Logger, onOpenshift bool) Manager {
	return &manager{
		Client:      c,
		ctx:         ctx,
		log:         log.WithName("resource manager"),
		onOpenshift: onOpenshift,
	}
}

func (m *manager) GenerateRemediationCR(node *corev1.Node, nhc *remediationv1alpha1.NodeHealthCheck, template *unstructured.Unstructured) (*unstructured.Unstructured, error) {

	remediationCR := m.GenerateRemediationCRBase(template.GroupVersionKind())

	// can't go wrong, we already checked for correct spec
	templateSpec, _, _ := unstructured.NestedMap(template.Object, "spec", "template", "spec")
	unstructured.SetNestedField(remediationCR.Object, templateSpec, "spec")

	remediationCR.SetName(node.Name)
	remediationCR.SetNamespace(template.GetNamespace())
	remediationCR.SetResourceVersion("")
	remediationCR.SetFinalizers(nil)
	remediationCR.SetUID("")
	remediationCR.SetSelfLink("")
	remediationCR.SetCreationTimestamp(metav1.Now())

	owners := make([]metav1.OwnerReference, 0)
	if nhc != nil {
		owners = append(owners, metav1.OwnerReference{
			APIVersion:         nhc.APIVersion,
			Kind:               nhc.Kind,
			Name:               nhc.Name,
			UID:                nhc.UID,
			Controller:         pointer.Bool(false),
			BlockOwnerDeletion: nil,
		})
		remediationCR.SetLabels(map[string]string{
			"app.kubernetes.io/part-of": "node-healthcheck-controller",
		})
	}

	// TODO also handle CAPI clusters / machines
	if m.onOpenshift {
		machineRef, machineNamespace, err := m.getOwningMachineWithNamespace(node)
		if err != nil {
			return nil, err
		}
		if machineRef != nil && machineNamespace != "" {
			// Owners must be cluster scoped, or in the same namespace as their dependent
			// Machines are always namespaced
			if remediationCR.GetNamespace() == machineNamespace {
				owners = append(owners, *machineRef)
			} else {
				// What to do if namespaces don't match?
				// So far this is a known issue for Metal3 remediation only, and that case was checked already
				// in the Reconciler. So just log it, but do not fail remediation.
				m.log.Info("Not setting remediation CR's owner ref to the machine, because namespaces don't match",
					"template ns", remediationCR.GetNamespace(),
					"machine ns", machineNamespace)
			}
		}
	}

	if len(owners) > 0 {
		remediationCR.SetOwnerReferences(owners)
	}

	return remediationCR, nil
}

func (m *manager) GenerateRemediationCRBaseNamed(gvk schema.GroupVersionKind, namespace string, name string) *unstructured.Unstructured {
	remediationCR := m.GenerateRemediationCRBase(gvk)
	remediationCR.SetName(name)
	remediationCR.SetNamespace(namespace)
	return remediationCR
}

func (m *manager) GenerateRemediationCRBase(gvk schema.GroupVersionKind) *unstructured.Unstructured {
	remediationCRBase := &unstructured.Unstructured{}
	remediationCRBase.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    strings.TrimSuffix(gvk.Kind, templateSuffix),
	})
	return remediationCRBase
}

func (m *manager) CreateRemediationCR(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) (bool, error) {
	// check if CR already exists
	if err := m.Get(m.ctx, client.ObjectKeyFromObject(remediationCR), remediationCR); err == nil {
		if !IsOwner(remediationCR, nhc) {
			m.log.Info("external remediation CR already exists, but it's not owned by us", "owners", remediationCR.GetOwnerReferences())
		} else {
			m.log.Info("external remediation CR already exists")
		}
		return false, nil
	} else if !apierrors.IsNotFound(err) {
		m.log.Error(err, "failed to check for existing external remediation object")
		return false, err
	}

	// create CR
	m.log.Info("Creating an remediation CR",
		"CR Name", remediationCR.GetName(),
		"CR KVK", remediationCR.GroupVersionKind(),
		"namespace", remediationCR.GetNamespace())

	if err := m.Create(m.ctx, remediationCR); err != nil {
		m.log.Error(err, "failed to create an external remediation object")
		return false, err
	}
	return true, nil
}

func (m *manager) DeleteRemediationCR(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) (bool, error) {

	err := m.Get(context.Background(), client.ObjectKeyFromObject(remediationCR), remediationCR)
	if err != nil && !apierrors.IsNotFound(err) {
		// something went wrong
		return false, errors.Wrapf(err, "failed to get remediation CR")
	} else if apierrors.IsNotFound(err) || remediationCR.GetDeletionTimestamp() != nil {
		// CR does not exist or is already deleted
		// nothing to do
		return false, nil
	}

	// also check if this is our CR
	if !IsOwner(remediationCR, nhc) {
		return false, nil
	}

	err = m.Delete(context.Background(), remediationCR, &client.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}
	return true, nil
}

func (m *manager) GetNodes(labelSelector metav1.LabelSelector) ([]corev1.Node, error) {
	var nodes corev1.NodeList
	selector, err := metav1.LabelSelectorAsSelector(&labelSelector)
	if err != nil {
		err = errors.Wrapf(err, "failed converting a selector from NHC selector")
		return []corev1.Node{}, err
	}
	err = m.List(m.ctx, &nodes, &client.ListOptions{LabelSelector: selector})
	return nodes.Items, err
}

func (m *manager) GetOwnedInflightRemediations(nhc *remediationv1alpha1.NodeHealthCheck) (map[string]metav1.Time, error) {
	all, err := m.GetAllInflightRemediations(nhc)
	if err != nil {
		return nil, err
	}
	owned := make(map[string]metav1.Time)
	for _, remediationCR := range all {
		if IsOwner(&remediationCR, nhc) {
			owned[remediationCR.GetName()] = remediationCR.GetCreationTimestamp()
		}
	}
	return owned, nil
}

func (m *manager) GetAllInflightRemediations(nhc *remediationv1alpha1.NodeHealthCheck) ([]unstructured.Unstructured, error) {
	baseRemediationCR := m.GenerateRemediationCRBase(nhc.Spec.RemediationTemplate.GroupVersionKind())
	crList := &unstructured.UnstructuredList{Object: baseRemediationCR.Object}
	err := m.List(m.ctx, crList)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, errors.Wrapf(err,
			"failed to get all remediation objects with kind %s and apiVersion %s",
			baseRemediationCR.GroupVersionKind(),
			baseRemediationCR.GetAPIVersion())
	}
	return crList.Items, nil
}

func IsOwner(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) bool {
	for _, owner := range remediationCR.GetOwnerReferences() {
		if owner.Kind == nhc.Kind && owner.APIVersion == nhc.APIVersion && owner.Name == nhc.Name {
			return true
		}
	}
	return false
}

func (m *manager) getOwningMachineWithNamespace(node *corev1.Node) (*metav1.OwnerReference, string, error) {
	// TODO this is Openshift / MachineAPI specific
	// TODO add support for upstream CAPI machines
	namespacedMachine, exists := node.GetAnnotations()[machineAnnotation]
	if !exists {
		m.log.Info("didn't find machine annotation for Openshift machine", "node", node.GetName())
		// nothing we can do, continue without owning machine
		return nil, "", nil
	}
	ns, name, err := cache.SplitMetaNamespaceKey(namespacedMachine)
	if err != nil {
		return nil, "", errors.Wrapf(err, "failed to split machine annotation value into namespace + name: %v", namespacedMachine)
	}
	machine := &machinev1beta1.Machine{}
	if err := m.Get(m.ctx, client.ObjectKey{Namespace: ns, Name: name}, machine); err != nil {
		return nil, "", errors.Wrapf(err, "failed to get machine. namespace %v, name: %v", ns, name)
	}
	return &metav1.OwnerReference{
		APIVersion:         machine.APIVersion,
		Kind:               machine.Kind,
		Name:               name,
		UID:                machine.UID,
		Controller:         pointer.Bool(false),
		BlockOwnerDeletion: pointer.Bool(false),
	}, ns, nil
}
