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

package v1alpha1

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

const (
	WebhookCertDir  = "/apiserver.local.config/certificates"
	WebhookCertName = "apiserver.crt"
	WebhookKeyName  = "apiserver.key"
)

// log is for logging in this package.
var nodehealthchecklog = logf.Log.WithName("nodehealthcheck-resource")

func (r *NodeHealthCheck) SetupWebhookWithManager(mgr ctrl.Manager) error {

	// check if OLM injected certs
	certs := []string{filepath.Join(WebhookCertDir, WebhookCertName), filepath.Join(WebhookCertDir, WebhookKeyName)}
	certsInjected := true
	for _, fname := range certs {
		if _, err := os.Stat(fname); err != nil {
			certsInjected = false
			break
		}
	}
	if certsInjected {
		server := mgr.GetWebhookServer()
		server.CertDir = WebhookCertDir
		server.CertName = WebhookCertName
		server.KeyName = WebhookKeyName
	} else {
		nodehealthchecklog.Info("OLM injected certs for webhooks not found")
	}

	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

//+kubebuilder:webhook:path=/validate-remediation-medik8s-io-v1alpha1-nodehealthcheck,mutating=false,failurePolicy=fail,sideEffects=None,groups=remediation.medik8s.io,resources=nodehealthchecks,verbs=update;delete,versions=v1alpha1,name=vnodehealthcheck.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &NodeHealthCheck{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *NodeHealthCheck) ValidateCreate() error {
	nodehealthchecklog.Info("validate create", "name", r.Name)
	return nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *NodeHealthCheck) ValidateUpdate(old runtime.Object) error {
	nodehealthchecklog.Info("validate update", "name", r.Name)
	if r.isRemediating() {
		return fmt.Errorf("update prohibited due to running remediations")
	}
	return nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *NodeHealthCheck) ValidateDelete() error {
	nodehealthchecklog.Info("validate delete", "name", r.Name)
	if r.isRemediating() {
		return fmt.Errorf("deletion prohibited due to running remediations")
	}
	return nil
}

func (r *NodeHealthCheck) isRemediating() bool {
	return len(r.Status.InFlightRemediations) > 0
}
