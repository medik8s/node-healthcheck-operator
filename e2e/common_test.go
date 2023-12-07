package e2e

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func fetchRemediationResourceByName(name string, namespace string, remediationResource schema.GroupVersionResource) func() error {
	return func() error {
		rem, err := dynamicClient.Resource(remediationResource).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			log.Info("didn't find remediation resource yet", "name", name)
			return err
		}
		log.Info("found remediation resource", "name", rem.GetName())
		return nil
	}
}
