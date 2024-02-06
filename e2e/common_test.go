package e2e

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func ensureRemediationResourceExists(name string, namespace string, remediationResource schema.GroupVersionResource) func() error {
	return func() error {
		_, err := dynamicClient.Resource(remediationResource).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				log.Info("didn't find remediation resource yet")
			} else {
				log.Error(err, "failed to get remediation resource")
			}
			return err
		}
		log.Info("found remediation resource")
		return nil
	}
}
