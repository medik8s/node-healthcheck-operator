package rbac

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	roleName            = "node-healthcheck-operator-aggregation"
	aggregationLabelKey = "rbac.ext-remediation/aggregate-to-ext-remediation"
	saName              = "node-healthcheck-controller-manager"
	deploymentName      = "node-healthcheck-controller-manager"
)

// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=*
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=*

// Aggregation defines the functions needed for setting up RBAC aggregation
type Aggregation interface {
	CreateOrUpdateAggregation() error
	getRole() *rbacv1.ClusterRole
	getRoleBinding() *rbacv1.ClusterRoleBinding
}

type aggregation struct {
	client.Client
	namespace string
}

var _ Aggregation = aggregation{}

// NewAggregation create a new Aggregation struct
func NewAggregation(client client.Client, namespace string) Aggregation {
	return &aggregation{
		Client:    client,
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
	err := a.Get(context.Background(), client.ObjectKeyFromObject(role), role)
	if errors.IsNotFound(err) {
		return a.createRole()
	} else if err != nil {
		return fmt.Errorf("failed to get cluster role: %v", err)
	}
	return a.updateRole(role)
}

func (a aggregation) createRole() error {
	err := a.Create(context.Background(), a.getRole())
	return err
}

func (a aggregation) updateRole(oldRole *rbacv1.ClusterRole) error {
	newRole := a.getRole()
	oldRole.Rules = newRole.Rules
	oldRole.AggregationRule = newRole.AggregationRule
	return a.Update(context.Background(), oldRole)
}

func (a aggregation) getRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:            roleName,
			Namespace:       a.namespace,
			OwnerReferences: a.getOwnerRefs(),
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
	err := a.Get(context.Background(), client.ObjectKeyFromObject(binding), binding)
	if errors.IsNotFound(err) {
		return a.createRoleBinding()
	} else if err != nil {
		return fmt.Errorf("failed to get cluster role binding: %v", err)
	}
	return a.updateRoleBinding(binding)
}

func (a aggregation) createRoleBinding() error {
	err := a.Create(context.Background(), a.getRoleBinding())
	return err
}

func (a aggregation) updateRoleBinding(oldBinding *rbacv1.ClusterRoleBinding) error {
	newBinding := a.getRoleBinding()
	oldBinding.RoleRef = newBinding.RoleRef
	oldBinding.Subjects = newBinding.Subjects
	return a.Update(context.Background(), oldBinding)
}

func (a aggregation) getRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:            roleName,
			Namespace:       a.namespace,
			OwnerReferences: a.getOwnerRefs(),
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
				Namespace: a.namespace,
			},
		},
	}
}

func (a aggregation) getOwnerRefs() []metav1.OwnerReference {

	depl := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: a.namespace,
		},
	}
	if err := a.Get(context.Background(), client.ObjectKeyFromObject(depl), depl); err != nil {
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
