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

package metricsclientca

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	testCAData    = "-----BEGIN CERTIFICATE-----\nMIIBkTCB+wIJAKHBfpB+cJIeMA0GCSqGSIb3DQEBCwUAMBExDzANBgNVBAMMBnRl\nc3QtY2EwHhcNMjMwMTAxMDAwMDAwWhcNMjQwMTAxMDAwMDAwWjARMQ8wDQYDVQQD\nDAZ0ZXN0LWNhMFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBALFl8TfIIkTRk9z7Y8eF\nLzA+A3aLA3K5FJfkI2QQ+MFZ+cMwL+E3rEzKLlGMPxNJl7sPBKZmNqRVLl0ELdUM\nSwUCAwEAAaNTMFEwHQYDVR0OBBYEFBqA8LhA5OJqJbSvOl+GdVWL9YNLMB8GA1Ud\nIwQYMBaAFBqA8LhA5OJqJbSvOl+GdVWL9YNLMA8GA1UdEwEB/wQFMAMBAf8wDQYJ\nKoZIhvcNAQELBQADQQB1\n-----END CERTIFICATE-----"
	updatedCAData = "-----BEGIN CERTIFICATE-----\nMIIBkTCB+wIJAKHBfpB+cJIfMA0GCSqGSIb3DQEBCwUAMBExDzANBgNVBAMMBnVw\nZGF0ZWQwHhcNMjMwMTAxMDAwMDAwWhcNMjQwMTAxMDAwMDAwWjARMQ8wDQYDVQQD\nDAZ1cGRhdGVkMFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBALFl8TfIIkTRk9z7Y8eF\nLzA+A3aLA3K5FJfkI2QQ+MFZ+cMwL+E3rEzKLlGMPxNJl7sPBKZmNqRVLl0ELdUM\nSwUCAwEAAaNTMFEwHQYDVR0OBBYEFBqA8LhA5OJqJbSvOl+GdVWL9YNLMB8GA1Ud\nIwQYMBaAFBqA8LhA5OJqJbSvOl+GdVWL9YNLMA8GA1UdEwEB/wQFMAMBAf8wDQYJ\nKoZIhvcNAQELBQADQQB2\n-----END CERTIFICATE-----"
)

// createOrUpdateConfigMap creates the ConfigMap if it doesn't exist, or updates it if it does
func createOrUpdateConfigMap(cm *corev1.ConfigMap) {
	existing := &corev1.ConfigMap{}
	err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cm), existing)
	if errors.IsNotFound(err) {
		ExpectWithOffset(1, k8sClient.Create(context.Background(), cm)).To(Succeed())
	} else {
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		existing.Data = cm.Data
		ExpectWithOffset(1, k8sClient.Update(context.Background(), existing)).To(Succeed())
	}
}

var _ = Describe("MetricsClientCA Sync Controller", func() {

	Context("When source ConfigMap contains client-ca-file key", func() {
		BeforeEach(func() {
			createOrUpdateConfigMap(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      SourceConfigMap,
					Namespace: SourceNamespace,
				},
				Data: map[string]string{
					SourceKey: testCAData,
				},
			})
		})

		It("Should create or update target ConfigMap with CA data", func() {
			By("Verifying the target ConfigMap is created with correct data")
			Eventually(func() string {
				targetCM := &corev1.ConfigMap{}
				if err := k8sClient.Get(context.Background(), types.NamespacedName{
					Namespace: testOperatorNamespace,
					Name:      TargetConfigMap,
				}, targetCM); err != nil {
					return ""
				}
				return targetCM.Data[TargetKey]
			}, 10*time.Second, 100*time.Millisecond).Should(Equal(testCAData))
		})
	})

	Context("When source ConfigMap is updated", func() {
		BeforeEach(func() {
			createOrUpdateConfigMap(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      SourceConfigMap,
					Namespace: SourceNamespace,
				},
				Data: map[string]string{
					SourceKey: testCAData,
				},
			})

			// Wait for target to be created/synced
			Eventually(func() string {
				targetCM := &corev1.ConfigMap{}
				if err := k8sClient.Get(context.Background(), types.NamespacedName{
					Namespace: testOperatorNamespace,
					Name:      TargetConfigMap,
				}, targetCM); err != nil {
					return ""
				}
				return targetCM.Data[TargetKey]
			}, 10*time.Second, 100*time.Millisecond).Should(Equal(testCAData))
		})

		It("Should update target ConfigMap when source CA changes", func() {
			By("Updating the source ConfigMap with new CA data")
			sourceConfigMap := &corev1.ConfigMap{}
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{
				Namespace: SourceNamespace,
				Name:      SourceConfigMap,
			}, sourceConfigMap)).To(Succeed())
			sourceConfigMap.Data[SourceKey] = updatedCAData
			Expect(k8sClient.Update(context.Background(), sourceConfigMap)).To(Succeed())

			By("Verifying the target ConfigMap is updated")
			Eventually(func() string {
				targetCM := &corev1.ConfigMap{}
				if err := k8sClient.Get(context.Background(), types.NamespacedName{
					Namespace: testOperatorNamespace,
					Name:      TargetConfigMap,
				}, targetCM); err != nil {
					return ""
				}
				return targetCM.Data[TargetKey]
			}, 10*time.Second, 100*time.Millisecond).Should(Equal(updatedCAData))
		})
	})

	Context("When source ConfigMap does not have the expected key", func() {
		BeforeEach(func() {
			// Set source ConfigMap without the expected key
			createOrUpdateConfigMap(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      SourceConfigMap,
					Namespace: SourceNamespace,
				},
				Data: map[string]string{
					"some-other-key": "some-data",
				},
			})

			// Delete target ConfigMap if it exists
			targetCM := &corev1.ConfigMap{}
			if err := k8sClient.Get(context.Background(), types.NamespacedName{
				Namespace: testOperatorNamespace,
				Name:      TargetConfigMap,
			}, targetCM); err == nil {
				Expect(k8sClient.Delete(context.Background(), targetCM)).To(Succeed())
				// Wait for deletion
				Eventually(func() bool {
					err := k8sClient.Get(context.Background(), types.NamespacedName{
						Namespace: testOperatorNamespace,
						Name:      TargetConfigMap,
					}, &corev1.ConfigMap{})
					return errors.IsNotFound(err)
				}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())
			}
		})

		It("Should not create target ConfigMap", func() {
			By("Verifying the target ConfigMap is NOT created")
			Consistently(func() bool {
				err := k8sClient.Get(context.Background(), types.NamespacedName{
					Namespace: testOperatorNamespace,
					Name:      TargetConfigMap,
				}, &corev1.ConfigMap{})
				return errors.IsNotFound(err)
			}, 3*time.Second, 100*time.Millisecond).Should(BeTrue())
		})
	})

	Context("When target ConfigMap already exists with different data", func() {
		BeforeEach(func() {
			// Create target ConfigMap with old data
			createOrUpdateConfigMap(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      TargetConfigMap,
					Namespace: testOperatorNamespace,
				},
				Data: map[string]string{
					TargetKey: "old-ca-data",
				},
			})

			// Set source ConfigMap with correct data
			createOrUpdateConfigMap(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      SourceConfigMap,
					Namespace: SourceNamespace,
				},
				Data: map[string]string{
					SourceKey: testCAData,
				},
			})
		})

		It("Should update target ConfigMap to match source", func() {
			By("Verifying the target ConfigMap is updated")
			Eventually(func() string {
				targetCM := &corev1.ConfigMap{}
				if err := k8sClient.Get(context.Background(), types.NamespacedName{
					Namespace: testOperatorNamespace,
					Name:      TargetConfigMap,
				}, targetCM); err != nil {
					return ""
				}
				return targetCM.Data[TargetKey]
			}, 10*time.Second, 100*time.Millisecond).Should(Equal(testCAData))
		})
	})

	Context("When target ConfigMap is modified/reverted (e.g., during upgrade)", func() {
		BeforeEach(func() {
			// Set source ConfigMap with correct data
			createOrUpdateConfigMap(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      SourceConfigMap,
					Namespace: SourceNamespace,
				},
				Data: map[string]string{
					SourceKey: testCAData,
				},
			})

			// Wait for target to be synced
			Eventually(func() string {
				targetCM := &corev1.ConfigMap{}
				if err := k8sClient.Get(context.Background(), types.NamespacedName{
					Namespace: testOperatorNamespace,
					Name:      TargetConfigMap,
				}, targetCM); err != nil {
					return ""
				}
				return targetCM.Data[TargetKey]
			}, 10*time.Second, 100*time.Millisecond).Should(Equal(testCAData))
		})

		It("Should heal target ConfigMap back to source CA", func() {
			By("Modifying target ConfigMap with placeholder data (simulating external modification)")
			targetCM := &corev1.ConfigMap{}
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{
				Namespace: testOperatorNamespace,
				Name:      TargetConfigMap,
			}, targetCM)).To(Succeed())
			targetCM.Data[TargetKey] = "placeholder-ca-data"
			Expect(k8sClient.Update(context.Background(), targetCM)).To(Succeed())

			By("Verifying the target ConfigMap is healed back to source CA")
			Eventually(func() string {
				cm := &corev1.ConfigMap{}
				if err := k8sClient.Get(context.Background(), types.NamespacedName{
					Namespace: testOperatorNamespace,
					Name:      TargetConfigMap,
				}, cm); err != nil {
					return ""
				}
				return cm.Data[TargetKey]
			}, 10*time.Second, 100*time.Millisecond).Should(Equal(testCAData))
		})
	})
})
