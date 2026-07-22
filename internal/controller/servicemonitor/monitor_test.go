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
	"fmt"
	"os"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/node-healthcheck-operator/internal/controller/cluster"
)

// getServiceMonitorFromCluster fetches the ServiceMonitor from the cluster and returns its spec fields
func getServiceMonitorFromCluster(namespace string) *unstructured.Unstructured {
	sm := &unstructured.Unstructured{}
	sm.SetGroupVersionKind(serviceMonitorGVK)
	err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ServiceMonitorName}, sm)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return sm
}

func getServerNameFromServiceMonitor(sm *unstructured.Unstructured) string {
	spec, ok := sm.Object["spec"].(map[string]interface{})
	ExpectWithOffset(1, ok).To(BeTrue(), "ServiceMonitor should have a spec")
	endpoints, ok := spec["endpoints"].([]interface{})
	ExpectWithOffset(1, ok).To(BeTrue(), "spec should have endpoints")
	ExpectWithOffset(1, endpoints).ToNot(BeEmpty())
	endpoint, ok := endpoints[0].(map[string]interface{})
	ExpectWithOffset(1, ok).To(BeTrue(), "endpoint should be a map")
	tlsConfig, ok := endpoint["tlsConfig"].(map[string]interface{})
	ExpectWithOffset(1, ok).To(BeTrue(), "endpoint should have tlsConfig")
	serverName, ok := tlsConfig["serverName"].(string)
	ExpectWithOffset(1, ok).To(BeTrue(), "tlsConfig should have serverName")
	return serverName
}

var namespaceCounter int

func createTestNamespace(name string) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, ns)).To(Succeed())
}

func uniqueNamespace(prefix string) string {
	namespaceCounter++
	name := fmt.Sprintf("%s-%d", prefix, namespaceCounter)
	createTestNamespace(name)
	return name
}

var _ = Describe("getServiceMonitor", func() {
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

		spec, ok := sm.Object["spec"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "ServiceMonitor should have a spec")
		Expect(spec["scrapeClass"]).To(Equal(ScrapeClass))
	})

	It("should create ServiceMonitor with correct label selectors", func() {
		namespace := "default"
		sm := getServiceMonitor(namespace, nil)

		spec, ok := sm.Object["spec"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "ServiceMonitor should have a spec")
		selector, ok := spec["selector"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "spec should have selector")
		matchLabels, ok := selector["matchLabels"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "selector should have matchLabels")

		Expect(matchLabels).To(HaveKeyWithValue("app.kubernetes.io/component", "controller-manager"))
		Expect(matchLabels).To(HaveKeyWithValue("app.kubernetes.io/name", "node-healthcheck-operator"))
		Expect(matchLabels).To(HaveKeyWithValue("app.kubernetes.io/instance", "metrics"))
	})

	It("should create ServiceMonitor with correct endpoint configuration", func() {
		namespace := "default"
		sm := getServiceMonitor(namespace, nil)

		spec, ok := sm.Object["spec"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "ServiceMonitor should have a spec")
		endpoints, ok := spec["endpoints"].([]interface{})
		Expect(ok).To(BeTrue(), "spec should have endpoints")
		Expect(endpoints).To(HaveLen(1))

		endpoint, ok := endpoints[0].(map[string]interface{})
		Expect(ok).To(BeTrue(), "endpoint should be a map")
		Expect(endpoint["port"]).To(Equal(MetricsPort))
		Expect(endpoint["scheme"]).To(Equal(MetricsScheme))
		Expect(endpoint["interval"]).To(Equal(ScrapeInterval))
	})

	It("should construct dynamic serverName from namespace", func() {
		namespace := "custom-namespace"
		sm := getServiceMonitor(namespace, nil)

		spec, ok := sm.Object["spec"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "ServiceMonitor should have a spec")
		endpoints, ok := spec["endpoints"].([]interface{})
		Expect(ok).To(BeTrue(), "spec should have endpoints")
		endpoint, ok := endpoints[0].(map[string]interface{})
		Expect(ok).To(BeTrue(), "endpoint should be a map")
		tlsConfig, ok := endpoint["tlsConfig"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "endpoint should have tlsConfig")

		expectedServerName := "node-healthcheck-controller-manager-metrics-service.custom-namespace.svc"
		Expect(tlsConfig["serverName"]).To(Equal(expectedServerName))
	})

	It("should set CA ConfigMap correctly", func() {
		namespace := "default"
		sm := getServiceMonitor(namespace, nil)

		spec, ok := sm.Object["spec"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "ServiceMonitor should have a spec")
		endpoints, ok := spec["endpoints"].([]interface{})
		Expect(ok).To(BeTrue(), "spec should have endpoints")
		endpoint, ok := endpoints[0].(map[string]interface{})
		Expect(ok).To(BeTrue(), "endpoint should be a map")
		tlsConfig, ok := endpoint["tlsConfig"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "endpoint should have tlsConfig")
		ca, ok := tlsConfig["ca"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "tlsConfig should have ca")
		configMap, ok := ca["configMap"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "ca should have configMap")

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

		spec, ok := sm.Object["spec"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "ServiceMonitor should have a spec")
		endpoints, ok := spec["endpoints"].([]interface{})
		Expect(ok).To(BeTrue(), "spec should have endpoints")
		endpoint, ok := endpoints[0].(map[string]interface{})
		Expect(ok).To(BeTrue(), "endpoint should be a map")
		tlsConfig, ok := endpoint["tlsConfig"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "endpoint should have tlsConfig")

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

var _ = Describe("createOrUpdateServiceMonitor", func() {
	It("should create ServiceMonitor when it does not exist", func() {
		namespace := uniqueNamespace("sm-create")
		Expect(createOrUpdateServiceMonitor(ctx, namespace, k8sClient, nil)).To(Succeed())

		sm := getServiceMonitorFromCluster(namespace)
		expectedServerName := fmt.Sprintf("%s.%s.svc", ServiceName, namespace)
		Expect(getServerNameFromServiceMonitor(sm)).To(Equal(expectedServerName))
	})

	It("should update existing ServiceMonitor when called again", func() {
		namespace := uniqueNamespace("sm-create")
		Expect(createOrUpdateServiceMonitor(ctx, namespace, k8sClient, nil)).To(Succeed())

		// Verify no owner reference initially
		sm := getServiceMonitorFromCluster(namespace)
		Expect(sm.GetOwnerReferences()).To(BeEmpty())

		// Update with a deployment owner
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-deployment",
				Namespace: namespace,
				UID:       types.UID("update-test-uid"),
			},
		}
		Expect(createOrUpdateServiceMonitor(ctx, namespace, k8sClient, deployment)).To(Succeed())

		// Verify owner reference was added
		sm = getServiceMonitorFromCluster(namespace)
		ownerRefs := sm.GetOwnerReferences()
		Expect(ownerRefs).To(HaveLen(1))
		Expect(ownerRefs[0].Name).To(Equal("test-deployment"))
		Expect(ownerRefs[0].UID).To(Equal(types.UID("update-test-uid")))
	})

	It("should be idempotent when called multiple times", func() {
		namespace := uniqueNamespace("sm-create")
		Expect(createOrUpdateServiceMonitor(ctx, namespace, k8sClient, nil)).To(Succeed())
		Expect(createOrUpdateServiceMonitor(ctx, namespace, k8sClient, nil)).To(Succeed())
		Expect(createOrUpdateServiceMonitor(ctx, namespace, k8sClient, nil)).To(Succeed())

		sm := getServiceMonitorFromCluster(namespace)
		expectedServerName := fmt.Sprintf("%s.%s.svc", ServiceName, namespace)
		Expect(getServerNameFromServiceMonitor(sm)).To(Equal(expectedServerName))
	})

	It("should produce different serverNames for different namespaces", func() {
		// Core regression test: each namespace must get its own serverName, not a hardcoded one
		ns1 := uniqueNamespace("ns-custom-a")
		ns2 := uniqueNamespace("ns-custom-b")
		ns3 := uniqueNamespace("ns-owa")
		for _, ns := range []string{ns1, ns2, ns3} {
			Expect(createOrUpdateServiceMonitor(ctx, ns, k8sClient, nil)).To(Succeed())
		}

		for _, ns := range []string{ns1, ns2, ns3} {
			sm := getServiceMonitorFromCluster(ns)
			expectedServerName := fmt.Sprintf("%s.%s.svc", ServiceName, ns)
			Expect(getServerNameFromServiceMonitor(sm)).To(Equal(expectedServerName),
				"serverName mismatch for namespace %s", ns)
		}
	})
})

var _ = Describe("labelNamespace", func() {
	It("should add cluster-monitoring label to namespace", func() {
		namespace := uniqueNamespace("sm-label")
		Expect(labelNamespace(ctx, namespace, k8sClient)).To(Succeed())

		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, ns)).To(Succeed())
		Expect(ns.Labels).To(HaveKeyWithValue(ClusterMonitoringLabel, ClusterMonitoringValue))
	})

	It("should be idempotent when label already exists", func() {
		namespace := uniqueNamespace("sm-label")
		Expect(labelNamespace(ctx, namespace, k8sClient)).To(Succeed())
		Expect(labelNamespace(ctx, namespace, k8sClient)).To(Succeed())

		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, ns)).To(Succeed())
		Expect(ns.Labels).To(HaveKeyWithValue(ClusterMonitoringLabel, ClusterMonitoringValue))
	})

	It("should not overwrite existing namespace labels", func() {
		namespace := uniqueNamespace("sm-label")
		// Pre-label the namespace with a custom label
		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, ns)).To(Succeed())
		base := ns.DeepCopy()
		if ns.Labels == nil {
			ns.Labels = make(map[string]string)
		}
		ns.Labels["custom-label"] = "custom-value"
		Expect(k8sClient.Patch(ctx, ns, client.MergeFrom(base))).To(Succeed())

		Expect(labelNamespace(ctx, namespace, k8sClient)).To(Succeed())

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, ns)).To(Succeed())
		Expect(ns.Labels).To(HaveKeyWithValue(ClusterMonitoringLabel, ClusterMonitoringValue))
		Expect(ns.Labels).To(HaveKeyWithValue("custom-label", "custom-value"))
	})

	It("should return error for non-existent namespace", func() {
		err := labelNamespace(ctx, "non-existent-namespace", k8sClient)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("could not fetch namespace"))
	})
})

var _ = Describe("CreateOrUpdate integration", func() {
	var log logr.Logger
	var originalPodName string
	var podNameWasSet bool

	BeforeEach(func() {
		log = ctrl.Log.WithName("servicemonitor-integration-test")
		originalPodName, podNameWasSet = os.LookupEnv("POD_NAME")
		os.Unsetenv("POD_NAME")
	})

	AfterEach(func() {
		if podNameWasSet {
			os.Setenv("POD_NAME", originalPodName)
		} else {
			os.Unsetenv("POD_NAME")
		}
	})

	It("should skip ServiceMonitor creation on non-OpenShift clusters", func() {
		namespace := uniqueNamespace("sm-integration")
		caps := cluster.Capabilities{IsOnOpenshift: false}
		Expect(CreateOrUpdate(ctx, k8sClient, caps, namespace, log)).To(Succeed())

		// Verify no ServiceMonitor was created
		sm := &unstructured.Unstructured{}
		sm.SetGroupVersionKind(serviceMonitorGVK)
		err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ServiceMonitorName}, sm)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not found"))

		// Verify namespace was NOT labeled for cluster monitoring
		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, ns)).To(Succeed())
		Expect(ns.Labels).ToNot(HaveKey(ClusterMonitoringLabel))
	})

	It("should skip with nil client on non-OpenShift", func() {
		caps := cluster.Capabilities{IsOnOpenshift: false}
		Expect(CreateOrUpdate(ctx, nil, caps, "any-namespace", log)).To(Succeed())
	})

	It("should create ServiceMonitor and label namespace on OpenShift", func() {
		namespace := uniqueNamespace("sm-integration")
		caps := cluster.Capabilities{IsOnOpenshift: true}
		// POD_NAME not set — owner reference will be skipped gracefully
		Expect(CreateOrUpdate(ctx, k8sClient, caps, namespace, log)).To(Succeed())

		// Verify ServiceMonitor exists with correct serverName
		sm := getServiceMonitorFromCluster(namespace)
		expectedServerName := fmt.Sprintf("%s.%s.svc", ServiceName, namespace)
		Expect(getServerNameFromServiceMonitor(sm)).To(Equal(expectedServerName))

		// Verify namespace was labeled
		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, ns)).To(Succeed())
		Expect(ns.Labels).To(HaveKeyWithValue(ClusterMonitoringLabel, ClusterMonitoringValue))
	})

	It("should create ServiceMonitor with correct serverName in custom namespace", func() {
		customNs := uniqueNamespace("my-custom-operator-ns")

		caps := cluster.Capabilities{IsOnOpenshift: true}
		Expect(CreateOrUpdate(ctx, k8sClient, caps, customNs, log)).To(Succeed())

		sm := getServiceMonitorFromCluster(customNs)
		expectedServerName := fmt.Sprintf("%s.%s.svc", ServiceName, customNs)
		Expect(getServerNameFromServiceMonitor(sm)).To(Equal(expectedServerName),
			"serverName must reflect the custom namespace, not a hardcoded default")
	})

	When("called for both default and custom namespaces", func() {
		It("should produce distinct serverNames per namespace", func() {
			defaultNs := uniqueNamespace("openshift-wa")
			customNs := uniqueNamespace("customer-ns")

			caps := cluster.Capabilities{IsOnOpenshift: true}
			Expect(CreateOrUpdate(ctx, k8sClient, caps, defaultNs, log)).To(Succeed())
			Expect(CreateOrUpdate(ctx, k8sClient, caps, customNs, log)).To(Succeed())

			smDefault := getServiceMonitorFromCluster(defaultNs)
			smCustom := getServiceMonitorFromCluster(customNs)

			Expect(getServerNameFromServiceMonitor(smDefault)).To(
				Equal(fmt.Sprintf("%s.%s.svc", ServiceName, defaultNs)))
			Expect(getServerNameFromServiceMonitor(smCustom)).To(
				Equal(fmt.Sprintf("%s.%s.svc", ServiceName, customNs)))
			Expect(getServerNameFromServiceMonitor(smDefault)).ToNot(
				Equal(getServerNameFromServiceMonitor(smCustom)),
				"serverNames must differ across namespaces")
		})
	})
})
