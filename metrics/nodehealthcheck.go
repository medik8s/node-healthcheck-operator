package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// nodeHealthCheckOldRemediationCR is a Prometheus metric, which reports the number of old Remediation CRs.
	// It is an indication for remediation that is pending for a long while, which might indicate a problem with the external remediation mechanism.
	nodeHealthCheckOldRemediationCR = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "nodehealthcheck_old_remediation_cr",
			Help: "Number of old remediation CRs detected by NodeHealthChecks",
		}, []string{"name", "namespace"},
	)
)

var (
	// nodeHealthCheckOngoingRemediation is a Prometheus metric, which reports an ongoing remediation of a node
	nodeHealthCheckOngoingRemediation = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "nodehealthcheck_ongoing_remediation",
			Help: "Indication of an ongoing remediation of an unhealthy node",
		}, []string{"name", "namespace", "remediation"},
	)
)

var (
	// nodehealtCheckRemediationDuration is a Prometheus metric, which reports the unhealthy node duration
	nodehealtCheckRemediationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "nodehealthcheck_unhealthy_node_duration_seconds",
			Help:    "Unhealthy Node duration distribution",
			Buckets: []float64{30, 60, 120, 180, 240, 300, 600, 1200, 2400, 3600},
		}, []string{"name", "namespace", "remediation"},
	)
)

func InitializeNodeHealthCheckMetrics() {
	metrics.Registry.MustRegister(
		nodeHealthCheckOldRemediationCR,
		nodeHealthCheckOngoingRemediation,
		nodehealtCheckRemediationDuration,
	)
}

func ObserveNodeHealthCheckOldRemediationCR(name, namespace string) {
	nodeHealthCheckOldRemediationCR.With(prometheus.Labels{
		"name":      name,
		"namespace": namespace,
	}).Inc()
}

func ObserveNodeHealthCheckRemediationCreated(name, namespace, remediation string) {
	nodeHealthCheckOngoingRemediation.With(prometheus.Labels{
		"name":        name,
		"namespace":   namespace,
		"remediation": remediation,
	}).Set(1)
}

func ObserveNodeHealthCheckRemediationDeleted(name, namespace, remediation string) {
	nodeHealthCheckOngoingRemediation.With(prometheus.Labels{
		"name":        name,
		"namespace":   namespace,
		"remediation": remediation,
	}).Set(0)
}

func ObserveNodeHealthCheckUnhealthyNodeDuration(name, namespace, remediation string, duration time.Duration) {
	nodehealtCheckRemediationDuration.With(prometheus.Labels{
		"name":        name,
		"namespace":   namespace,
		"remediation": remediation,
	}).Observe(duration.Seconds())
}
