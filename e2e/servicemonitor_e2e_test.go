package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var serviceMonitorGVR = schema.GroupVersionResource{
	Group:    "monitoring.coreos.com",
	Version:  "v1",
	Resource: "servicemonitors",
}

var _ = Describe("ServiceMonitor", Ordered, labelOcpOnly, func() {

	const (
		serviceMonitorName = "node-healthcheck-controller-manager-metrics-monitor"
		serviceName        = "node-healthcheck-controller-manager-metrics-service"
	)

	var sm *unstructured.Unstructured

	BeforeAll(func() {
		// Wait for ServiceMonitor to be created by the operator at startup
		Eventually(func(g Gomega) {
			var err error
			sm, err = dynamicClient.Resource(serviceMonitorGVR).Namespace(operatorNsName).Get(
				context.Background(),
				serviceMonitorName,
				metav1.GetOptions{},
			)
			g.Expect(err).ToNot(HaveOccurred(), "ServiceMonitor should exist in operator namespace")
		}, 30*time.Second, 2*time.Second).Should(Succeed())
	})

	It("should have correct dynamic serverName matching the operator namespace", func() {
		spec, ok := sm.Object["spec"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "ServiceMonitor should have a spec")

		endpoints, ok := spec["endpoints"].([]interface{})
		Expect(ok).To(BeTrue(), "spec should have endpoints")
		Expect(endpoints).ToNot(BeEmpty())

		endpoint, ok := endpoints[0].(map[string]interface{})
		Expect(ok).To(BeTrue(), "endpoint should be a map")

		tlsConfig, ok := endpoint["tlsConfig"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "endpoint should have tlsConfig")

		expectedServerName := fmt.Sprintf("%s.%s.svc", serviceName, operatorNsName)
		Expect(tlsConfig["serverName"]).To(Equal(expectedServerName),
			"serverName must be dynamically constructed from the operator namespace, not hardcoded")
	})

	It("should have cluster-monitoring label on operator namespace", func() {
		ns, err := clientSet.CoreV1().Namespaces().Get(context.Background(), operatorNsName, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(ns.Labels).To(HaveKeyWithValue("openshift.io/cluster-monitoring", "true"),
			"operator namespace must be labeled for Prometheus discovery")
	})

	It("should have correct TLS CA configuration", func() {
		spec, ok := sm.Object["spec"].(map[string]interface{})
		Expect(ok).To(BeTrue())

		endpoints, ok := spec["endpoints"].([]interface{})
		Expect(ok).To(BeTrue())

		endpoint, ok := endpoints[0].(map[string]interface{})
		Expect(ok).To(BeTrue())

		tlsConfig, ok := endpoint["tlsConfig"].(map[string]interface{})
		Expect(ok).To(BeTrue())

		ca, ok := tlsConfig["ca"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "tlsConfig should have ca")

		configMap, ok := ca["configMap"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "ca should have configMap")

		Expect(configMap["name"]).To(Equal("node-healthcheck-ca-bundle"),
			"CA ConfigMap name should reference the service-ca injected bundle")
		Expect(configMap["key"]).To(Equal("service-ca.crt"))
	})

	It("should use the tls-client-certificate-auth scrape class", func() {
		spec, ok := sm.Object["spec"].(map[string]interface{})
		Expect(ok).To(BeTrue())

		Expect(spec["scrapeClass"]).To(Equal("tls-client-certificate-auth"),
			"scrapeClass must be set for Platform Prometheus mTLS")
	})
})
