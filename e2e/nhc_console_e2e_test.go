package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	consolev1 "github.com/openshift/api/console/v1"

	"github.com/medik8s/node-healthcheck-operator/controllers/console"
	"github.com/medik8s/node-healthcheck-operator/e2e/utils"
)

var _ = Describe("e2e - console", Label("NHC"), func() {
	When("the operator and the console plugin is deployed", labelOcpOnly, func() {
		It("the plugin manifest should be served", func() {
			By("getting the ConsolePlugin")
			plugin := &consolev1.ConsolePlugin{}
			Expect(k8sClient.Get(context.Background(), ctrl.ObjectKey{Name: console.PluginName}, plugin)).To(Succeed(), "failed to get ConsolePlugin")

			By("getting the plugin Service")
			svc := &v1.Service{}
			Expect(k8sClient.Get(context.Background(), ctrl.ObjectKey{Namespace: plugin.Spec.Backend.Service.Namespace, Name: plugin.Spec.Backend.Service.Name}, svc)).To(Succeed(), "failed to get plugin Service")

			By("getting the console manifest")
			manifestUrl := fmt.Sprintf("https://%s:%d/%s/plugin-manifest.json", svc.Spec.ClusterIP, svc.Spec.Ports[0].Port, plugin.Spec.Backend.Service.BasePath)
			cmd := fmt.Sprintf("curl -k %s", manifestUrl)
			node := utils.GetWorkerNodes(k8sClient)[0]
			output, err := utils.RunCommandInCluster(clientSet, node.Name, testNsName, cmd, log)
			Expect(err).ToNot(HaveOccurred())
			log.Info("got manifest (stripped)", "manifest", output[:100])
			Expect(output).To(ContainSubstring(console.PluginName), "failed to get correct plugin manifest")
		})
	})
})
