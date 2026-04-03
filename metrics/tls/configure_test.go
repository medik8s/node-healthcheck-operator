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

package metricstls_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/server/dynamiccertificates"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	metricstls "github.com/medik8s/node-healthcheck-operator/metrics/tls"
)

// --- test helpers ---

// generateCA creates a self-signed Certificate Authority. It returns the
// CA certificate and private key, which can be passed to generateCert to
// create certificates that any party trusting this CA will accept.
func generateCA(cn string) (certPEM, keyPEM []byte, cert *x509.Certificate, key *rsa.PrivateKey, err error) {
	key, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	cert = &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	// re-parse so cert.Raw is populated
	cert, _ = x509.ParseCertificate(der)
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, cert, key, nil
}

// generateCert creates a new certificate (server or client) and signs it
// using the provided CA certificate and private key, so that any party
// trusting that CA will accept the generated certificate.
func generateCert(caCert *x509.Certificate, caKey *rsa.PrivateKey, cn string, isServer bool) (certPEM, keyPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	if isServer {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.IPAddresses = []net.IP{net.IPv4(127, 0, 0, 1)}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, nil
}

func writeToTempDir(certPEM, keyPEM []byte, certName, keyName string) (string, error) {
	dir, err := os.MkdirTemp("", "metricstls-test-*")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, certName), certPEM, 0600); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, keyName), keyPEM, 0600); err != nil {
		return "", err
	}
	return dir, nil
}

// createCAConfigMap creates or updates the extension-apiserver-authentication
// ConfigMap in kube-system with the given CA PEM.
func createCAConfigMap(caPEM []byte) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "extension-apiserver-authentication",
			Namespace: metav1.NamespaceSystem,
		},
		Data: map[string]string{
			"client-ca-file": string(caPEM),
		},
	}
	_, err := testKubeClient.CoreV1().ConfigMaps(metav1.NamespaceSystem).Get(testCtx, cm.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = testKubeClient.CoreV1().ConfigMaps(metav1.NamespaceSystem).Create(testCtx, cm, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
	} else {
		Expect(err).NotTo(HaveOccurred()) // fail on unexpected Get errors
		_, err = testKubeClient.CoreV1().ConfigMaps(metav1.NamespaceSystem).Update(testCtx, cm, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())
	}
}

// primeController creates the ConfigMap, starts the controller, and waits for VerifyOptions to succeed.
func primeController(controller *dynamiccertificates.ConfigMapCAController, caPEM []byte) {
	createCAConfigMap(caPEM)

	controllerCtx, controllerCancel := context.WithCancel(testCtx)
	go controller.Run(controllerCtx, 1)

	Eventually(func() bool {
		_, ok := controller.VerifyOptions()
		return ok
	}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())

	// Store cancel in closure for DeferCleanup
	DeferCleanup(func() {
		controllerCancel()
	})
}

const (
	certName = "tls.crt"
	keyName  = "tls.key"
)

var _ = Describe("ConfigureMTLS", func() {
	var log = logf.Log.WithName("metricstls-test")

	Context("when TLS cert files are absent", func() {
		It("returns nil controller and no error", func() {
			// cert files absent → no-op
			metricsOpts := server.Options{}
			controller, err := metricstls.ConfigureMTLS(
				&metricsOpts, testKubeClient,
				"/nonexistent/path", certName, keyName,
				log,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(controller).To(BeNil())
		})
	})

	Context("when TLS cert files are present", func() {
		var (
			serverCACertPEM []byte
			serverCAKey     *rsa.PrivateKey
			serverCACert    *x509.Certificate
			serverCertDir   string
		)

		BeforeEach(func() {
			var err error
			// Generate a CA for the server certificate (separate from client CA)
			serverCACertPEM, _, serverCACert, serverCAKey, err = generateCA("server-ca")
			Expect(err).NotTo(HaveOccurred())
			_ = serverCACertPEM // suppress lint: assigned here, read in nested It closures

			// Generate server cert signed by server CA
			serverCertPEM, serverKeyPEM, err := generateCert(serverCACert, serverCAKey, "localhost", true)
			Expect(err).NotTo(HaveOccurred())

			serverCertDir, err = writeToTempDir(serverCertPEM, serverKeyPEM, certName, keyName)
			Expect(err).NotTo(HaveOccurred())

			DeferCleanup(func() {
				os.RemoveAll(serverCertDir)
			})
		})

		Context("GetConfigForClient callback", func() {
			When("CA bundle is loaded", func() {
				It("returns cloned config with RequireAndVerifyClientCert, correct ClientCAs, and preserved NextProtos", func() {
					// verify callback content after controller primed
					clientCACertPEM, _, _, _, err := generateCA("client-ca-test-1")
					Expect(err).NotTo(HaveOccurred())

					metricsOpts := server.Options{
						TLSOpts: []func(*tls.Config){
							func(c *tls.Config) { c.NextProtos = []string{"http/1.1"} },
						},
					}
					controller, err := metricstls.ConfigureMTLS(
						&metricsOpts, testKubeClient,
						serverCertDir, certName, keyName,
						log,
					)
					Expect(err).NotTo(HaveOccurred())
					Expect(controller).NotTo(BeNil())

					primeController(controller, clientCACertPEM)

					// Apply all TLSOpts to a base config, simulating what the server does
					baseCfg := &tls.Config{}
					for _, fn := range metricsOpts.TLSOpts {
						fn(baseCfg)
					}
					// Verify ConfigureMTLS set the GetConfigForClient callback for mTLS
					Expect(baseCfg.GetConfigForClient).NotTo(BeNil())

					cfg, err := baseCfg.GetConfigForClient(&tls.ClientHelloInfo{})
					Expect(err).NotTo(HaveOccurred())
					Expect(cfg.ClientAuth).To(Equal(tls.RequireAndVerifyClientCert))
					Expect(cfg.ClientCAs).NotTo(BeNil())
					Expect(cfg.NextProtos).To(ContainElement("http/1.1"))
				})
			})

			When("CA bundle not yet loaded", func() {
				It("returns error 'client CA bundle not yet available'", func() {
					// controller created but not primed
					metricsOpts := server.Options{}
					controller, err := metricstls.ConfigureMTLS(
						&metricsOpts, testKubeClient,
						serverCertDir, certName, keyName,
						log,
					)
					Expect(err).NotTo(HaveOccurred())
					Expect(controller).NotTo(BeNil())

					// Don't prime controller - CA bundle not loaded
					baseCfg := &tls.Config{}
					for _, fn := range metricsOpts.TLSOpts {
						fn(baseCfg)
					}

					_, err = baseCfg.GetConfigForClient(&tls.ClientHelloInfo{})
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("client CA bundle not yet available"))
				})
			})
		})

		Context("mTLS handshake", func() {
			var (
				clientCACertPEM []byte
				clientCACert    *x509.Certificate
				clientCAKey     *rsa.PrivateKey
			)

			BeforeEach(func() {
				var err error
				clientCACertPEM, _, clientCACert, clientCAKey, err = generateCA("client-ca-handshake")
				Expect(err).NotTo(HaveOccurred())
			})

			// startTLSListener creates a real TLS listener using the metricsOpts
			// and returns the listener address for dialing.
			startTLSListener := func(metricsOpts *server.Options) net.Listener {
				// Build server TLS config
				serverCert, err := tls.LoadX509KeyPair(
					filepath.Join(metricsOpts.CertDir, metricsOpts.CertName),
					filepath.Join(metricsOpts.CertDir, metricsOpts.KeyName),
				)
				Expect(err).NotTo(HaveOccurred())

				// Replicate controller-runtime metric server boot
				// 1. create a tls.Config with the server certificates
				serverCfg := &tls.Config{
					Certificates: []tls.Certificate{serverCert},
				}
				// 2. apply all TLSOpts functions to it (included our ConfigureMTLS)
				for _, fn := range metricsOpts.TLSOpts {
					fn(serverCfg)
				}

				// Use a raw TCP listener so we can handle TLS handshakes manually
				tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
				Expect(err).NotTo(HaveOccurred())

				go func() {
					defer GinkgoRecover()
					for {
						conn, err := tcpListener.Accept()
						if err != nil {
							return // listener closed
						}
						go func(c net.Conn) {
							tlsConn := tls.Server(c, serverCfg)
							// Complete the TLS handshake
							if err := tlsConn.Handshake(); err != nil {
								// Handshake failed (e.g. bad client cert) — close and return
								tlsConn.Close()
								return
							}
							// Signal success: write one byte so the client can
							// distinguish a successful handshake from a failed one.
							tlsConn.Write([]byte("K"))
							tlsConn.Close()
						}(conn)
					}
				}()

				DeferCleanup(func() {
					tcpListener.Close()
				})

				return tcpListener
			}

			dialTLS := func(addr string, clientCertPEM, clientKeyPEM, caCertPEM []byte) error {
				// Load the certificates the client presents to the metric server.
				// The metric server will verify them via the client CA the ConfigMapCAController reads
				clientCert, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
				if err != nil {
					return fmt.Errorf("failed to load client cert: %w", err)
				}

				// Create the Root CA Bundle the client uses to verify server's certificate
				// NOTE: caCertPEM is the server CA certificate
				caPool := x509.NewCertPool()
				caPool.AppendCertsFromPEM(caCertPEM)

				dialer := &net.Dialer{Timeout: 5 * time.Second}
				conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
					Certificates: []tls.Certificate{clientCert},
					RootCAs:      caPool,
					ServerName:   "127.0.0.1",
				})
				if err != nil {
					return err
				}
				defer conn.Close()
				// Read one byte from the server. The server writes "K" on
				// successful handshake, so if we can read it the mTLS
				// connection was accepted. Any error (alert, EOF, etc.)
				// means the server rejected the client certificate.
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				buf := make([]byte, 1)
				_, err = conn.Read(buf)
				return err
			}

			When("client has valid cert from trusted CA", func() {
				It("accepts the connection", func() {
					metricsOpts := server.Options{}
					controller, err := metricstls.ConfigureMTLS(
						&metricsOpts, testKubeClient,
						serverCertDir, certName, keyName,
						log,
					)
					Expect(err).NotTo(HaveOccurred())
					primeController(controller, clientCACertPEM)

					listener := startTLSListener(&metricsOpts)

					clientCertPEM, clientKeyPEM, err := generateCert(clientCACert, clientCAKey, "test-client", false)
					Expect(err).NotTo(HaveOccurred())

					err = dialTLS(listener.Addr().String(), clientCertPEM, clientKeyPEM, serverCACertPEM)
					Expect(err).NotTo(HaveOccurred())
				})
			})

			When("client has cert from untrusted CA", func() {
				It("rejects the connection", func() {
					metricsOpts := server.Options{}
					controller, err := metricstls.ConfigureMTLS(
						&metricsOpts, testKubeClient,
						serverCertDir, certName, keyName,
						log,
					)
					Expect(err).NotTo(HaveOccurred())
					primeController(controller, clientCACertPEM)

					listener := startTLSListener(&metricsOpts)

					// Generate a different untrusted CA
					_, _, untrustedCACert, untrustedCAKey, err := generateCA("untrusted-ca")
					Expect(err).NotTo(HaveOccurred())

					untrustedClientCertPEM, untrustedClientKeyPEM, err := generateCert(untrustedCACert, untrustedCAKey, "untrusted-client", false)
					Expect(err).NotTo(HaveOccurred())

					err = dialTLS(listener.Addr().String(), untrustedClientCertPEM, untrustedClientKeyPEM, serverCACertPEM)
					Expect(err).To(HaveOccurred())
				})
			})

			When("CA is rotated in ConfigMap", func() {
				It("accepts clients signed by the new CA", func() {
					metricsOpts := server.Options{}
					controller, err := metricstls.ConfigureMTLS(
						&metricsOpts, testKubeClient,
						serverCertDir, certName, keyName,
						log,
					)
					Expect(err).NotTo(HaveOccurred())
					primeController(controller, clientCACertPEM)

					// The listener starts with the old CA
					listener := startTLSListener(&metricsOpts)

					// Generate a new CA
					newCACertPEM, _, newCACert, newCAKey, err := generateCA("new-client-ca")
					Expect(err).NotTo(HaveOccurred())

					// Rotate: update the ConfigMap to the new CA
					createCAConfigMap(newCACertPEM)

					newClientCertPEM, newClientKeyPEM, err := generateCert(newCACert, newCAKey, "new-client", false)
					Expect(err).NotTo(HaveOccurred())

					// Wait for controller to pick up the new CA
					Eventually(func() error {
						return dialTLS(listener.Addr().String(), newClientCertPEM, newClientKeyPEM, serverCACertPEM)
					}, 10*time.Second, 500*time.Millisecond).Should(Succeed())
				})

				It("rejects clients signed by the old CA", func() {
					metricsOpts := server.Options{}
					controller, err := metricstls.ConfigureMTLS(
						&metricsOpts, testKubeClient,
						serverCertDir, certName, keyName,
						log,
					)
					Expect(err).NotTo(HaveOccurred())
					primeController(controller, clientCACertPEM)

					listener := startTLSListener(&metricsOpts)

					// Generate a new CA
					newCACertPEM, _, _, _, err := generateCA("replacement-client-ca")
					Expect(err).NotTo(HaveOccurred())

					// Rotate: update the ConfigMap to the new CA only
					createCAConfigMap(newCACertPEM)

					// Sign the client certificate with the old CA
					oldClientCertPEM, oldClientKeyPEM, err := generateCert(clientCACert, clientCAKey, "old-client", false)
					Expect(err).NotTo(HaveOccurred())

					Eventually(func() error {
						return dialTLS(listener.Addr().String(), oldClientCertPEM, oldClientKeyPEM, serverCACertPEM)
					}, 10*time.Second, 500*time.Millisecond).ShouldNot(Succeed())
				})
			})
		})
	})
})
