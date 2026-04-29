/*
Copyright 2026.

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

package servicemonitor

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/medik8s/node-healthcheck-operator/internal/controller/cluster"
)

func TestServiceMonitor(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ServiceMonitor Suite")
}

var _ = Describe("ServiceMonitor", func() {
	var ctx context.Context
	var log logr.Logger

	BeforeEach(func() {
		ctx = context.Background()
		log = ctrl.Log.WithName("servicemonitor-test")
	})

	Describe("CreateOrUpdate", func() {
		When("on non-OpenShift cluster", func() {
			It("should skip creation and return nil", func() {
				caps := cluster.Capabilities{IsOnOpenshift: false}
				err := CreateOrUpdate(ctx, nil, caps, "test-namespace", log)
				Expect(err).To(BeNil())
			})
		})
	})

	Describe("getServiceMonitor", func() {
		It("should create ServiceMonitor with correct metadata", func() {
			namespace := "test-namespace"
			sm := getServiceMonitor(namespace, nil)

			Expect(sm.GetName()).To(Equal(ServiceMonitorName))
			Expect(sm.GetNamespace()).To(Equal(namespace))
			Expect(sm.GetLabels()).To(HaveKeyWithValue("app.kubernetes.io/component", "controller-manager"))
		})

		It("should create ServiceMonitor with correct scrape class", func() {
			namespace := "default"
			sm := getServiceMonitor(namespace, nil)

			spec := sm.Object["spec"].(map[string]interface{})
			Expect(spec["scrapeClass"]).To(Equal(ScrapeClass))
		})

		It("should create ServiceMonitor with correct label selectors", func() {
			namespace := "default"
			sm := getServiceMonitor(namespace, nil)

			spec := sm.Object["spec"].(map[string]interface{})
			selector := spec["selector"].(map[string]interface{})
			matchLabels := selector["matchLabels"].(map[string]interface{})

			Expect(matchLabels).To(HaveKeyWithValue("app.kubernetes.io/component", "controller-manager"))
			Expect(matchLabels).To(HaveKeyWithValue("app.kubernetes.io/name", "node-healthcheck-operator"))
			Expect(matchLabels).To(HaveKeyWithValue("app.kubernetes.io/instance", "metrics"))
		})

		It("should create ServiceMonitor with correct endpoint configuration", func() {
			namespace := "default"
			sm := getServiceMonitor(namespace, nil)

			spec := sm.Object["spec"].(map[string]interface{})
			endpoints := spec["endpoints"].([]interface{})
			Expect(endpoints).To(HaveLen(1))

			endpoint := endpoints[0].(map[string]interface{})
			Expect(endpoint["port"]).To(Equal(MetricsPort))
			Expect(endpoint["scheme"]).To(Equal(MetricsScheme))
			Expect(endpoint["interval"]).To(Equal(ScrapeInterval))
		})

		It("should construct dynamic serverName from namespace", func() {
			namespace := "custom-namespace"
			sm := getServiceMonitor(namespace, nil)

			spec := sm.Object["spec"].(map[string]interface{})
			endpoints := spec["endpoints"].([]interface{})
			endpoint := endpoints[0].(map[string]interface{})
			tlsConfig := endpoint["tlsConfig"].(map[string]interface{})

			expectedServerName := "node-healthcheck-controller-manager-metrics-service.custom-namespace.svc"
			Expect(tlsConfig["serverName"]).To(Equal(expectedServerName))
		})

		It("should set CA ConfigMap correctly", func() {
			namespace := "default"
			sm := getServiceMonitor(namespace, nil)

			spec := sm.Object["spec"].(map[string]interface{})
			endpoints := spec["endpoints"].([]interface{})
			endpoint := endpoints[0].(map[string]interface{})
			tlsConfig := endpoint["tlsConfig"].(map[string]interface{})
			ca := tlsConfig["ca"].(map[string]interface{})
			configMap := ca["configMap"].(map[string]interface{})

			Expect(configMap["name"]).To(Equal(CAConfigMapName))
			Expect(configMap["key"]).To(Equal(CAConfigMapKey))
		})

		It("should use correct ServiceMonitor name", func() {
			namespace := "default"
			sm := getServiceMonitor(namespace, nil)

			Expect(sm.GetName()).To(Equal("node-healthcheck-controller-manager-metrics-monitor"))
		})

		It("should work with default namespace", func() {
			namespace := "openshift-workload-availability"
			sm := getServiceMonitor(namespace, nil)

			spec := sm.Object["spec"].(map[string]interface{})
			endpoints := spec["endpoints"].([]interface{})
			endpoint := endpoints[0].(map[string]interface{})
			tlsConfig := endpoint["tlsConfig"].(map[string]interface{})

			expectedServerName := "node-healthcheck-controller-manager-metrics-service.openshift-workload-availability.svc"
			Expect(tlsConfig["serverName"]).To(Equal(expectedServerName))
		})

		It("should set owner reference when deployment is provided", func() {
			namespace := "default"
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "node-healthcheck-controller-manager",
					Namespace: namespace,
					UID:       types.UID("test-uid-12345"),
				},
			}
			sm := getServiceMonitor(namespace, deployment)

			ownerRefs := sm.GetOwnerReferences()
			Expect(ownerRefs).To(HaveLen(1))
			Expect(ownerRefs[0].Kind).To(Equal("Deployment"))
			Expect(ownerRefs[0].Name).To(Equal("node-healthcheck-controller-manager"))
			Expect(ownerRefs[0].UID).To(Equal(types.UID("test-uid-12345")))
			Expect(*ownerRefs[0].Controller).To(BeTrue())
		})

		It("should not set owner reference when deployment is nil", func() {
			namespace := "default"
			sm := getServiceMonitor(namespace, nil)

			ownerRefs := sm.GetOwnerReferences()
			Expect(ownerRefs).To(BeEmpty())
		})
	})

	Describe("Constants", func() {
		It("should have correct constant values", func() {
			Expect(ServiceName).To(Equal("node-healthcheck-controller-manager-metrics-service"))
			Expect(CAConfigMapName).To(Equal("node-healthcheck-ca-bundle"))
			Expect(CAConfigMapKey).To(Equal("service-ca.crt"))
			Expect(MetricsPort).To(Equal("https"))
			Expect(MetricsScheme).To(Equal("https"))
			Expect(ScrapeInterval).To(Equal("15s"))
			Expect(ScrapeClass).To(Equal("tls-client-certificate-auth"))
			Expect(ClusterMonitoringLabel).To(Equal("openshift.io/cluster-monitoring"))
			Expect(ClusterMonitoringValue).To(Equal("true"))
		})
	})
})
