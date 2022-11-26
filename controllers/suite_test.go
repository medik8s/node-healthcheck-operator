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
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"go.uber.org/zap/zapcore"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/cluster"
	"github.com/medik8s/node-healthcheck-operator/controllers/mhc"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

const (
	DeploymentNamespace = "testns"
)

var cfg *rest.Config
var k8sClient client.Client
var k8sManager manager.Manager
var testEnv *envtest.Environment
var ctx context.Context
var cancel context.CancelFunc

var upgradeChecker *fakeClusterUpgradeChecker

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.RFC3339NanoTimeEncoder,
	}
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseFlagOptions(&opts)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	scheme.AddToScheme(scheme.Scheme)
	Expect(remediationv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(machinev1beta1.Install(scheme.Scheme)).To(Succeed())
	Expect(apiextensionsv1.AddToScheme(scheme.Scheme)).To(Succeed())
	// +kubebuilder:scaffold:scheme

	k8sManager, err = ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Deploy test remediation CRDs and CR
	Expect(k8sClient.Create(context.Background(), testRemediationCRD)).To(Succeed())
	Expect(k8sClient.Create(context.Background(), testRemediationTemplateCRD)).To(Succeed())
	time.Sleep(time.Second)
	Expect(k8sClient.Create(context.Background(), newTestRemediationTemplateCR())).To(Succeed())

	upgradeChecker = &fakeClusterUpgradeChecker{
		Err:       nil,
		Upgrading: false,
	}

	mhcChecker, err := mhc.NewMHCChecker(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	os.Setenv("DEPLOYMENT_NAMESPACE", DeploymentNamespace)

	err = (&NodeHealthCheckReconciler{
		Client:                      k8sManager.GetClient(),
		Log:                         k8sManager.GetLogger().WithName("test reconciler"),
		Scheme:                      k8sManager.GetScheme(),
		Recorder:                    k8sManager.GetEventRecorderFor("NodeHealthCheck"),
		ClusterUpgradeStatusChecker: upgradeChecker,
		MHCChecker:                  mhcChecker,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&MachineHealthCheckReconciler{
		Client:                      k8sManager.GetClient(),
		Log:                         k8sManager.GetLogger().WithName("test reconciler"),
		Scheme:                      k8sManager.GetScheme(),
		Recorder:                    k8sManager.GetEventRecorderFor("NodeHealthCheck"),
		ClusterUpgradeStatusChecker: upgradeChecker,
		MHCChecker:                  mhcChecker,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		// https://github.com/kubernetes-sigs/controller-runtime/issues/1571
		ctx, cancel = context.WithCancel(ctrl.SetupSignalHandler())
		err := k8sManager.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

}, 60)

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

func (c *fakeClusterUpgradeChecker) Check() (bool, error) {
	return c.Upgrading, c.Err
}

var testRemediationCRD = &apiextensionsv1.CustomResourceDefinition{
	TypeMeta: metav1.TypeMeta{
		APIVersion: apiextensions.SchemeGroupVersion.String(),
		Kind:       "CustomResourceDefinition",
	},
	ObjectMeta: metav1.ObjectMeta{
		Name: "infrastructureremediations.test.medik8s.io",
	},
	Spec: apiextensionsv1.CustomResourceDefinitionSpec{
		Group: "test.medik8s.io",
		Scope: apiextensionsv1.NamespaceScoped,
		Names: apiextensionsv1.CustomResourceDefinitionNames{
			Kind:   "InfrastructureRemediation",
			Plural: "infrastructureremediations",
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

var testRemediationTemplateCRD = &apiextensionsv1.CustomResourceDefinition{
	TypeMeta: metav1.TypeMeta{
		APIVersion: apiextensionsv1.SchemeGroupVersion.String(),
		Kind:       "CustomResourceDefinition",
	},
	ObjectMeta: metav1.ObjectMeta{
		Name: "infrastructureremediationtemplates.test.medik8s.io",
	},
	Spec: apiextensionsv1.CustomResourceDefinitionSpec{
		Group: "test.medik8s.io",
		Scope: apiextensionsv1.NamespaceScoped,
		Names: apiextensionsv1.CustomResourceDefinitionNames{
			Kind:   "InfrastructureRemediationTemplate",
			Plural: "infrastructureremediationtemplates",
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

func newTestRemediationTemplateCR() client.Object {
	remediation := map[string]interface{}{
		"kind":       "InfrastructureRemediation",
		"apiVersion": "test.medik8s.io/v1alpha1",
		"metadata":   map[string]interface{}{},
		"spec": map[string]interface{}{
			"size": "foo",
		},
	}
	template := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"template": remediation,
			},
		},
	}
	template.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "test.medik8s.io",
		Version: "v1alpha1",
		Kind:    "InfrastructureRemediationTemplate",
	})
	template.SetNamespace("default")
	template.SetName("template")
	return template
}
