package bootstrap

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/defaults"
)

var _ = Describe("Init", func() {

	BeforeEach(func() {
		Expect(Initialize(context.TODO(), k8sManager, ctrl.Log.WithName("test init"))).To(Succeed())
	})

	When("initialization is called", func() {
		It("creates a default NHC resource", func() {
			nhcs := v1alpha1.NodeHealthCheckList{}
			err := k8sClient.List(context.Background(), &nhcs, &client.ListOptions{
				Namespace: "",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(nhcs.Items).To(HaveLen(1))
			Expect(nhcs.Items[0].Name).To(Equal(defaults.DefaultCRName))
		})
	})
})
