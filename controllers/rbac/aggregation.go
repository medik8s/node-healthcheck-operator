package rbac

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	roleName            = "node-healthcheck-operator-aggregation"
	aggregationLabelKey = "rbac.ext-remediation/aggregate-to-ext-remediation"
	saName              = "node-healthcheck-operator-controller-manager"
	deploymentName      = "node-healthcheck-operator-controller-manager"
)

// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=*
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=*

// Aggregation defines the functions needed for setting up RBAC aggregation
type Aggregation interface {
	CreateOrUpdateAggregation() error
}

type aggregation struct {
	client.Client
	reader    client.Reader
	namespace string
}

var _ Aggregation = aggregation{}

// NewAggregation create a new Aggregation struct
func NewAggregation(mgr ctrl.Manager, namespace string) Aggregation {
	return &aggregation{
		Client:    mgr.GetClient(),
		reader:    mgr.GetAPIReader(),
		namespace: namespace,
	}
}

func (a aggregation) CreateOrUpdateAggregation() error {

	if err := a.createOrUpdateRole(); err != nil {
		return err
	}
	return a.createOrUpdateRoleBinding()

}

func (a aggregation) createOrUpdateRole() error {
	// check if the role exists
	role := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: roleName,
		},
	}
	err := a.reader.Get(context.Background(), client.ObjectKeyFromObject(role), role)
	if errors.IsNotFound(err) {
		return a.createRole()
	} else if err != nil {
		return fmt.Errorf("failed to get cluster role: %v", err)
	}
	return a.updateRole(role)
}

func (a aggregation) createRole() error {
	err := a.Create(context.Background(), getRole(a.reader, a.namespace))
	return err
}

func (a aggregation) updateRole(oldRole *rbacv1.ClusterRole) error {
	newRole := getRole(a.reader, a.namespace)
	oldRole.Rules = newRole.Rules
	oldRole.AggregationRule = newRole.AggregationRule
	return a.Update(context.Background(), oldRole)
}

func getRole(reader client.Reader, namespace string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:            roleName,
			Namespace:       namespace,
			OwnerReferences: getOwnerRefs(reader, namespace),
		},
		AggregationRule: &rbacv1.AggregationRule{
			ClusterRoleSelectors: []metav1.LabelSelector{
				{
					MatchLabels: map[string]string{
						aggregationLabelKey: "true",
					},
				},
			},
		},
	}
}

func (a aggregation) createOrUpdateRoleBinding() error {
	// check if the role exists
	binding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: roleName,
		},
	}
	err := a.reader.Get(context.Background(), client.ObjectKeyFromObject(binding), binding)
	if errors.IsNotFound(err) {
		return a.createRoleBinding()
	} else if err != nil {
		return fmt.Errorf("failed to get cluster role binding: %v", err)
	}
	return a.updateRoleBinding(binding)
}

func (a aggregation) createRoleBinding() error {
	err := a.Create(context.Background(), getRoleBinding(a.reader, a.namespace))
	return err
}

func (a aggregation) updateRoleBinding(oldBinding *rbacv1.ClusterRoleBinding) error {
	newBinding := getRoleBinding(a.reader, a.namespace)
	oldBinding.RoleRef = newBinding.RoleRef
	oldBinding.Subjects = newBinding.Subjects
	return a.Update(context.Background(), oldBinding)
}

func getRoleBinding(reader client.Reader, namespace string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:            roleName,
			Namespace:       namespace,
			OwnerReferences: getOwnerRefs(reader, namespace),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     roleName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: namespace,
			},
		},
	}
}

func getOwnerRefs(reader client.Reader, namespace string) []metav1.OwnerReference {

	depl := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
		},
	}
	if err := reader.Get(context.Background(), client.ObjectKeyFromObject(depl), depl); err != nil {
		// ignore for now, skip owner refs
		return nil
	}

	return []metav1.OwnerReference{
		{
			// at least in tests, TypeMeta is empty for the test deployment...
			APIVersion: fmt.Sprintf("%s/%s", appsv1.SchemeGroupVersion.Group, appsv1.SchemeGroupVersion.Version),
			Kind:       "Deployment",
			Name:       depl.Name,
			UID:        depl.UID,
		},
	}
}
