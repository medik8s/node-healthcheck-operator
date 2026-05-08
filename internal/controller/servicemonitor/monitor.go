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

// TODO: Consider using typed clients from prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1
// once the Kubernetes dependency chain is upgraded. Currently using unstructured clients to avoid
// cascading dependency updates that would break existing NHC webhook APIs.

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/node-healthcheck-operator/internal/controller/cluster"
	"github.com/medik8s/node-healthcheck-operator/internal/controller/utils"
)

const (
	// ServiceName is the name of the metrics Service and must match the name of the Service in the bundle
	ServiceName = "node-healthcheck-controller-manager-metrics-service"
	// CAConfigMapName is the name of the CA ConfigMap containing the service-ca.crt
	CAConfigMapName = "node-healthcheck-ca-bundle"
	// CAConfigMapKey is the key within the CA ConfigMap
	CAConfigMapKey = "service-ca.crt"
	// MetricsPort is the port name for the metrics endpoint
	MetricsPort = "https"
	// MetricsScheme is the scheme for metrics scraping
	MetricsScheme = "https"
	// ScrapeInterval is the interval for scraping metrics
	ScrapeInterval = "15s"
	// ScrapeClass is the OpenShift-specific scrape class for TLS client certificate auth
	ScrapeClass = "tls-client-certificate-auth"
	// ServiceMonitorName is the name of the ServiceMonitor resource
	ServiceMonitorName = "node-healthcheck-controller-manager-metrics-monitor"
	// ClusterMonitoringLabel is the label required by OCP Prometheus Operator to scrape from the namespace
	ClusterMonitoringLabel = "openshift.io/cluster-monitoring"
	ClusterMonitoringValue = "true"
)

var serviceMonitorGVK = schema.GroupVersionKind{
	Group:   "monitoring.coreos.com",
	Version: "v1",
	Kind:    "ServiceMonitor",
}

// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;create;update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list
// +kubebuilder:rbac:groups=apps,resources=replicasets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;patch

// CreateOrUpdate creates or updates the ServiceMonitor resource for metrics scraping on OpenShift.
// On non-OpenShift clusters, this is a no-op since the user creates the ServiceMonitor manually.
func CreateOrUpdate(ctx context.Context, cl client.Client, caps cluster.Capabilities, namespace string, log logr.Logger) error {
	// Check if we are on OCP
	if !caps.IsOnOpenshift {
		log.Info("we are not on Openshift, skipping ServiceMonitor creation")
		return nil
	}

	// Fetch operator Deployment to use as owner reference
	// Use dynamic discovery via ownership chain walking: Pod → ReplicaSet → Deployment
	var deployment *appsv1.Deployment
	podName, err := utils.GetPodName()
	if err != nil {
		log.Info("POD_NAME environment variable not set, ServiceMonitor will be created without owner reference. Delete the ServiceMonitor manually after deleting the operator", "error", err)
	} else {
		deployment, err = utils.GetOwningDeployment(ctx, cl, namespace, podName)
		if err != nil {
			deployment = nil
			log.Info("could not determine operator Deployment via ownership chain, ServiceMonitor will be created without owner reference. Delete the ServiceMonitor manually after deleting the operator", "pod", podName, "namespace", namespace, "error", err)
		} else {
			log.Info("successfully discovered operator Deployment for ServiceMonitor owner reference", "deployment", deployment.Name, "namespace", deployment.Namespace)
		}
	}

	if err := labelNamespace(ctx, namespace, cl); err != nil {
		log.Error(err, "failed to label namespace for cluster monitoring; metrics scraping may not work. Manual intervention required: label the namespace with openshift.io/cluster-monitoring=true")
	}

	if err := createOrUpdateServiceMonitor(ctx, namespace, cl, deployment); err != nil {
		return err
	}

	log.Info("ServiceMonitor reconciliation completed (created/updated or skipped if CRD unavailable)")
	return nil
}

func createOrUpdateServiceMonitor(ctx context.Context, namespace string, cl client.Client, deployment *appsv1.Deployment) error {
	sm := getServiceMonitor(namespace, deployment)
	oldSM := &unstructured.Unstructured{}
	oldSM.SetGroupVersionKind(serviceMonitorGVK)

	if err := cl.Get(ctx, client.ObjectKeyFromObject(sm), oldSM); apierrors.IsNotFound(err) {
		if err := cl.Create(ctx, sm); err != nil {
			// If CRD is not installed, return nil (graceful degradation)
			if meta.IsNoMatchError(err) {
				return nil
			}
			// If another replica already created it during startup race, that's fine
			if apierrors.IsAlreadyExists(err) {
				return nil
			}
			return errors.Wrap(err, "could not create ServiceMonitor")
		}
	} else if err != nil {
		// If CRD is not installed, return nil (graceful degradation)
		if meta.IsNoMatchError(err) {
			return nil
		}
		return errors.Wrap(err, "could not check for existing ServiceMonitor")
	} else {
		// Copy resourceVersion from existing resource to prevent "object has been modified" conflicts
		// This is required for optimistic concurrency control in Kubernetes API server
		sm.Object["metadata"].(map[string]interface{})["resourceVersion"] = oldSM.GetResourceVersion()
		if err := cl.Update(ctx, sm); err != nil {
			return errors.Wrap(err, "could not update ServiceMonitor")
		}
	}
	return nil
}

// labelNamespace adds the cluster-monitoring label to the namespace.
// This label is required by the OCP Prometheus Operator to scrape metrics from the namespace.
// If the namespace is already labeled, this is a no-op. If the labeling fails, the error is returned
// but the caller should not fail—operator continues without the label; manual intervention will be needed.
func labelNamespace(ctx context.Context, namespace string, cl client.Client) error {
	ns := &corev1.Namespace{}
	if err := cl.Get(ctx, types.NamespacedName{Name: namespace}, ns); err != nil {
		return errors.Wrap(err, "could not fetch namespace")
	}

	if ns.Labels == nil {
		ns.Labels = make(map[string]string)
	}

	// Check if label is already set correctly (idempotent check)
	if ns.Labels[ClusterMonitoringLabel] == ClusterMonitoringValue {
		return nil
	}

	base := ns.DeepCopy()

	// Set the label
	ns.Labels[ClusterMonitoringLabel] = ClusterMonitoringValue
	if err := cl.Patch(ctx, ns, client.MergeFrom(base)); err != nil {
		return errors.Wrap(err, "could not patch namespace with cluster-monitoring label")
	}
	return nil
}

// getServiceMonitor builds a ServiceMonitor resource using unstructured types.
// This avoids adding prometheus-operator as a dependency which would require upgrading
// the Kubernetes dependency chain and potentially break existing webhook APIs.
// If deployment is provided, it's set as owner reference so the ServiceMonitor is
// automatically deleted when the operator is uninstalled.
func getServiceMonitor(namespace string, deployment *appsv1.Deployment) *unstructured.Unstructured {
	metadata := map[string]interface{}{
		"name":      ServiceMonitorName,
		"namespace": namespace,
		"labels": map[string]interface{}{
			"app.kubernetes.io/component": "controller-manager",
		},
	}

	// Set owner reference if deployment is available for automatic cascading deletion
	if deployment != nil {
		ownerRefs := []interface{}{
			map[string]interface{}{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"name":       deployment.Name,
				"uid":        string(deployment.UID),
				"controller": true,
			},
		}
		metadata["ownerReferences"] = ownerRefs
	}

	sm := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "ServiceMonitor",
			"metadata":   metadata,
			"spec": map[string]interface{}{
				"scrapeClass": ScrapeClass,
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"app.kubernetes.io/component": "controller-manager",
						"app.kubernetes.io/name":      "node-healthcheck-operator",
						"app.kubernetes.io/instance":  "metrics",
					},
				},
				"endpoints": []interface{}{
					map[string]interface{}{
						"port":     MetricsPort,
						"scheme":   MetricsScheme,
						"interval": ScrapeInterval,
						"tlsConfig": map[string]interface{}{
							"ca": map[string]interface{}{
								"configMap": map[string]interface{}{
									"name": CAConfigMapName,
									"key":  CAConfigMapKey,
								},
							},
							"serverName": fmt.Sprintf("%s.%s.svc", ServiceName, namespace),
						},
					},
				},
			},
		},
	}
	return sm
}
