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

// Package metricsclientca provides a controller to sync the metrics client CA
// from kube-system/extension-apiserver-authentication to the operator namespace.
// This CA is required for Platform Prometheus to authenticate to the operator's
// metrics endpoint using TLS client certificates.
package metricsclientca

import (
	"context"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// SourceNamespace is the namespace containing the extension-apiserver-authentication ConfigMap
	SourceNamespace = "kube-system"
	// SourceConfigMap is the name of the ConfigMap containing the client CA
	SourceConfigMap = "extension-apiserver-authentication"
	// SourceKey is the key in the source ConfigMap containing the client CA
	SourceKey = "client-ca-file"
	// TargetConfigMap is the name of the ConfigMap to create in the operator namespace
	TargetConfigMap = "node-healthcheck-metrics-client-ca"
	// TargetKey is the key to use in the target ConfigMap (same as source for consistency)
	TargetKey = "client-ca-file"
)

// RBAC for this controller is managed in config/optional/prometheus-ocp/
// (not via kubebuilder marker because it's OCP-specific):
// - kube_system_role.yaml: Role to read from kube-system/extension-apiserver-authentication
// - kube_system_role_binding.yaml: RoleBinding for the above
// - configmap_role.yaml: ClusterRole to create/update ConfigMaps
// - configmap_role_binding.yaml: ClusterRoleBinding for the above

// MetricsClientCAReconciler reconciles the metrics client CA ConfigMap
type MetricsClientCAReconciler struct {
	client.Client
	Log               logr.Logger
	OperatorNamespace string
}

// Reconcile syncs the client CA from kube-system/extension-apiserver-authentication
// to the operator namespace for Platform Prometheus mTLS authentication
func (r *MetricsClientCAReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("configmap", req.NamespacedName)

	// Only process the source ConfigMap
	if req.Namespace != SourceNamespace || req.Name != SourceConfigMap {
		return ctrl.Result{}, nil
	}

	log.Info("Reconciling metrics client CA")

	// Get source ConfigMap from kube-system
	source := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: SourceNamespace,
		Name:      SourceConfigMap,
	}, source); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Source ConfigMap not found, nothing to sync")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get source ConfigMap")
		return ctrl.Result{}, err
	}

	// Get the client CA data
	caData, ok := source.Data[SourceKey]
	if !ok {
		log.Info("Source key not found in ConfigMap", "key", SourceKey)
		return ctrl.Result{}, nil
	}

	// Check if target ConfigMap exists
	target := &corev1.ConfigMap{}
	targetKey := types.NamespacedName{
		Namespace: r.OperatorNamespace,
		Name:      TargetConfigMap,
	}

	err := r.Get(ctx, targetKey, target)
	if errors.IsNotFound(err) {
		// Create the target ConfigMap
		target = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      TargetConfigMap,
				Namespace: r.OperatorNamespace,
			},
			Data: map[string]string{
				TargetKey: caData,
			},
		}
		if err := r.Create(ctx, target); err != nil {
			log.Error(err, "Failed to create target ConfigMap")
			return ctrl.Result{}, err
		}
		log.Info("Created target ConfigMap", "namespace", r.OperatorNamespace)
		return ctrl.Result{}, nil
	} else if err != nil {
		log.Error(err, "Failed to get target ConfigMap")
		return ctrl.Result{}, err
	}

	// Update if the CA data has changed
	if target.Data == nil {
		target.Data = make(map[string]string)
	}
	if target.Data[TargetKey] != caData {
		target.Data[TargetKey] = caData
		if err := r.Update(ctx, target); err != nil {
			log.Error(err, "Failed to update target ConfigMap")
			return ctrl.Result{}, err
		}
		log.Info("Updated target ConfigMap", "namespace", r.OperatorNamespace)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager
func (r *MetricsClientCAReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("metrics-client-ca").
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				// Watch the source ConfigMap (kube-system/extension-apiserver-authentication)
				if obj.GetNamespace() == SourceNamespace && obj.GetName() == SourceConfigMap {
					return []reconcile.Request{{
						NamespacedName: types.NamespacedName{
							Namespace: SourceNamespace,
							Name:      SourceConfigMap,
						},
					}}
				}
				// Also watch the target ConfigMap - if it's modified/reverted (e.g., during upgrade),
				// reconcile to ensure it has the correct CA data
				if obj.GetNamespace() == r.OperatorNamespace && obj.GetName() == TargetConfigMap {
					return []reconcile.Request{{
						NamespacedName: types.NamespacedName{
							Namespace: SourceNamespace,
							Name:      SourceConfigMap,
						},
					}}
				}
				return nil
			}),
		).
		Complete(r)
}
