package resources_test

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	commonannotations "github.com/medik8s/common/pkg/annotations"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	nhcapi "github.com/medik8s/node-healthcheck-operator/v5/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/v5/internal/controller/resources"
)

func TestSelectedNodeBinding(t *testing.T) {
	for _, tc := range []struct {
		name, uid, uuid         string
		safety, preset, wantErr bool
		foreign                 bool
		missingName, mhc        bool
		policy                  any
	}{
		{name: "legacy"},
		{name: "missing Node name", safety: true, missingName: true, uid: "node-a", uuid: "00112233-4455-4677-8899-aabbccddeeff", wantErr: true},
		{name: "MHC generation does not bind", safety: true, mhc: true, uid: "node-a", uuid: "00112233-4455-4677-8899-aabbccddeeff"},
		{name: "other remediator", safety: true, foreign: true},
		{name: "unknown policy", safety: true, policy: "unknown", wantErr: true},
		{name: "malformed policy", safety: true, policy: int64(1), wantErr: true},
		{name: "uppercase UUID", safety: true, uid: "node-a", uuid: "00112233-4455-4677-8899-AABBCCDDEEFF"},
		{name: "bound", safety: true, uid: "node-a", uuid: "00112233-4455-4677-8899-aabbccddeeff"},
		{name: "missing UID", safety: true, uuid: "00112233-4455-4677-8899-aabbccddeeff", wantErr: true},
		{name: "missing UUID", safety: true, uid: "node-a", wantErr: true},
		{name: "malformed UUID", safety: true, uid: "node-a", uuid: "not-a-uuid", wantErr: true},
		{name: "zero UUID", safety: true, uid: "node-a", uuid: "00000000-0000-0000-0000-000000000000", wantErr: true},
		{name: "ones UUID", safety: true, uid: "node-a", uuid: "ffffffff-ffff-ffff-ffff-ffffffffffff", wantErr: true},
		{name: "preset identity", safety: true, uid: "node-a", uuid: "00112233-4455-4677-8899-aabbccddeeff", preset: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			safety := tc.safety
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := nhcapi.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			manager := resources.NewManager(fake.NewClientBuilder().WithScheme(scheme).Build(), context.Background(), logr.Discard(), false, nil, record.NewFakeRecorder(10))
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-a", UID: "node-a"}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{SystemUUID: "00112233-4455-4677-8899-aabbccddeeff"}}}
			owner := &nhcapi.NodeHealthCheck{TypeMeta: metav1.TypeMeta{APIVersion: nhcapi.GroupVersion.String(), Kind: "NodeHealthCheck"}, ObjectMeta: metav1.ObjectMeta{Name: "workers", UID: "nhc-a"}}
			spec := map[string]any{"agent": "fence_redfish", "retrycount": int64(1), "remediationStrategy": "OutOfServiceTaint"}
			if tc.missingName {
				node.Name = ""
			}
			node.UID = types.UID(tc.uid)
			node.Status.NodeInfo.SystemUUID = tc.uuid
			if safety {
				spec["safetyPolicy"] = "RedfishOffV1"
			}
			if tc.policy != nil {
				spec["safetyPolicy"] = tc.policy
			}
			if tc.preset {
				spec["nodeBinding"] = map[string]any{"uid": "injected"}
			}
			template := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "fence-agents-remediation.medik8s.io/v1alpha1", "kind": "FenceAgentsRemediationTemplate", "metadata": map[string]any{"name": "off-template", "namespace": "bmc"}, "spec": map[string]any{"template": map[string]any{"spec": spec}}}}
			if tc.foreign {
				template.SetAPIVersion("remediation.example/v1alpha1")
			}
			var cr *unstructured.Unstructured
			var err error
			if tc.mhc {
				machine := &machinev1beta1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "machine-a", Namespace: "machines", UID: "machine-uid"}}
				cr, err = manager.GenerateRemediationCRForMachine(machine, owner, template, node.Name)
			} else {
				cr, err = manager.GenerateRemediationCRForNode(node, owner, template)
			}
			if tc.wantErr {
				if err == nil {
					t.Fatal("unsafe identity accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cr.GetAnnotations()[commonannotations.NodeNameAnnotation] != node.Name || len(cr.GetOwnerReferences()) != 1 || cr.GetOwnerReferences()[0].UID != owner.UID {
				t.Fatal("generation control failed")
			}
			if _, found, err := unstructured.NestedFieldNoCopy(template.Object, "spec", "template", "spec", "nodeBinding"); found || err != nil {
				t.Fatal("generation mutated the shared template")
			}
			binding, found, err := unstructured.NestedStringMap(cr.Object, "spec", "nodeBinding")
			if err != nil {
				t.Fatal(err)
			}
			if !safety || tc.foreign || tc.mhc {
				if found {
					t.Fatal("legacy generation changed")
				}
				return
			}
			for field, want := range map[string]string{"version": "v1", "name": node.Name, "uid": string(node.UID), "systemUUID": strings.ToLower(node.Status.NodeInfo.SystemUUID)} {
				if binding[field] != want {
					t.Errorf("binding.%s = %q, want %q", field, binding[field], want)
				}
			}
		})
	}
}
