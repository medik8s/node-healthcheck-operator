/*
Copyright 2022.

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

package console

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
	"github.com/openshift/api/console/v1alpha1"
)

const (
	// PluginName is the name of the plugin and uses at several places
	PluginName = "node-remediation-console-plugin"
	// ServiceName is the name of the console plugin Service and must match the name of the Service in /bundle/manifests!
	ServiceName = "node-healthcheck-operator-node-remediation-console-plugin"
	// ServicePort is the port of the console plugin Service and must match the port of the Service in /bundle/manifests!
	ServicePort = 9443
)

// +kubebuilder:rbac:groups=console.openshift.io,resources=consoleplugins,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch

// CreateOrUpdatePlugin creates or updates the resources needed for the remediation console plugin.
// HEADS UP: consider cleanup of old resources in case of name changes or removals!
//
// TODO image reference to console plugin in CSV?
func CreateOrUpdatePlugin(ctx context.Context, mgr ctrl.Manager, namespace string, log logr.Logger) error {

	// check if we are on OCP
	if isOpenshift, err := utils.IsOnOpenshift(mgr.GetConfig()); err != nil {
		return errors.Wrap(err, "failed to check if we are on Openshift")
	} else if !isOpenshift {
		log.Info("we are not on Openshift, skipping console plugin activation")
		return nil
	}

	// reader is needed because mgr isn't started yet, so caches are not filled yet and client.get fails
	reader := mgr.GetAPIReader()
	cl := mgr.GetClient()

	// Create ConsolePlugin resource
	// Deployment and Service are deployed by OLM
	if err := createOrUpdateConsolePlugin(ctx, namespace, reader, cl); err != nil {
		return err
	}

	log.Info("successfully created / updated console plugin resources")
	return nil
}

func createOrUpdateConsolePlugin(ctx context.Context, namespace string, reader client.Reader, cl client.Client) error {
	cp := getConsolePlugin(namespace)
	oldCP := &v1alpha1.ConsolePlugin{}
	if err := reader.Get(ctx, client.ObjectKeyFromObject(cp), oldCP); apierrors.IsNotFound(err) {
		if err := cl.Create(ctx, cp); err != nil {
			return errors.Wrap(err, "could not create console plugin")
		}
	} else if err != nil {
		return errors.Wrap(err, "could not check for existing console plugin")
	} else {
		oldCP.OwnerReferences = cp.OwnerReferences
		oldCP.Spec = cp.Spec
		if err := cl.Update(ctx, oldCP); err != nil {
			return errors.Wrap(err, "could not update console plugin")
		}
	}
	return nil
}

func getConsolePlugin(namespace string) *v1alpha1.ConsolePlugin {
	return &v1alpha1.ConsolePlugin{
		ObjectMeta: metav1.ObjectMeta{
			Name: PluginName,
			// TODO set owner ref for deletion when operator is uninstalled
			// but which resource to use, needs to be cluster scoped
		},
		Spec: v1alpha1.ConsolePluginSpec{
			DisplayName: "Node Remediation",
			Service: v1alpha1.ConsolePluginService{
				Name:      ServiceName,
				Namespace: namespace,
				Port:      ServicePort,
				BasePath:  "/",
			},
		},
	}
}
