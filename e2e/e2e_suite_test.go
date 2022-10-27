package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"go.uber.org/zap/zapcore"

	consolev1alpha1 "github.com/openshift/api/console/v1alpha1"
	"github.com/openshift/api/machine/v1beta1"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

func TestE2e(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2e Suite")
}

var (
	dynamicClient          dynamic.Interface
	clientSet              *kubernetes.Clientset
	k8sClient              ctrl.Client
	remediationTemplateGVR = schema.GroupVersionResource{
		Group:    "self-node-remediation.medik8s.io",
		Version:  "v1alpha1",
		Resource: "selfnoderemediationtemplates",
	}
	remediationGVR = schema.GroupVersionResource{
		Group:    "self-node-remediation.medik8s.io",
		Version:  "v1alpha1",
		Resource: "selfnoderemediations",
	}
	nhcGVR = schema.GroupVersionResource{
		Group:    v1alpha1.GroupVersion.Group,
		Version:  v1alpha1.GroupVersion.Version,
		Resource: "nodehealthchecks",
	}
	mhcGVR = schema.GroupVersionResource{
		Group:    v1beta1.GroupVersion.Group,
		Version:  v1beta1.GroupVersion.Version,
		Resource: "machinehealthchecks",
	}

	log logr.Logger

	// The ns the operator is running in
	operatorNsName string

	// The ns test pods are started in
	testNsName = "nhc-test"
)

var _ = BeforeSuite(func() {
	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.RFC3339NanoTimeEncoder,
	}
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseFlagOptions(&opts)))
	log = logf.Log

	operatorNsName = os.Getenv("OPERATOR_NS")
	// operatorNsName isn't used yet but might be useful in future
	//Expect(operatorNsName).ToNot(BeEmpty(), "OPERATOR_NS env var not set, can't start e2e test")

	// +kubebuilder:scaffold:scheme

	// get the k8sClient or die
	config, err := config.GetConfig()
	if err != nil {
		Fail(fmt.Sprintf("Couldn't get kubeconfig %v", err))
	}
	clientSet, err = kubernetes.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())
	Expect(clientSet).NotTo(BeNil())

	dynamicClient, err = dynamic.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())
	Expect(dynamicClient).NotTo(BeNil())

	scheme.AddToScheme(scheme.Scheme)
	err = v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = v1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	Expect(consolev1alpha1.Install(scheme.Scheme)).To(Succeed())

	k8sClient, err = ctrl.New(config, ctrl.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	// create test ns
	testNs := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: testNsName,
			Labels: map[string]string{
				// allow privileged pods in test namespace, needed for API blocker pod
				"pod-security.kubernetes.io/enforce":             "privileged",
				"security.openshift.io/scc.podSecurityLabelSync": "false",
			},
		},
	}
	err = k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(testNs), testNs)
	if errors.IsNotFound(err) {
		err = k8sClient.Create(context.Background(), testNs)
	}
	Expect(err).ToNot(HaveOccurred(), "could not get or create test ns")

	debug()
}, 10)

func debug() {
	version, _ := clientSet.ServerVersion()
	fmt.Fprint(GinkgoWriter, version)
}
