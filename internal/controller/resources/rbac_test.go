package resources_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

var _ = Describe("generated manager ClusterRole", func() {
	It("grants update on nodes", func() {
		raw, err := os.ReadFile("../../../config/rbac/role.yaml")
		Expect(err).NotTo(HaveOccurred())

		role := &rbacv1.ClusterRole{}
		Expect(yaml.Unmarshal(raw, role)).To(Succeed())

		var nodeVerbs []string
		for _, rule := range role.Rules {
			if containsAny(rule.APIGroups, "", rbacv1.APIGroupAll) &&
				containsAny(rule.Resources, "nodes", rbacv1.ResourceAll) {
				nodeVerbs = append(nodeVerbs, rule.Verbs...)
			}
		}
		Expect(nodeVerbs).To(ContainElement("patch"),
			"core/nodes must allow patch — removeConfirmedHealthyAnnotation uses client.MergeFrom")
	})
})

func containsAny(haystack []string, needles ...string) bool {
	for _, h := range haystack {
		for _, n := range needles {
			if h == n {
				return true
			}
		}
	}
	return false
}
