package resources_test

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	commonannotations "github.com/medik8s/common/pkg/annotations"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	nhcapi "github.com/medik8s/node-healthcheck-operator/v5/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/v5/internal/controller/resources"
)

func newIdentityTestManager(t *testing.T) resources.Manager {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := nhcapi.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return resources.NewManager(fake.NewClientBuilder().WithScheme(scheme).Build(), context.Background(), logr.Discard(), false, nil, record.NewFakeRecorder(10))
}

func newIdentityTestTemplate(apiVersion, kind string, annotations map[string]string) *unstructured.Unstructured {
	template := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind + "Template",
		"metadata":   map[string]any{"name": "template", "namespace": "remediators"},
		"spec":       map[string]any{"template": map[string]any{"spec": map[string]any{}}},
	}}
	template.SetAnnotations(annotations)
	return template
}

func TestRemediationCRCarriesSelectedNodeIdentity(t *testing.T) {
	owner := &nhcapi.NodeHealthCheck{TypeMeta: metav1.TypeMeta{APIVersion: nhcapi.GroupVersion.String(), Kind: "NodeHealthCheck"}, ObjectMeta: metav1.ObjectMeta{Name: "workers", UID: "nhc-a"}}
	for _, tc := range []struct {
		name, apiVersion, kind string
		systemUUID             string
		templateAnnotations    map[string]string
		wantSystemUUID         bool
	}{
		{name: "FAR", apiVersion: "fence-agents-remediation.medik8s.io/v1alpha1", kind: "FenceAgentsRemediation", systemUUID: "4C4C4544-0033-3910-8053-B4C04F4A3732", wantSystemUUID: true},
		{name: "SNR", apiVersion: "self-node-remediation.medik8s.io/v1alpha1", kind: "SelfNodeRemediation", systemUUID: "4C4C4544-0033-3910-8053-B4C04F4A3732", wantSystemUUID: true},
		{name: "MDR", apiVersion: "machine-deletion-remediation.medik8s.io/v1alpha1", kind: "MachineDeletionRemediation", systemUUID: "4C4C4544-0033-3910-8053-B4C04F4A3732", wantSystemUUID: true},
		{name: "Node without system UUID", apiVersion: "self-node-remediation.medik8s.io/v1alpha1", kind: "SelfNodeRemediation"},
		{name: "template presets the keys", apiVersion: "fence-agents-remediation.medik8s.io/v1alpha1", kind: "FenceAgentsRemediation", templateAnnotations: map[string]string{
			commonannotations.NodeUIDAnnotation:        "other-node",
			commonannotations.NodeSystemUUIDAnnotation: "00000000-0000-0000-0000-000000000001",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-a", UID: "node-a"}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{SystemUUID: tc.systemUUID}}}
			cr, err := newIdentityTestManager(t).GenerateRemediationCRForNode(node, owner, newIdentityTestTemplate(tc.apiVersion, tc.kind, tc.templateAnnotations))
			if err != nil {
				t.Fatal(err)
			}
			annotations := cr.GetAnnotations()
			if got := annotations[commonannotations.NodeNameAnnotation]; got != "worker-a" {
				t.Errorf("node name annotation = %q, want worker-a", got)
			}
			if got := annotations[commonannotations.NodeUIDAnnotation]; got != "node-a" {
				t.Errorf("node UID annotation = %q, want node-a", got)
			}
			got, found := annotations[commonannotations.NodeSystemUUIDAnnotation]
			if tc.wantSystemUUID && got != tc.systemUUID {
				t.Errorf("node system UUID annotation = %q, want the Node's %q as reported", got, tc.systemUUID)
			}
			if !tc.wantSystemUUID && found {
				t.Errorf("node system UUID annotation = %q, want none for a Node that reports none", got)
			}
		})
	}
}

func TestMachineRemediationCRCarriesNoNodeIdentity(t *testing.T) {
	machine := &machinev1beta1.Machine{TypeMeta: metav1.TypeMeta{APIVersion: "machine.openshift.io/v1beta1", Kind: "Machine"}, ObjectMeta: metav1.ObjectMeta{Name: "machine-a", Namespace: "openshift-machine-api", UID: "machine-a"}}
	owner := &metav1.PartialObjectMetadata{TypeMeta: metav1.TypeMeta{APIVersion: "machine.openshift.io/v1beta1", Kind: "MachineHealthCheck"}, ObjectMeta: metav1.ObjectMeta{Name: "workers", UID: "mhc-a"}}
	cr, err := newIdentityTestManager(t).GenerateRemediationCRForMachine(machine, owner, newIdentityTestTemplate("metal3.io/v1beta1", "Metal3Remediation", nil), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{commonannotations.NodeUIDAnnotation, commonannotations.NodeSystemUUIDAnnotation} {
		if got, found := cr.GetAnnotations()[key]; found {
			t.Errorf("%s = %q on a Machine remediation CR, want none", key, got)
		}
	}
}
