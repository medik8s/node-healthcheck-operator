package initializer

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ctrl "sigs.k8s.io/controller-runtime"
)

var _ = Describe("Init", func() {

	var initializer *initializer

	JustBeforeEach(func() {
		initializer = New(k8sManager, ctrl.Log.WithName("test initializer"))
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
