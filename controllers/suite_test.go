/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	commonannotations "github.com/medik8s/common/pkg/annotations"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap/zapcore"

	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/cluster"
	"github.com/medik8s/node-healthcheck-operator/controllers/featuregates"
	"github.com/medik8s/node-healthcheck-operator/controllers/mhc"
	"github.com/medik8s/node-healthcheck-operator/controllers/resources"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils/annotations"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

const (
	DeploymentNamespace = "testns"
	MachineNamespace    = "openshift-machine-api"
	leaseNs             = "medik8s-leases"

	InfraRemediationGroup                       = "test.medik8s.io"
	InfraRemediationVersion                     = "v1alpha1"
	InfraRemediationKind                        = "InfrastructureRemediation"
	InfraRemediationTemplateKind                = "InfrastructureRemediationTemplate"
	InfraRemediationTemplateName                = "infra-remediation-template"
	InfraMultipleSupportRemediationTemplateName = "infra-multiple-remediation-template"
	MultipleSupportTemplateName                 = "multi-supported-template"
	SecondMultipleSupportTemplateName           = "second-multi-supported-template"
)

var (
	InfraRemediationAPIVersion = fmt.Sprintf("%s/%s", InfraRemediationGroup, InfraRemediationVersion)

	infraRemediationTemplateRef = &v1.ObjectReference{
		APIVersion: InfraRemediationAPIVersion,
		Kind:       InfraRemediationTemplateKind,
		Namespace:  MachineNamespace,
		Name:       InfraRemediationTemplateName,
	}

	infraMultipleRemediationTemplateRef = &v1.ObjectReference{
		APIVersion: InfraRemediationAPIVersion,
		Kind:       InfraRemediationTemplateKind,
		Namespace:  MachineNamespace,
		Name:       InfraMultipleSupportRemediationTemplateName,
	}

	infraRemediationTemplate         *unstructured.Unstructured
	infraMultipleRemediationTemplate *unstructured.Unstructured

	multiSupportTemplateRef = &v1.ObjectReference{
		APIVersion: InfraRemediationAPIVersion,
		Kind:       "MultiSupportTemplate",
		Namespace:  MachineNamespace,
		Name:       MultipleSupportTemplateName,
	}

	secondMultiSupportTemplateRef = &v1.ObjectReference{
		APIVersion: multiSupportTemplateRef.APIVersion,
		Kind:       multiSupportTemplateRef.Kind,
		Namespace:  multiSupportTemplateRef.Namespace,
		Name:       SecondMultipleSupportTemplateName,
	}
)

var (
	cfg        *rest.Config
	k8sClient  client.Client
	k8sManager manager.Manager
	testEnv    *envtest.Environment
	ctx        context.Context
	cancel     context.CancelFunc

	upgradeChecker    *fakeClusterUpgradeChecker
	ocpUpgradeChecker cluster.UpgradeChecker

	fakeTime     *time.Time
	fakeRecorder *record.FakeRecorder

	nhcReconciler *NodeHealthCheckReconciler
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	// debugging time values needs much place...
	//format.MaxLength = 10000
	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.RFC3339NanoTimeEncoder,
	}
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseFlagOptions(&opts)))

	testScheme := runtime.NewScheme()

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDInstallOptions: envtest.CRDInstallOptions{
			Scheme: testScheme,
			Paths: []string{
				filepath.Join("..", "vendor", "github.com", "openshift", "api", "machine", "v1beta1"),
				filepath.Join("..", "config", "crd", "bases"),
			},
			ErrorIfPathMissing: true,
		},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	scheme.AddToScheme(testScheme)
	Expect(remediationv1alpha1.AddToScheme(testScheme)).To(Succeed())
	Expect(machinev1beta1.Install(testScheme)).To(Succeed())
	Expect(apiextensionsv1.AddToScheme(testScheme)).To(Succeed())
	Expect(policyv1.AddToScheme(testScheme)).To(Succeed())
	// +kubebuilder:scaffold:scheme

	k8sManager, err = ctrl.NewManager(cfg, ctrl.Options{Scheme: testScheme})
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: testScheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Deploy test remediation CRDs and CR
	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: MachineNamespace,
		},
		Spec: v1.NamespaceSpec{},
	}
	Expect(k8sClient.Create(context.Background(), ns)).To(Succeed())
	Expect(k8sClient.Create(context.Background(), newTestRemediationTemplateCRD(InfraRemediationKind))).To(Succeed())
	Expect(k8sClient.Create(context.Background(), newTestRemediationCRD(InfraRemediationKind))).To(Succeed())
	time.Sleep(time.Second)
	infraRemediationTemplate = newTestRemediationTemplateCR(InfraRemediationKind, MachineNamespace, InfraRemediationTemplateName)
	Expect(k8sClient.Create(context.Background(), infraRemediationTemplate)).To(Succeed())

	infraMultipleRemediationTemplate = newTestRemediationTemplateCR(InfraRemediationKind, MachineNamespace, InfraMultipleSupportRemediationTemplateName)
	infraMultipleRemediationTemplate.SetAnnotations(map[string]string{commonannotations.MultipleTemplatesSupportedAnnotation: "true"})
	Expect(k8sClient.Create(context.Background(), infraMultipleRemediationTemplate)).To(Succeed())

	testKind := "Metal3Remediation"
	Expect(k8sClient.Create(context.Background(), newTestRemediationTemplateCRD(testKind))).To(Succeed())
	Expect(k8sClient.Create(context.Background(), newTestRemediationCRD(testKind))).To(Succeed())
	time.Sleep(time.Second)

	Expect(k8sClient.Create(context.Background(), newTestRemediationTemplateCR(testKind, MachineNamespace, "ok"))).To(Succeed())
	Expect(k8sClient.Create(context.Background(), newTestRemediationTemplateCR(testKind, "default", "nok"))).To(Succeed())

	multiSupportTestKind := "MultiSupport"
	Expect(k8sClient.Create(context.Background(), newTestRemediationTemplateCRD(multiSupportTestKind))).To(Succeed())
	Expect(k8sClient.Create(context.Background(), newTestRemediationCRD(multiSupportTestKind))).To(Succeed())
	time.Sleep(time.Second)
	multiSupportTemplate := newTestRemediationTemplateCR(multiSupportTestKind, MachineNamespace, MultipleSupportTemplateName)
	multiSupportTemplate.SetAnnotations(map[string]string{commonannotations.MultipleTemplatesSupportedAnnotation: "true"})
	Expect(k8sClient.Create(context.Background(), multiSupportTemplate)).To(Succeed())
	secondMultiSupportTemplate := newTestRemediationTemplateCR(multiSupportTestKind, MachineNamespace, SecondMultipleSupportTemplateName)
	secondMultiSupportTemplate.SetAnnotations(map[string]string{commonannotations.MultipleTemplatesSupportedAnnotation: "true"})
	Expect(k8sClient.Create(context.Background(), secondMultiSupportTemplate)).To(Succeed())

	upgradeChecker = &fakeClusterUpgradeChecker{
		Err:       nil,
		Upgrading: false,
	}

	caps := cluster.Capabilities{IsOnOpenshift: true, HasMachineAPI: true}

	mhcChecker, err := mhc.NewMHCChecker(k8sManager, caps, nil)
	Expect(err).NotTo(HaveOccurred())

	os.Setenv("DEPLOYMENT_NAMESPACE", DeploymentNamespace)
	depNs := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: DeploymentNamespace,
		},
		Spec: v1.NamespaceSpec{},
	}
	Expect(k8sClient.Create(context.Background(), depNs)).To(Succeed())

	// to be able faking the current time for tests
	currentTime = func() time.Time {
		if fakeTime != nil {
			return *fakeTime
		}
		return time.Now()
	}

	watchManager := resources.NewWatchManager(k8sManager.GetClient(), ctrl.Log.WithName("controllers").WithName("NodeHealthCheck").WithName("WatchManager"), k8sManager.GetCache())
	mhcEvents := make(chan event.GenericEvent)
	fakeRecorder = record.NewFakeRecorder(1000)
	ocpUpgradeChecker, _ = cluster.NewClusterUpgradeStatusChecker(k8sManager, cluster.Capabilities{IsOnOpenshift: true})
	nhcReconciler = &NodeHealthCheckReconciler{
		Client:                      k8sManager.GetClient(),
		Log:                         k8sManager.GetLogger().WithName("test reconciler"),
		Recorder:                    fakeRecorder,
		ClusterUpgradeStatusChecker: upgradeChecker,
		MHCChecker:                  mhcChecker,
		MHCEvents:                   mhcEvents,
		Capabilities:                caps,
		WatchManager:                watchManager,
	}
	err = nhcReconciler.SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	mhcWatchManager := resources.NewWatchManager(k8sManager.GetClient(), ctrl.Log.WithName("controllers").WithName("MachineHealthCheck").WithName("WatchManager"), k8sManager.GetCache())
	err = (&MachineHealthCheckReconciler{
		Client:                         k8sManager.GetClient(),
		Log:                            k8sManager.GetLogger().WithName("test reconciler"),
		Recorder:                       k8sManager.GetEventRecorderFor("NodeHealthCheck"),
		ClusterUpgradeStatusChecker:    upgradeChecker,
		MHCChecker:                     mhcChecker,
		FeatureGateMHCControllerEvents: make(chan event.GenericEvent),
		FeatureGates: &featuregates.FakeAccessor{
			IsMaoMhcDisabled: false,
		},
		WatchManager: mhcWatchManager,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		// https://github.com/kubernetes-sigs/controller-runtime/issues/1571
		ctx, cancel = context.WithCancel(ctrl.SetupSignalHandler())
		err := k8sManager.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

type fakeClusterUpgradeChecker struct {
	Upgrading bool
	Err       error
}

// force implementation of interface
var _ cluster.UpgradeChecker = &fakeClusterUpgradeChecker{}

func (c *fakeClusterUpgradeChecker) Check([]v1.Node) (bool, error) {
	return c.Upgrading, c.Err
}

type fakeWatchManager struct{}

func (c *fakeWatchManager) AddWatchesNhc(rm resources.Manager, nhc *remediationv1alpha1.NodeHealthCheck) error {
	return nil
}
func (c *fakeWatchManager) AddWatchesMhc(rm resources.Manager, mhc *machinev1beta1.MachineHealthCheck) error {
	return nil
}
func (c *fakeWatchManager) SetController(controller.Controller) {}

func newTestRemediationTemplateCRD(kind string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apiextensionsv1.SchemeGroupVersion.String(),
			Kind:       "CustomResourceDefinition",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%stemplates.%s", strings.ToLower(kind), InfraRemediationGroup),
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: InfraRemediationGroup,
			Scope: apiextensionsv1.NamespaceScoped,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:   kind + "Template",
				Plural: strings.ToLower(kind) + "templates",
			},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    InfraRemediationVersion,
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
			Name: fmt.Sprintf("%ss.%s", strings.ToLower(kind), InfraRemediationGroup),
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: InfraRemediationGroup,
			Scope: apiextensionsv1.NamespaceScoped,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:   kind,
				Plural: strings.ToLower(kind) + "s",
			},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    InfraRemediationVersion,
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

func newTestRemediationTemplateCR(kind, namespace, name string) *unstructured.Unstructured {
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
		Group:   InfraRemediationGroup,
		Version: InfraRemediationVersion,
		Kind:    kind + "Template",
	})
	template.SetNamespace(namespace)
	template.SetName(name)
	return template
}

func newRemediationCR(name string, nodeName string, templateRef v1.ObjectReference, owner metav1.OwnerReference) *unstructured.Unstructured {

	// check template if it supports multiple same kind remediation
	// not needed (and fails for missing k8sclient) for MHC tests
	mutipleSameKindSupported := false
	if k8sClient != nil {
		template := &unstructured.Unstructured{}
		template.SetGroupVersionKind(templateRef.GroupVersionKind())
		key := client.ObjectKey{
			Namespace: templateRef.Namespace,
			Name:      templateRef.Name,
		}
		Expect(k8sClient.Get(context.Background(), key, template)).To(Succeed())
		if annotations.HasMultipleTemplatesAnnotation(template) {
			mutipleSameKindSupported = true
		}
	}

	cr := unstructured.Unstructured{}
	cr.SetNamespace(templateRef.Namespace)
	if mutipleSameKindSupported {
		cr.SetGenerateName(fmt.Sprintf("%s-", name))
	} else {
		cr.SetName(name)
	}

	kind := templateRef.GroupVersionKind().Kind
	// remove trailing template
	kind = kind[:len(kind)-len("template")]
	cr.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   templateRef.GroupVersionKind().Group,
		Version: templateRef.GroupVersionKind().Version,
		Kind:    kind,
	})
	cr.SetOwnerReferences([]metav1.OwnerReference{owner})
	ann := map[string]string{
		commonannotations.NodeNameAnnotation: nodeName,
		annotations.TemplateNameAnnotation:   templateRef.Name,
	}
	cr.SetAnnotations(ann)
	return &cr
}
