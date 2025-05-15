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

const (
	HealthyDelayContextKey = "healthyDelay"
	// RemediationHealthyDelayAnnotationKey annotation storing the time in minutes to postponed node regaining health
	RemediationHealthyDelayAnnotationKey = "remediation.medik8s.io/healthy-delay"
	// RemediationManuallyConfirmedHealthyAnnotationKey annotation is placed by the user on the node to indelicate a node is healthy, it's relevant when Healthy Delay is applied
	RemediationManuallyConfirmedHealthyAnnotationKey = "remediation.medik8s.io/manually-confirmed-healthy"
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
	HandleHealthyNode(nodeName string, crName string, owner client.Object) ([]unstructured.Unstructured, *time.Duration, error)
	CleanUp(nodeName string, isManuallyConfirmedHealthy bool) error
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

func (m *manager) HandleHealthyNode(nodeName string, crName string, owner client.Object) ([]unstructured.Unstructured, *time.Duration, error) {
	remediationCRs, err := m.ListRemediationCRs(utils.GetAllRemediationTemplates(owner), func(cr unstructured.Unstructured) bool {
		return (cr.GetName() == crName || utils.GetNodeNameFromCR(cr) == nodeName) && IsOwner(&cr, owner)
	})
	if err != nil {
		m.log.Error(err, "failed to get remediation CRs for healthy node", "node", nodeName)
		return remediationCRs, nil, err
	}

	isManuallyConfirmedHealthy, err := m.isManuallyConfirmedHealthyAnnotationSet(nodeName)
	if err != nil {
		m.log.Error(err, fmt.Sprintf("failed to check whether node was set with %s annotation", RemediationManuallyConfirmedHealthyAnnotationKey), "node", nodeName)
		return remediationCRs, nil, err
	}

	if len(remediationCRs) == 0 {
		// when all CRs are gone, the node is considered healthy
		if err = m.CleanUp(nodeName, isManuallyConfirmedHealthy); err != nil {
			m.log.Error(err, "failed to handle healthy node", "node", nodeName)
			return remediationCRs, nil, err
		}

		return remediationCRs, nil, nil
	}
	var requeueAfter *time.Duration

	for _, cr := range remediationCRs {
		shouldDelete := true
		if !isManuallyConfirmedHealthy {
			crCalculatedDelay, err := m.calcCrDeletionDelay(cr)
			if err != nil {
				m.log.Error(err, "failed to check whether remediation deletion should be delayed, remediation isn't delayed", "node", nodeName, "CR name", cr.GetName())
			} else if crCalculatedDelay < 0 {
				// Remediation deletion is permanently delayed and requires manual intervention.
				shouldDelete = false
			} else if crCalculatedDelay > 0 {
				// Remediation deletion is temporarily delayed.
				if requeueAfter == nil || crCalculatedDelay < *requeueAfter {
					requeueAfter = &crCalculatedDelay
				}
				shouldDelete = false
			}
		}
		if shouldDelete {
			if deleted, err := m.DeleteRemediationCR(&cr, owner); err != nil {
				m.log.Error(err, "failed to delete remediation CR", "name", cr.GetName())
				return remediationCRs, nil, err
			} else if deleted {
				m.log.Info("deleted remediation CR", "name", cr.GetName())
			}
		}

	}

	nhc, isNhcOwner := owner.(*remediationv1alpha1.NodeHealthCheck)
	// Offset by 1 second in order to make sure remediation can be deleted when requeue happens.
	if requeueAfter != nil && isNhcOwner {
		*requeueAfter += time.Second
		UpdateStatusNodeDelayedHealthy(nodeName, nhc, remediationCRs)
	}

	return remediationCRs, requeueAfter, nil
}

func (m *manager) calcCrDeletionDelay(cr unstructured.Unstructured) (time.Duration, error) {
	healthyDelay, isDelayConfigured := m.ctx.Value(HealthyDelayContextKey).(time.Duration)
	// Delay isn't configured stick with regular flow and delete the CR without delay.
	if !isDelayConfigured {
		return 0, nil
	}
	switch {
	case healthyDelay == 0: // Delete the CR
		return 0, nil
	case healthyDelay < 0: // Negative value is an indication to never automatically delete the CR.
		return -1, nil
	default:
		if cr.GetAnnotations() == nil {
			cr.SetAnnotations(make(map[string]string))
		}
		delayStartTimeStr, isDelayAnnotationExist := cr.GetAnnotations()[RemediationHealthyDelayAnnotationKey]
		if isDelayAnnotationExist {
			delayStartTime, err := time.Parse(time.RFC3339, delayStartTimeStr)
			if err != nil {
				return 0, err
			}
			var remainingTime time.Duration
			now := time.Now().UTC()
			delayUntil := delayStartTime.Add(healthyDelay)
			if now.Before(delayUntil) {
				remainingTime = delayUntil.Sub(now)
				m.log.Info("delaying node getting healthy", "node name", utils.GetNodeNameFromCR(cr), "remaining time in seconds", remainingTime.Seconds())
			} else {
				m.log.Info("delaying for node getting healthy is done, about to remove the remediation CR", "node name", utils.GetNodeNameFromCR(cr))
			}
			return remainingTime, nil

		}
		// Set current time as the baseline for delaying node healthy.
		crAnnotations := cr.GetAnnotations()
		crAnnotations[RemediationHealthyDelayAnnotationKey] = time.Now().UTC().Format(time.RFC3339)
		cr.SetAnnotations(crAnnotations)
		m.log.Info("setting a delay for node getting healthy", "node name", utils.GetNodeNameFromCR(cr), "delay in seconds", healthyDelay.Seconds())
		return healthyDelay, m.UpdateRemediationCR(&cr)
	}
}

func (m *manager) isManuallyConfirmedHealthyAnnotationSet(nodeName string) (bool, error) {
	node := &corev1.Node{}
	err := m.Get(m.ctx, client.ObjectKey{Name: nodeName}, node)
	if err != nil {
		m.log.Error(err, "failed to get node", "node", nodeName)
		return false, err
	}
	_, found := node.GetAnnotations()[RemediationManuallyConfirmedHealthyAnnotationKey]
	return found, nil
}

func (m *manager) removeConfirmedHealthyAnnotation(nodeName string) error {
	node := &corev1.Node{}
	err := m.Get(m.ctx, client.ObjectKey{Name: nodeName}, node)
	if err != nil {
		m.log.Error(err, "failed to get node", "node", nodeName)
		return err
	}
	ann := node.GetAnnotations()
	if _, found := ann[RemediationManuallyConfirmedHealthyAnnotationKey]; !found {
		return nil
	}
	delete(ann, RemediationManuallyConfirmedHealthyAnnotationKey)
	node.SetAnnotations(ann)

	return m.Update(m.ctx, node)

}

func (m *manager) CleanUp(nodeName string, isManuallyConfirmedHealthy bool) error {
	if isManuallyConfirmedHealthy {
		// Remove the annotation once all the CRs were removed
		if err := m.removeConfirmedHealthyAnnotation(nodeName); err != nil {
			m.log.Error(err, fmt.Sprintf("failed to remove node annotation %s", RemediationManuallyConfirmedHealthyAnnotationKey), "node", nodeName)
			return err
		}
	}
	return m.leaseManager.InvalidateLease(m.ctx, nodeName)
}

func (m *manager) getOwningMachineWithNamespace(node *corev1.Node) (*metav1.OwnerReference, string, error) {
	ns, name, err := utils.GetMachineNamespaceName(node)
	if err != nil {
		if errors.Is(err, utils.MachineAnnotationNotFoundError) {
			m.log.Info("didn't find machine annotation for Openshift machine", "node", node.GetName())
			// Nothing we can do, continue without owning machine.
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
