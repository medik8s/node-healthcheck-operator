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
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

const templateSuffix = "Template"

type Manager interface {
	GetTemplate(nhc *remediationv1alpha1.NodeHealthCheck) (*unstructured.Unstructured, error)
	GenerateRemediationCRBase(gvk schema.GroupVersionKind) *unstructured.Unstructured
	GenerateRemediationCR(node *corev1.Node, nhc *remediationv1alpha1.NodeHealthCheck, template *unstructured.Unstructured) *unstructured.Unstructured
	CreateRemediationCR(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck, log logr.Logger) (bool, error)
	DeleteRemediationCR(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) (bool, error)
	GetNodes(labelSelector metav1.LabelSelector) ([]corev1.Node, error)
	GetOwnedInflightRemediations(nhc *remediationv1alpha1.NodeHealthCheck) (map[string]metav1.Time, error)
	GetAllInflightRemediations(nhc *remediationv1alpha1.NodeHealthCheck) ([]unstructured.Unstructured, error)
}

type manager struct {
	client.Client
	ctx context.Context
}

var _ Manager = &manager{}

func NewManager(c client.Client, ctx context.Context) Manager {
	return &manager{
		Client: c,
		ctx:    ctx,
	}
}

func (m *manager) GetTemplate(nhc *remediationv1alpha1.NodeHealthCheck) (*unstructured.Unstructured, error) {
	t := nhc.Spec.RemediationTemplate.DeepCopy()
	template := new(unstructured.Unstructured)
	template.SetGroupVersionKind(t.GroupVersionKind())
	template.SetName(t.Name)
	template.SetNamespace(t.Namespace)
	if err := m.Get(m.ctx, client.ObjectKeyFromObject(template), template); err != nil {
		return nil, errors.Wrapf(err, "failed to get external remdiation template %q/%q", template.GetNamespace(), template.GetName())
	}

	// check if template is valid
	_, found, err := unstructured.NestedMap(template.Object, "spec", "template")
	if !found || err != nil {
		return nil, errors.Errorf("invalid template %q/%q, didn't find spec.template.spec", template.GetNamespace(), template.GetName())
	}
	return template, nil
}

func (m *manager) GenerateRemediationCR(node *corev1.Node, nhc *remediationv1alpha1.NodeHealthCheck, template *unstructured.Unstructured) *unstructured.Unstructured {

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

	if nhc != nil {
		remediationCR.SetOwnerReferences([]metav1.OwnerReference{
			{
				APIVersion:         nhc.APIVersion,
				Kind:               nhc.Kind,
				Name:               nhc.Name,
				UID:                nhc.UID,
				Controller:         pointer.Bool(false),
				BlockOwnerDeletion: nil,
			},
		})
		remediationCR.SetLabels(map[string]string{
			"app.kubernetes.io/part-of": "node-healthcheck-controller",
		})
	}

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

func (m *manager) CreateRemediationCR(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck, log logr.Logger) (bool, error) {
	// check if CR already exists
	if err := m.Get(m.ctx, client.ObjectKeyFromObject(remediationCR), remediationCR); err == nil {
		if !IsOwner(remediationCR, nhc) {
			log.Info("external remediation CR already exists, but it's not owned by us", "owners", remediationCR.GetOwnerReferences())
		} else {
			log.Info("external remediation CR already exists")
		}
		return false, nil
	} else if !apierrors.IsNotFound(err) {
		log.Error(err, "failed to check for existing external remediation object")
		return false, err
	}

	// create CR
	log.Info("Creating an remediation CR",
		"CR Name", remediationCR.GetName(),
		"CR KVK", remediationCR.GroupVersionKind(),
		"namespace", remediationCR.GetNamespace())

	if err := m.Create(m.ctx, remediationCR); err != nil {
		log.Error(err, "failed to create an external remediation object")
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
