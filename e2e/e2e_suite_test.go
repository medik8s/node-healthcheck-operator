package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"

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

	Expect(createPoisonPillTemplate()).To(Succeed())
	Expect(createNHCResources()).To(Succeed())
}, 10)

func createNHCResources() error {
	maxUnhealthy := intstr.Parse("49%")
	nhc := v1alpha1.NodeHealthCheck{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NodeHealthCheck",
			APIVersion: v1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: v1alpha1.NodeHealthCheckSpec{
			Selector: metav1.LabelSelector{},
			UnhealthyConditions: []v1alpha1.UnhealthyCondition{
				{
					Type:     v1.NodeReady,
					Status:   v1.ConditionUnknown,
					Duration: metav1.Duration{Duration: time.Second * 20},
				},
			},
			MaxUnhealthy: &maxUnhealthy,
			RemediationTemplate: &v1.ObjectReference{
				Kind:       "PoisonPillRemediationTemplate",
				APIVersion: "poison-pill.medik8s.io/v1alpha1",
				Name:       "poison-pill-template",
				Namespace:  testNamespace,
			},
		},
	}
	toUnstructured, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&nhc)
	fmt.Println(toUnstructured)
	Expect(err).NotTo(HaveOccurred())

	_, err = dynamicClient.
		Resource(nhcGVR).
		Create(
			context.Background(),
			&unstructured.Unstructured{Object: toUnstructured},
			metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	return nil
}

func debug() {
	version, _ := clientSet.ServerVersion()
	fmt.Fprint(GinkgoWriter, version)
}

func createPoisonPillTemplate() error {
	obj := unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "PoisonPillRemediationTemplate",
			"apiVersion": "poison-pill.medik8s.io/v1alpha1",
			"metadata": map[string]interface{}{
				"name": "poison-pill-template",
			},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": map[string]interface{}{},
				},
			},
		},
	}
	_, err := dynamicClient.Resource(poisonPillTemplateGVR).Namespace(testNamespace).Create(context.Background(), &obj, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	return nil
}
