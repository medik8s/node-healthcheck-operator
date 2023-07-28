package utils

import corev1 "k8s.io/api/core/v1"

const NodeProviderIDAnnotation = "nodehealthcheck.medik8s.io/node-provider-id"

func CreateProviderIdAnnotation(node *corev1.Node) map[string]string {
	annotations := map[string]string{}
	annotations[NodeProviderIDAnnotation] = node.Spec.ProviderID
	return annotations
}
