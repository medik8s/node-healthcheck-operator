package e2e

import (
	"context"
	"fmt"

	commonannotations "github.com/medik8s/common/pkg/annotations"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func ensureRemediationResourceExists(name string, namespace string, remediationResource schema.GroupVersionResource) func() error {
	return func() error {
		// The CR name doesn't always match the node name in case multiple CRs of same type for the same node are supported
		// So list all, and look for the node name in the annotation
		list, err := dynamicClient.Resource(remediationResource).Namespace(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				log.Info("didn't find remediation resource yet")
			} else {
				log.Error(err, "failed to get remediation resource")
			}
			return err
		}
		for _, cr := range list.Items {
			if nodeName, exists := cr.GetAnnotations()[commonannotations.NodeNameAnnotation]; exists && nodeName == name {
				log.Info("found remediation resource")
				return nil
			}
		}
		log.Info("didn't find remediation resource yet")
		return fmt.Errorf("not found")
	}
}
