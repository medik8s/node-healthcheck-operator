package e2e

import (
	"fmt"
	"testing"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestE2e(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2e Suite")
}

var (
	dynamicClient         dynamic.Interface
	clientSet             *kubernetes.Clientset
	poisonPillTemplateGVR = schema.GroupVersionResource{
		Group:    "poison-pill.medik8s.io",
		Version:  "v1alpha1",
		Resource: "poisonpillremediationtemplates",
	}
	poisonPillRemediationGVR = schema.GroupVersionResource{
		Group:    "poison-pill.medik8s.io",
		Version:  "v1alpha1",
		Resource: "poisonpillremediations",
	}
	nhcGVR = schema.GroupVersionResource{
		Group:    v1alpha1.GroupVersion.Group,
		Version:  v1alpha1.GroupVersion.Version,
		Resource: "nodehealthchecks",
	}
)

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	// +kubebuilder:scaffold:scheme

	// get the client or die
	getConfig, err := config.GetConfig()
	if err != nil {
		Fail(fmt.Sprintf("Couldn't get kubeconfig %v", err))
	}
	clientSet, err = kubernetes.NewForConfig(getConfig)
	Expect(err).NotTo(HaveOccurred())
	Expect(clientSet).NotTo(BeNil())

	dynamicClient, err = dynamic.NewForConfig(getConfig)
	Expect(err).NotTo(HaveOccurred())
	Expect(dynamicClient).NotTo(BeNil())

	debug()
}, 10)

func debug() {
	version, _ := clientSet.ServerVersion()
	fmt.Fprint(GinkgoWriter, version)
}
