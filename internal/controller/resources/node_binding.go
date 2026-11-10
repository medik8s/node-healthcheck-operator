package resources

import (
	"fmt"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var systemUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func bindSelectedNode(cr *unstructured.Unstructured, node *corev1.Node) error {
	if cr.GroupVersionKind().Group != "fence-agents-remediation.medik8s.io" || cr.GetKind() != "FenceAgentsRemediation" {
		return nil
	}
	policy, found, err := unstructured.NestedString(cr.Object, "spec", "safetyPolicy")
	if err != nil {
		return fmt.Errorf("read FAR safety policy: %w", err)
	}
	if !found || policy == "" {
		return nil
	}
	if policy != "RedfishOffV1" {
		return fmt.Errorf("unsupported FAR safety policy")
	}
	if _, found, err := unstructured.NestedFieldNoCopy(cr.Object, "spec", "nodeBinding"); err != nil || found {
		return fmt.Errorf("FAR templates must not supply a node binding")
	}
	uuid := strings.ToLower(node.Status.NodeInfo.SystemUUID)
	if node.Name == "" || node.UID == "" || !systemUUIDPattern.MatchString(uuid) || uuid == "00000000-0000-0000-0000-000000000000" || uuid == "ffffffff-ffff-ffff-ffff-ffffffffffff" {
		return fmt.Errorf("selected Node has no valid UID and system UUID")
	}
	if err := unstructured.SetNestedStringMap(cr.Object, map[string]string{"version": "v1", "name": node.Name, "uid": string(node.UID), "systemUUID": uuid}, "spec", "nodeBinding"); err != nil {
		return fmt.Errorf("set selected Node binding: %w", err)
	}
	return nil
}
