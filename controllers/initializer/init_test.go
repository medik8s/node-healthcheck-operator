package initializer

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/medik8s/node-healthcheck-operator/controllers/cluster"
)

var _ = Describe("Init", func() {

	var initializer *initializer

	JustBeforeEach(func() {
		caps := cluster.Capabilities{IsOnOpenshift: true, HasMachineAPI: true}
		initializer = New(k8sManager, caps, ctrl.Log.WithName("test initializer"))
	})

	AfterEach(func() {
		// noop
	})

	When("starting the initializer", func() {
		It("should not fail", func() {
			// Move this to JustBeforeEach when adding more tests!
			Expect(initializer.Start(context.Background())).To(Succeed())
		})
	})
})
