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
	roleName       = "node-healthcheck-operator-aggregation"
	saName         = "node-healthcheck-controller-manager"
	deploymentName = "node-healthcheck-controller-manager"
)

// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=bind,resourceNames=node-healthcheck-operator-aggregation
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=update,resourceNames=node-healthcheck-operator-aggregation
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch

// Aggregation defines the functions needed for setting up RBAC aggregation
type Aggregation interface {
	CreateOrUpdateAggregation() error
	getRoleBinding() *rbacv1.ClusterRoleBinding
}

type aggregation struct {
	client.Client
	ctx       context.Context
	namespace string
}

var _ Aggregation = aggregation{}

// NewAggregation create a new Aggregation struct
func NewAggregation(ctx context.Context, client client.Client, namespace string) Aggregation {
	return &aggregation{
		Client:    client,
		ctx:       ctx,
		namespace: namespace,
	}
}

func (a aggregation) CreateOrUpdateAggregation() error {
	return a.createOrUpdateRoleBinding()
}

func (a aggregation) createOrUpdateRoleBinding() error {
	// check if the binding exists
	binding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: roleName,
		},
	}
	err := a.Get(a.ctx, client.ObjectKeyFromObject(binding), binding)
	if errors.IsNotFound(err) {
		return a.createRoleBinding()
	} else if err != nil {
		return fmt.Errorf("failed to get cluster role binding: %v", err)
	}
	return a.updateRoleBinding(binding)
}

func (a aggregation) createRoleBinding() error {
	err := a.Create(a.ctx, a.getRoleBinding())
	return err
}

func (a aggregation) updateRoleBinding(oldBinding *rbacv1.ClusterRoleBinding) error {
	newBinding := a.getRoleBinding()
	oldBinding.RoleRef = newBinding.RoleRef
	oldBinding.Subjects = newBinding.Subjects
	return a.Update(a.ctx, oldBinding)
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
	if err := a.Get(a.ctx, client.ObjectKeyFromObject(depl), depl); err != nil {
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
