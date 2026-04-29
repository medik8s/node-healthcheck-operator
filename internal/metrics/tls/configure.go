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

package metricstls

import (
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-logr/logr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/server/dynamiccertificates"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// ConfigureMTLS checks whether TLS cert files are present in certDir and,
// if so, configures metricsOpts for mTLS by creating a
// ConfigMapCAController that watches the client CA bundle in the
// extension-apiserver-authentication ConfigMap. If the cert files are
// absent the function is a no-op and returns (nil, nil).
func ConfigureMTLS(
	metricsOpts *server.Options,
	kubeClient kubernetes.Interface,
	certDir, certName, keyName string,
	log logr.Logger,
) (*dynamiccertificates.ConfigMapCAController, error) {

	certFile := filepath.Join(certDir, certName)
	keyFile := filepath.Join(certDir, keyName)

	if _, err := os.Stat(certFile); err != nil {
		log.Info("mTLS certificate not present, skipping mTLS configuration", "certFile", certFile)
		return nil, nil
	}
	if _, err := os.Stat(keyFile); err != nil {
		log.Info("mTLS key not present, skipping mTLS configuration", "keyFile", keyFile)
		return nil, nil
	}

	log.Info("Configuring secure metrics with mTLS")

	clientCAController, err := dynamiccertificates.NewDynamicCAFromConfigMapController(
		"metrics-client-ca", metav1.NamespaceSystem,
		"extension-apiserver-authentication", "client-ca-file", kubeClient)
	if err != nil {
		return nil, fmt.Errorf("unable to create client CA controller: %w", err)
	}

	metricsOpts.SecureServing = true
	metricsOpts.CertDir = certDir
	metricsOpts.CertName = certName
	metricsOpts.KeyName = keyName
	metricsOpts.TLSOpts = append(metricsOpts.TLSOpts, func(c *tls.Config) {
		c.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
			// Clone the base TLS config so each connection inherits
			// the shared settings (e.g. NextProtos, serving cert) and
			// gets the latest client CA roots without data races.
			cfg := c.Clone()
			cfg.ClientAuth = tls.RequireAndVerifyClientCert
			opts, ok := clientCAController.VerifyOptions()
			if !ok {
				return nil, fmt.Errorf("client CA bundle not yet available")
			}
			cfg.ClientCAs = opts.Roots
			return cfg, nil
		}
	})

	return clientCAController, nil
}
