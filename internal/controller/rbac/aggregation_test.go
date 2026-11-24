package rbac

import (
	"context"

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

	var aggr Aggregation

	BeforeEach(func() {
		// fake the NHC deployment
		depl := getDeployment()
		err := k8sClient.Create(context.Background(), depl)
		Expect(err).To(Or(
			BeNil(),
			WithTransform(func(err error) bool { return errors.IsAlreadyExists(err) }, BeTrue()),
		))
	})

	JustBeforeEach(func() {
		By("init new Aggregation")
		aggr = NewAggregation(context.Background(), k8sManager.GetClient(), "default")
		Expect(aggr.CreateOrUpdateAggregation()).To(Succeed())
	})

	getEmptyRole := func() *rbacv1.ClusterRole {
		return &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: roleName,
			},
		}
	}

	getEmptyRoleBinding := func() *rbacv1.ClusterRoleBinding {
		return &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: roleName,
			},
		}
	}

	AfterEach(func() {
		By("deleting old roles and bindings")
		k8sClient.Delete(context.Background(), getEmptyRole())
		k8sClient.Delete(context.Background(), getEmptyRoleBinding())

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

	validateRoleBinding := func() *rbacv1.ClusterRoleBinding {
		rb := getEmptyRoleBinding()
		ExpectWithOffset(1, k8sClient.Get(context.Background(), client.ObjectKeyFromObject(rb), rb)).To(Succeed())
		expected := aggr.getRoleBinding()
		ExpectWithOffset(1, rb.RoleRef).To(Equal(expected.RoleRef))
		ExpectWithOffset(1, rb.Subjects).To(Equal(expected.Subjects))
		return rb
	}

	When("Aggregation is not set up", func() {

		It("Should create the binding but not the role", func() {
			rb := validateRoleBinding()
			Expect(rb.OwnerReferences).To(HaveLen(1))
			Expect(rb.OwnerReferences).To(Equal(aggr.getRoleBinding().OwnerReferences))
			Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(getEmptyRole()), getEmptyRole())).To(
				WithTransform(errors.IsNotFound, BeTrue()))
		})

	})

	When("Aggregation is already set up", func() {

		var existingRole *rbacv1.ClusterRole

		BeforeEach(func() {
			By("creating the role like the manifests or older versions did")
			existingRole = &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: roleName,
				},
				AggregationRule: &rbacv1.AggregationRule{
					ClusterRoleSelectors: []metav1.LabelSelector{
						{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(context.Background(), existingRole)).To(Succeed())
		})

		validateRoleUntouched := func() {
			r := getEmptyRole()
			ExpectWithOffset(1, k8sClient.Get(context.Background(), client.ObjectKeyFromObject(r), r)).To(Succeed())
			ExpectWithOffset(1, r.ResourceVersion).To(Equal(existingRole.ResourceVersion))
			ExpectWithOffset(1, r.AggregationRule).To(Equal(existingRole.AggregationRule))
		}

		Context("Binding is up to date", func() {
			BeforeEach(func() {
				Expect(k8sClient.Create(context.Background(), NewAggregation(context.Background(), k8sClient, namespace).getRoleBinding())).To(Succeed())
			})

			It("Should keep the binding and not touch the role", func() {
				validateRoleBinding()
				validateRoleUntouched()
			})
		})

		Context("Binding is outdated", func() {
			BeforeEach(func() {
				oldRoleBinding := NewAggregation(context.Background(), k8sClient, namespace).getRoleBinding()
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
				Expect(k8sClient.Create(context.Background(), oldRoleBinding)).To(Succeed())
			})

			It("Should update the binding and not touch the role", func() {
				validateRoleBinding()
				validateRoleUntouched()
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
