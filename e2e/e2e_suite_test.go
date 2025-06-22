package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap/zapcore"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	consolev1 "github.com/openshift/api/console/v1"
	"github.com/openshift/api/machine/v1beta1"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

func TestE2e(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2e Suite")
}

const (
	unhealthyConditionDuration = 30 * time.Second
)

var (
	labelOcpOnlyValue = "OCP-ONLY"
	labelOcpOnly      = Label(labelOcpOnlyValue)

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
	dummyRemediationTemplateGVK schema.GroupVersionKind
	dummyRemediationGVK         schema.GroupVersionKind
	dummyTemplateName           = "dummy-template"
	snrTemplateName             string

	log logr.Logger

	// The ns the operator is running in
	operatorNsName string

	// The ns test pods are started in
	testNsName = "nhc-test"

	// The ns where leases will be created
	leaseNs = "medik8s-leases"
)

var _ = BeforeSuite(func() {
	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.RFC3339NanoTimeEncoder,
	}
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseFlagOptions(&opts)))
	log = logf.Log

	operatorNsName = os.Getenv("OPERATOR_NS")
	Expect(operatorNsName).ToNot(BeEmpty(), "OPERATOR_NS env var not set, can't start e2e test")

	snrTemplateName = os.Getenv("SNRT_NAME")
	Expect(snrTemplateName).ToNot(BeEmpty(), "SNRT_NAME env var not set, can't start e2e test")

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
	err = apiextensionsv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	Expect(consolev1.Install(scheme.Scheme)).To(Succeed())

	k8sClient, err = ctrl.New(config, ctrl.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	// create test ns
	testNs := &corev1.Namespace{
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

	// Create dummy remediation CRDs, a template, and the RBAC role
	dummyKind := "Foo"
	dummyTemplateCRD := newTestRemediationTemplateCRD(dummyKind)
	dummyRemediationTemplateGVK = schema.GroupVersionKind{
		Group:   dummyTemplateCRD.Spec.Group,
		Version: dummyTemplateCRD.Spec.Versions[0].Name,
		Kind:    dummyKind + "Template",
	}
	dummyCRD := newTestRemediationCRD(dummyKind)
	dummyRemediationGVK = schema.GroupVersionKind{
		Group:   dummyCRD.Spec.Group,
		Version: dummyCRD.Spec.Versions[0].Name,
		Kind:    dummyKind,
	}
	dummyTemplate := newTestRemediationTemplateCR(dummyKind, testNsName, dummyTemplateName)
	clusterRole := newExtRemediationClusterRole()
	for _, obj := range []ctrl.Object{dummyTemplateCRD, dummyCRD, dummyTemplate, clusterRole} {
		err := k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(obj), obj)
		if errors.IsNotFound(err) {
			// no need to cleanup, just reuse fro next round of tests
			Expect(k8sClient.Create(context.Background(), obj)).To(Succeed())
			// wait until resource exists
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(obj), obj)).To(Succeed())
			}, 2*time.Second, 100*time.Millisecond).Should(Succeed())
		} else {
			Expect(err).ToNot(HaveOccurred())
		}
	}
	debug()
})

func debug() {
	version, _ := clientSet.ServerVersion()
	fmt.Fprint(GinkgoWriter, version)
}

// copied from controllers package since test files don't export anything
func newTestRemediationTemplateCRD(kind string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apiextensionsv1.SchemeGroupVersion.String(),
			Kind:       "CustomResourceDefinition",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: strings.ToLower(kind) + "templates.test.medik8s.io",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "test.medik8s.io",
			Scope: apiextensionsv1.NamespaceScoped,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:   kind + "Template",
				Plural: strings.ToLower(kind) + "templates",
			},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
					Subresources: &apiextensionsv1.CustomResourceSubresources{
						Status: &apiextensionsv1.CustomResourceSubresourceStatus{},
					},
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type:                   "object",
									XPreserveUnknownFields: pointer.Bool(true),
								},
								"status": {
									Type:                   "object",
									XPreserveUnknownFields: pointer.Bool(true),
								},
							},
						},
					},
				},
			},
		},
	}
}

func newTestRemediationCRD(kind string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apiextensions.SchemeGroupVersion.String(),
			Kind:       "CustomResourceDefinition",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: strings.ToLower(kind) + "s.test.medik8s.io",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "test.medik8s.io",
			Scope: apiextensionsv1.NamespaceScoped,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:   kind,
				Plural: strings.ToLower(kind) + "s",
			},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
					Subresources: &apiextensionsv1.CustomResourceSubresources{
						Status: &apiextensionsv1.CustomResourceSubresourceStatus{},
					},
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type:                   "object",
									XPreserveUnknownFields: pointer.Bool(true),
								},
								"status": {
									Type:                   "object",
									XPreserveUnknownFields: pointer.Bool(true),
								},
							},
						},
					},
				},
			},
		},
	}
}

func newTestRemediationTemplateCR(kind, namespace, name string) ctrl.Object {
	template := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"size": "foo",
					},
				},
			},
		},
	}
	template.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "test.medik8s.io",
		Version: "v1alpha1",
		Kind:    kind + "Template",
	})
	template.SetNamespace(namespace)
	template.SetName(name)
	return template
}

func newExtRemediationClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dummy-remediation-aggregation",
			Labels: map[string]string{
				"rbac.ext-remediation/aggregate-to-ext-remediation": "true",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{dummyRemediationTemplateGVK.Group},
				Resources: []string{strings.ToLower(dummyRemediationTemplateGVK.Kind) + "s"},
				Verbs:     []string{"get"},
			},
			{
				APIGroups: []string{dummyRemediationGVK.Group},
				Resources: []string{strings.ToLower(dummyRemediationGVK.Kind) + "s"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
		},
	}
}
