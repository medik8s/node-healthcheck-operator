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

package initializer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap/zapcore"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	consolev1 "github.com/openshift/api/console/v1"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var k8sManager manager.Manager
var testEnv *envtest.Environment
var ctx context.Context
var cancel context.CancelFunc

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bootstrap Suite")
}

var _ = BeforeSuite(func() {
	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.RFC3339NanoTimeEncoder,
	}
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseFlagOptions(&opts)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDInstallOptions: envtest.CRDInstallOptions{
			Paths: []string{
				filepath.Join("..", "..", "vendor", "github.com", "openshift", "api", "console", "v1alpha1"),
				filepath.Join("..", "..", "config", "crd", "bases"),
			},
			ErrorIfPathMissing: true,
		},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	scheme.AddToScheme(scheme.Scheme)
	Expect(remediationv1alpha1.AddToScheme(scheme.Scheme)).NotTo(HaveOccurred())
	Expect(consolev1.AddToScheme(scheme.Scheme)).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	k8sManager, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: server.Options{
			BindAddress: "0",
		},
	})
	Expect(err).NotTo(HaveOccurred())

	os.Setenv("DEPLOYMENT_NAMESPACE", "default")

	go func() {
		defer GinkgoRecover()
		// https://github.com/kubernetes-sigs/controller-runtime/issues/1571
		ctx, cancel = context.WithCancel(ctrl.SetupSignalHandler())
		err := k8sManager.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

	// no clue why, but do not use the context from above!
	Expect(k8sManager.GetCache().WaitForCacheSync(context.Background())).To(BeTrue(), "failed to wait for caches to sync")
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
