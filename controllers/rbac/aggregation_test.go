package rbac

import (
	"context"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const namespace = "default"

var _ = Describe("Aggregation Tests", func() {

	var a Aggregation

	BeforeEach(func() {
		depl := getDeployment()
		err := k8sClient.Create(context.Background(), depl)
		Expect(err).To(Or(
			BeNil(),
			WithTransform(func(err error) bool { return errors.IsAlreadyExists(err) }, BeTrue()),
		))
	})

	JustBeforeEach(func() {
		By("init new Aggregation")
		a = NewAggregation(k8sManager.GetClient(), "default")
		Expect(a.CreateOrUpdateAggregation()).To(Succeed())
	})

	getEmptyRole := func() *rbacv1.ClusterRole {
		return &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name:      roleName,
				Namespace: namespace,
			},
		}
	}

	getEmptyRoleBinding := func() *rbacv1.ClusterRoleBinding {
		return &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      roleName,
				Namespace: namespace,
			},
		}
	}

	AfterEach(func() {
		By("deleting old roles and bindings")
		// cleanup role and binding
		k8sClient.Delete(context.Background(), getEmptyRole())
		k8sClient.Delete(context.Background(), getEmptyRoleBinding())

		/// wait
		Eventually(func() bool {
			r := getEmptyRole()
			if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(r), r); !errors.IsNotFound(err) {
				return false
			}
			rb := getEmptyRoleBinding()
			if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(rb), rb); !errors.IsNotFound(err) {
				return false
			}
			return true
		}, 30, 1).Should(BeTrue())

	})

	validateRole := func(r *rbacv1.ClusterRole) {
		expected := a.getRole()
		ExpectWithOffset(1, reflect.DeepEqual(r.AggregationRule, expected.AggregationRule))
		ExpectWithOffset(1, reflect.DeepEqual(r.Rules, expected.Rules))
		ExpectWithOffset(1, reflect.DeepEqual(r.OwnerReferences, expected.OwnerReferences))
	}

	validateRoleBinding := func(rb *rbacv1.ClusterRoleBinding) {
		expected := a.getRoleBinding()
		Expect(reflect.DeepEqual(rb.RoleRef, expected.RoleRef))
		Expect(reflect.DeepEqual(rb.Subjects, expected.Subjects))
		Expect(reflect.DeepEqual(rb.OwnerReferences, expected.OwnerReferences))
	}

	validate := func() {
		r := getEmptyRole()
		Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(r), r)).To(Succeed())
		validateRole(r)

		rb := getEmptyRoleBinding()
		Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(rb), r)).To(Succeed())
		validateRoleBinding(rb)

	}

	When("Aggregation is not set up", func() {

		It("Should create rule and binding", func() {
			validate()
		})

	})

	When("Aggregation is already set up", func() {

		Context("Aggregation is up to date", func() {
			BeforeEach(func() {
				By("faking old roles and bindings")
				Expect(k8sClient.Create(context.Background(), a.getRole())).To(Succeed())
				Expect(k8sClient.Create(context.Background(), a.getRoleBinding())).To(Succeed())
			})

			It("Should update rule and binding", func() {
				validate()
			})
		})

		Context("Aggregation is outdated", func() {
			BeforeEach(func() {
				oldRole := a.getRole()
				oldRole.AggregationRule.ClusterRoleSelectors = []metav1.LabelSelector{
					{
						MatchLabels: map[string]string{
							"foo": "bar",
						},
					},
				}
				oldRole.Rules = []rbacv1.PolicyRule{
					{
						APIGroups: []string{"foo"},
						Resources: []string{"something"},
						Verbs:     []string{"all"},
					},
				}
				oldRole.OwnerReferences[0].UID = "1234"

				oldRoleBinding := a.getRoleBinding()
				oldRoleBinding.RoleRef = rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "foo",
				}
				oldRoleBinding.Subjects = []rbacv1.Subject{
					{
						Kind:      "ServiceAccount",
						Name:      "bar",
						Namespace: "somewhere",
					},
				}
				oldRoleBinding.OwnerReferences = []metav1.OwnerReference{
					{
						APIVersion: "dummy",
						Kind:       "foo",
						Name:       "bar",
						UID:        "123",
					},
				}
			})

			It("Should update rule and binding", func() {
				validate()
			})
		})
	})
})

func getDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels:      map[string]string{"app": "test"},
				MatchExpressions: nil,
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test"},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "test",
							Image: "test",
						},
					},
				},
			},
		},
	}
}
