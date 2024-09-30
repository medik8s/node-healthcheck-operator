package resources

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	commonannotations "github.com/medik8s/common/pkg/annotations"
	commonevents "github.com/medik8s/common/pkg/events"
	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils/annotations"
)

type Manager interface {
	GetCurrentTemplateWithTimeout(node *corev1.Node, nhc *remediationv1alpha1.NodeHealthCheck) (*unstructured.Unstructured, *time.Duration, error)
	GetTemplate(mhc *machinev1beta1.MachineHealthCheck) (*unstructured.Unstructured, error)
	GenerateTemplate(reference *corev1.ObjectReference) *unstructured.Unstructured
	ValidateTemplates(nhc *remediationv1alpha1.NodeHealthCheck) (valid bool, reason string, message string, err error)
	GenerateRemediationCRBase(gvk schema.GroupVersionKind) *unstructured.Unstructured
	GenerateRemediationCRBaseNamed(gvk schema.GroupVersionKind, namespace string, name string) *unstructured.Unstructured
	GenerateRemediationCRForNode(node *corev1.Node, owner client.Object, template *unstructured.Unstructured) (*unstructured.Unstructured, error)
	GenerateRemediationCRForMachine(machine *machinev1beta1.Machine, owner client.Object, template *unstructured.Unstructured, nodeName string) (*unstructured.Unstructured, error)
	CreateRemediationCR(remediationCR *unstructured.Unstructured, owner client.Object, nodeName *string, currentRemediationDuration, previousRemediationsDuration time.Duration) (bool, *time.Duration, *unstructured.Unstructured, error)
	DeleteRemediationCR(remediationCR *unstructured.Unstructured, owner client.Object) (bool, error)
	UpdateRemediationCR(remediationCR *unstructured.Unstructured) error
	ListRemediationCRs(remediationTemplates []*corev1.ObjectReference, remediationCRFilter func(r unstructured.Unstructured) bool) ([]unstructured.Unstructured, error)
	GetNodes(labelSelector metav1.LabelSelector) ([]corev1.Node, error)
	GetMHCTargets(mhc *machinev1beta1.MachineHealthCheck) ([]Target, error)
	HandleHealthyNode(nodeName string, crName string, owner client.Object) ([]unstructured.Unstructured, error)
	CleanUp(nodeName string) error
}

type RemediationCRNotOwned struct{ msg string }

func (r RemediationCRNotOwned) Error() string { return r.msg }

type manager struct {
	client.Client
	ctx           context.Context
	log           logr.Logger
	hasMachineAPI bool
	leaseManager  LeaseManager
	recorder      record.EventRecorder
}

var _ Manager = &manager{}

func NewManager(c client.Client, ctx context.Context, log logr.Logger, hasMachineAPI bool, leaseManager LeaseManager, recorder record.EventRecorder) Manager {
	return &manager{
		Client:        c,
		ctx:           ctx,
		log:           log.WithName("resource manager"),
		hasMachineAPI: hasMachineAPI,
		leaseManager:  leaseManager,
		recorder:      recorder,
	}
}

func (m *manager) GenerateRemediationCRForNode(node *corev1.Node, owner client.Object, template *unstructured.Unstructured) (*unstructured.Unstructured, error) {

	nhcOwnerRef := createOwnerRef(owner)

	// also set the node's machine as owner ref if possible
	// TODO also handle CAPI clusters / machines
	var machineOwnerRef *metav1.OwnerReference
	if m.hasMachineAPI {
		ref, machineNamespace, err := m.getOwningMachineWithNamespace(node)
		if err != nil {
			return nil, err
		}
		if ref != nil && machineNamespace != "" {
			// Owners must be cluster scoped, or in the same namespace as their dependent.
			// Machines are always namespaced.
			// So setting the machine as owner only works when the machine is in the same template as the remediation CR
			if template.GetNamespace() == machineNamespace {
				machineOwnerRef = ref
			} else {
				// What to do if namespaces don't match?
				// So far this is a known issue for Metal3 remediation only, and that case was checked already
				// in the Reconciler. So ignore, logging it is too verbose.
			}
		}
	}

	return m.generateRemediationCR(node.GetName(), node.GetName(), nhcOwnerRef, machineOwnerRef, template)
}

func (m *manager) GenerateRemediationCRForMachine(machine *machinev1beta1.Machine, owner client.Object, template *unstructured.Unstructured, nodeName string) (*unstructured.Unstructured, error) {

	mhcOwnerRef := createOwnerRef(owner)

	// Owners must be cluster scoped, or in the same namespace as their dependent.
	// Machines are always namespaced.
	// So setting the machine as owner only works when the machine is in the same template as the remediation CR
	var machineOwnerRef *metav1.OwnerReference
	if machine.GetNamespace() == template.GetNamespace() {
		machineOwnerRef = createOwnerRef(machine)
	} else {
		// TODO This should be catched in the Reconciler, similar as NHC already does for Metal3Remediation!
		// So it can be ignored here.
	}

	return m.generateRemediationCR(machine.GetName(), nodeName, mhcOwnerRef, machineOwnerRef, template)
}

func (m *manager) generateRemediationCR(name string, nodeName string, healthCheckOwnerRef *metav1.OwnerReference, machineOwnerRef *metav1.OwnerReference, template *unstructured.Unstructured) (*unstructured.Unstructured, error) {

	remediationCR := m.GenerateRemediationCRBase(template.GroupVersionKind())

	// can't go wrong, we already checked for correct spec
	templateSpec, _, _ := unstructured.NestedMap(template.Object, "spec", "template", "spec")
	unstructured.SetNestedField(remediationCR.Object, templateSpec, "spec")

	// Multiple same kind templates are never supported for MHC, and remediators are not expected to handle generated names in this case, even if they do for NHC.
	isMHCRemediation := name != nodeName
	if annotations.HasMultipleTemplatesAnnotation(template) && !isMHCRemediation {
		remediationCR.SetGenerateName(fmt.Sprintf("%s-", name))
	} else {
		remediationCR.SetName(name)
	}
	remediationCR.SetAnnotations(map[string]string{commonannotations.NodeNameAnnotation: nodeName, annotations.TemplateNameAnnotation: template.GetName()})

	remediationCR.SetNamespace(template.GetNamespace())
	remediationCR.SetResourceVersion("")
	remediationCR.SetFinalizers(nil)
	remediationCR.SetUID("")
	remediationCR.SetSelfLink("")
	remediationCR.SetCreationTimestamp(metav1.Now())

	owners := make([]metav1.OwnerReference, 0)
	if healthCheckOwnerRef != nil {
		owners = append(owners, *healthCheckOwnerRef)
		remediationCR.SetLabels(map[string]string{
			"app.kubernetes.io/part-of": "node-healthcheck-controller",
		})
	}
	if machineOwnerRef != nil {
		owners = append(owners, *machineOwnerRef)
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

// CreateRemediationCR creates the given remediation CR from remediationCR it'll return: a bool indicator of success, a *time.Duration an indicator on when requeue is needed in order to extend the lease, a *unstructured.Unstructured of the created/existing CR and an error
func (m *manager) CreateRemediationCR(remediationCR *unstructured.Unstructured, owner client.Object, nodeName *string, currentRemediationDuration, previousRemediationsDuration time.Duration) (bool, *time.Duration, *unstructured.Unstructured, error) {
	var err error
	if remediationCR.GetAnnotations() == nil || len(remediationCR.GetAnnotations()[commonannotations.NodeNameAnnotation]) == 0 {
		err = m.Get(m.ctx, client.ObjectKeyFromObject(remediationCR), remediationCR)
	} else {
		remediationCR, err = m.getCRWithNodeNameAnnotation(remediationCR)
	}

	// check if CR already exists
	if err == nil {
		if !IsOwner(remediationCR, owner) {
			m.log.Info("external remediation CR already exists, but it's not owned by us", "CR name", remediationCR.GetName(), "kind", remediationCR.GetKind(), "namespace", remediationCR.GetNamespace(), "owners", remediationCR.GetOwnerReferences())
			return false, nil, remediationCR, RemediationCRNotOwned{msg: "CR exists but isn't owned by current NHC"}
		}
		m.log.Info("external remediation CR already exists", "CR name", remediationCR.GetName(), "kind", remediationCR.GetKind(), "namespace", remediationCR.GetNamespace())
		if nodeName == nil {
			// we can't create a node lease, there is no known node (e.g. for failed Machines)
			return false, nil, remediationCR, nil
		}
		duration, err := m.leaseManager.ManageLease(m.ctx, *nodeName, currentRemediationDuration, previousRemediationsDuration)
		return false, &duration, remediationCR, err
	} else if !apierrors.IsNotFound(err) {
		m.log.Error(err, "failed to check for existing external remediation object")
		return false, nil, remediationCR, err
	}

	var requeue *time.Duration
	if nodeName != nil {
		m.log.Info("Attempting to obtain Node Lease", "Node name", *nodeName)
		var err error
		requeue, err = m.leaseManager.ObtainNodeLease(m.ctx, *nodeName, currentRemediationDuration)
		if err != nil {
			return false, requeue, remediationCR, err
		}
	}

	// create CR
	m.log.Info("Creating a remediation CR",
		"CR name", remediationCR.GetName(),
		"CR kind", remediationCR.GetKind(),
		"namespace", remediationCR.GetNamespace())

	if err := m.Create(m.ctx, remediationCR); err != nil {
		m.log.Error(err, "failed to create an external remediation object")
		return false, nil, remediationCR, err
	}

	return true, requeue, remediationCR, nil

}

func (m *manager) DeleteRemediationCR(remediationCR *unstructured.Unstructured, owner client.Object) (isDeleted bool, errResult error) {
	err := m.Get(m.ctx, client.ObjectKeyFromObject(remediationCR), remediationCR)
	if err != nil && !apierrors.IsNotFound(err) {
		// something went wrong
		return false, errors.Wrapf(err, "failed to get remediation CR")
	} else if apierrors.IsNotFound(err) || remediationCR.GetDeletionTimestamp() != nil {
		// CR does not exist or is already deleted
		// nothing to do
		return false, nil
	}

	// also check if this is our CR
	if !IsOwner(remediationCR, owner) {
		return false, nil
	}

	err = m.Delete(m.ctx, remediationCR, &client.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}
	commonevents.NormalEventf(m.recorder, owner, utils.EventReasonRemediationRemoved, "Deleted remediation CR of kind %s with name %s", remediationCR.GetKind(), remediationCR.GetName())
	return true, nil
}

func (m *manager) UpdateRemediationCR(remediationCR *unstructured.Unstructured) error {
	return m.Update(m.ctx, remediationCR)
}

func (m *manager) ListRemediationCRs(remediationTemplates []*corev1.ObjectReference, remediationCRFilter func(r unstructured.Unstructured) bool) ([]unstructured.Unstructured, error) {
	// get CRs
	remediationCRs := make([]unstructured.Unstructured, 0)
	for _, template := range remediationTemplates {
		baseRemediationCR := m.GenerateRemediationCRBase(template.GroupVersionKind())
		crList := &unstructured.UnstructuredList{Object: baseRemediationCR.Object}

		if err := m.List(m.ctx, crList); err != nil && !apierrors.IsNotFound(err) {
			return nil, errors.Wrapf(err,
				"failed to get all remediation objects with kind %s and apiVersion %s",
				baseRemediationCR.GroupVersionKind(),
				baseRemediationCR.GetAPIVersion())
		} else {
			for _, cr := range crList.Items {
				if m.isMatchNodeTemplate(cr, utils.GetNodeNameFromCR(cr), template.Name) {
					remediationCRs = append(remediationCRs, cr)
				}
			}
		}
	}

	// apply filter
	matches := make([]unstructured.Unstructured, 0)
	for _, cr := range remediationCRs {
		if remediationCRFilter(cr) {
			matches = append(matches, cr)
		}
	}
	return matches, nil
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

func IsOwner(remediationCR *unstructured.Unstructured, owner client.Object) bool {
	apiVersion, kind := owner.GetObjectKind().GroupVersionKind().ToAPIVersionAndKind()
	for _, ownerRef := range remediationCR.GetOwnerReferences() {
		if ownerRef.Kind == kind && ownerRef.APIVersion == apiVersion && ownerRef.Name == owner.GetName() {
			return true
		}
	}
	return false
}

func (m *manager) HandleHealthyNode(nodeName string, crName string, owner client.Object) ([]unstructured.Unstructured, error) {
	remediationCRs, err := m.ListRemediationCRs(utils.GetAllRemediationTemplates(owner), func(cr unstructured.Unstructured) bool {
		return (cr.GetName() == crName || utils.GetNodeNameFromCR(cr) == nodeName) && IsOwner(&cr, owner)
	})
	if err != nil {
		m.log.Error(err, "failed to get remediation CRs for healthy node", "node", nodeName)
		return remediationCRs, err
	}

	if len(remediationCRs) == 0 {
		// when all CRs are gone, the node is considered healthy
		if err = m.CleanUp(nodeName); err != nil {
			m.log.Error(err, "failed to handle healthy node", "node", nodeName)
			return remediationCRs, err
		}
		return remediationCRs, nil
	}

	for _, cr := range remediationCRs {
		if deleted, err := m.DeleteRemediationCR(&cr, owner); err != nil {
			m.log.Error(err, "failed to delete remediation CR", "name", cr.GetName())
			return remediationCRs, err
		} else if deleted {
			m.log.Info("deleted remediation CR", "name", cr.GetName())
		}
	}

	return remediationCRs, nil
}

func (m *manager) CleanUp(nodeName string) error {
	return m.leaseManager.InvalidateLease(m.ctx, nodeName)
}

func (m *manager) getOwningMachineWithNamespace(node *corev1.Node) (*metav1.OwnerReference, string, error) {
	ns, name, err := utils.GetMachineNamespaceName(node)
	if err != nil {
		if errors.Is(err, utils.MachineAnnotationNotFoundError) {
			m.log.Info("didn't find machine annotation for Openshift machine", "node", node.GetName())
			// nothing we can do, continue without owning machine
			return nil, "", nil
		}
		return nil, "", err
	}
	machine := &machinev1beta1.Machine{}
	if err := m.Get(m.ctx, client.ObjectKey{Namespace: ns, Name: name}, machine); err != nil {
		return nil, "", errors.Wrapf(err, "failed to get machine. namespace %v, name: %v", ns, name)
	}
	return createOwnerRef(machine), ns, nil
}

func (m *manager) getCRWithNodeNameAnnotation(remediationCR *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	nodeName := remediationCR.GetAnnotations()[commonannotations.NodeNameAnnotation]
	templateName := remediationCR.GetAnnotations()[annotations.TemplateNameAnnotation]

	resourceList := &unstructured.UnstructuredList{Object: m.GenerateRemediationCRBase(remediationCR.GroupVersionKind()).Object}
	if err := m.List(m.ctx, resourceList); err == nil {
		for _, cr := range resourceList.Items {
			if m.isMatchNodeTemplate(cr, nodeName, templateName) {
				return &cr, nil
			}
		}
	} else {
		m.log.Error(err, "failed fetching CRs List")
		return nil, err
	}
	return remediationCR, apierrors.NewNotFound(
		schema.GroupResource(metav1.GroupResource{Group: remediationCR.GroupVersionKind().Group, Resource: remediationCR.GetKind()}),
		remediationCR.GetName(),
	)
}

func (m *manager) isMatchNodeTemplate(cr unstructured.Unstructured, nodeName string, templateName string) bool {
	if cr.GetAnnotations() == nil {
		return cr.GetName() == nodeName
	}
	ann := cr.GetAnnotations()
	if _, isMultiSupported := ann[annotations.TemplateNameAnnotation]; !isMultiSupported {
		return cr.GetName() == nodeName
	}
	return ann[annotations.TemplateNameAnnotation] == templateName && ann[commonannotations.NodeNameAnnotation] == nodeName
}

func createOwnerRef(obj client.Object) *metav1.OwnerReference {
	gvk := obj.GetObjectKind().GroupVersionKind()
	apiVersion, kind := gvk.ToAPIVersionAndKind()
	return &metav1.OwnerReference{
		APIVersion:         apiVersion,
		Kind:               kind,
		Name:               obj.GetName(),
		UID:                obj.GetUID(),
		Controller:         pointer.Bool(false),
		BlockOwnerDeletion: nil,
	}
}
