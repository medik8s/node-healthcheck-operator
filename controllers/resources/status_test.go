package resources

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/metrics"
)

func TestUpdateStatusNodeHealthy_MetricsUseNodeName(t *testing.T) {
	// Initialize metrics so the gauge and histogram are registered
	metrics.InitializeNodeHealthCheckMetrics()

	nodeName := "worker-1"
	remediationCRName := "worker-1-snr-a3f2" // different from node name
	remediationNamespace := "test-namespace"
	remediationKind := "SelfNodeRemediation"
	metricName := "nodehealthcheck_ongoing_remediation"
	metricHeader := `
# HELP nodehealthcheck_ongoing_remediation Indication of an ongoing remediation of an unhealthy node
# TYPE nodehealthcheck_ongoing_remediation gauge
`

	nhc := &remediationv1alpha1.NodeHealthCheck{
		Status: remediationv1alpha1.NodeHealthCheckStatus{
			UnhealthyNodes: []*remediationv1alpha1.UnhealthyNode{
				{
					Name: nodeName,
					Remediations: []*remediationv1alpha1.Remediation{
						{
							Resource: corev1.ObjectReference{
								Name:      remediationCRName,
								Namespace: remediationNamespace,
								Kind:      remediationKind,
							},
							Started: metav1.Time{Time: time.Now().Add(-2 * time.Minute)},
						},
					},
				},
			},
		},
	}

	// Stage 1: simulate remediation creation — gauge should be 1
	metrics.ObserveNodeHealthCheckRemediationCreated(nodeName, remediationNamespace, remediationKind)

	expected1 := strings.NewReader(metricHeader +
		`nodehealthcheck_ongoing_remediation{name="worker-1",namespace="test-namespace",remediation="SelfNodeRemediation"} 1
`)
	if err := testutil.GatherAndCompare(crmetrics.Registry, expected1, metricName); err != nil {
		t.Fatalf("stage 1 (after remediation created): unexpected metric output:\n%v", err)
	}

	// Stage 2: call UpdateStatusNodeHealthy — gauge should be 0
	UpdateStatusNodeHealthy(nodeName, nhc)

	expected0 := strings.NewReader(metricHeader +
		`nodehealthcheck_ongoing_remediation{name="worker-1",namespace="test-namespace",remediation="SelfNodeRemediation"} 0
`)
	if err := testutil.GatherAndCompare(crmetrics.Registry, expected0, metricName); err != nil {
		t.Errorf("stage 2 (after remediation deleted): unexpected metric output:\n%v", err)
	}

	// Stage 3: verify the duration histogram was observed with the node name label
	durationMetricName := "nodehealthcheck_unhealthy_node_duration_seconds"
	// gather all the metrics families from the registry
	mfs, err := crmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	var found bool
	for _, mf := range mfs {
		if mf.GetName() != durationMetricName {
			continue
		}
		for _, m := range mf.GetMetric() {
			// get the label pairs from the metric and verify the metric label corresponds to the node's name
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "name" {
					found = true
					if lp.GetValue() != nodeName {
						t.Errorf("stage 3 (duration histogram): expected name label %q, got %q", nodeName, lp.GetValue())
					}
				}
			}
			// verify the number of stored observations
			expectedSampleCount := uint64(1)
			if h := m.GetHistogram(); h != nil && h.GetSampleCount() != expectedSampleCount {
				t.Errorf("stage 3 (duration histogram): expected sample count %d, got %d", expectedSampleCount, h.GetSampleCount())
			}
		}
	}
	if !found {
		t.Error("stage 3 (duration histogram): metric not found in registry")
	}

	// Verify the node was removed from unhealthy nodes
	if len(nhc.Status.UnhealthyNodes) != 0 {
		t.Errorf("expected unhealthy nodes to be empty, got %d", len(nhc.Status.UnhealthyNodes))
	}
}
