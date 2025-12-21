package utils

import (
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/api/machine/v1beta1"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

// generic unhealthy condition type for sharing code for NHC and MHC unhealthy conditions
type unhealthyCondition struct {
	Type     corev1.NodeConditionType
	Status   corev1.ConditionStatus
	Duration metav1.Duration
}

func unhealthyConditionsFromNHC(unhealthyConditions []remediationv1alpha1.UnhealthyCondition) (conditions []unhealthyCondition) {
	for _, c := range unhealthyConditions {
		conditions = append(conditions, unhealthyCondition{
			Type:     c.Type,
			Status:   c.Status,
			Duration: c.Duration,
		})
	}
	return conditions
}

func unhealthyConditionsFromMHC(unhealthyConditions []v1beta1.UnhealthyCondition) (conditions []unhealthyCondition) {
	for _, c := range unhealthyConditions {
		conditions = append(conditions, unhealthyCondition{
			Type:     c.Type,
			Status:   c.Status,
			Duration: c.Timeout,
		})
	}
	return conditions
}

func IsHealthyNHC(unhealthyConditions []remediationv1alpha1.UnhealthyCondition, nodeConditions []corev1.NodeCondition, now time.Time) (bool, *time.Duration) {
	return isHealthy(unhealthyConditionsFromNHC(unhealthyConditions), nodeConditions, now)
}

func IsHealthyMHC(unhealthyConditions []v1beta1.UnhealthyCondition, nodeConditions []corev1.NodeCondition, now time.Time) (bool, *time.Duration) {
	return isHealthy(unhealthyConditionsFromMHC(unhealthyConditions), nodeConditions, now)
}

func isHealthy(unhealthyConditions []unhealthyCondition, nodeConditions []corev1.NodeCondition, now time.Time) (bool, *time.Duration) {
	nodeConditionByType := make(map[corev1.NodeConditionType]corev1.NodeCondition)
	for _, nc := range nodeConditions {
		nodeConditionByType[nc.Type] = nc
	}

	for _, c := range unhealthyConditions {
		n, exists := nodeConditionByType[c.Type]
		if !exists {
			continue
		}
		if n.Status == c.Status {
			if now.After(n.LastTransitionTime.Add(c.Duration.Duration)) {
				// unhealthy condition duration expired, node is unhealthy
				return false, nil
			} else {
				// unhealthy condition duration not expired yet, node is healthy. Requeue when duration expires
				expiresAfter := n.LastTransitionTime.Add(c.Duration.Duration).Sub(now) + 1*time.Second
				return true, &expiresAfter
			}
		}
	}
	return true, nil
}

// IsConditionTrue return true when the conditions contain a condition of given type and reason with status true
func IsConditionTrue(conditions []metav1.Condition, conditionType string, reason string) bool {
	if !IsConditionSet(conditions, conditionType, reason) {
		return false
	}
	condition := meta.FindStatusCondition(conditions, conditionType)
	return condition.Status == metav1.ConditionTrue
}

// IsConditionSet return true when the conditions contain a condition of given type and reason
func IsConditionSet(conditions []metav1.Condition, conditionType string, reason string) bool {
	condition := meta.FindStatusCondition(conditions, conditionType)
	if condition == nil {
		return false
	}
	if condition.Reason != reason {
		return false
	}
	return true
}

func SetMachineCondition(mhc *v1beta1.MachineHealthCheck, condition *v1beta1.Condition) {
	// Check if the new conditions already exists, and change it only if there is a status
	// transition (otherwise we should preserve the current last transition time)-
	conditions := mhc.Status.Conditions
	exists := false
	for i := range conditions {
		existingCondition := conditions[i]
		if existingCondition.Type == condition.Type {
			exists = true
			if !hasSameState(&existingCondition, condition) {
				condition.LastTransitionTime = metav1.NewTime(time.Now().UTC().Truncate(time.Second))
				conditions[i] = *condition
				break
			}
			condition.LastTransitionTime = existingCondition.LastTransitionTime
			break
		}
	}

	// If the condition does not exist, add it, setting the transition time only if not already set
	if !exists {
		if condition.LastTransitionTime.IsZero() {
			condition.LastTransitionTime = metav1.NewTime(time.Now().UTC().Truncate(time.Second))
		}
		conditions = append(conditions, *condition)
	}

	// Sorts conditions for convenience of the consumer, i.e. kubectl.
	sort.Slice(conditions, func(i, j int) bool {
		return lexicographicLess(&conditions[i], &conditions[j])
	})

	mhc.Status.Conditions = conditions
}

// hasSameState returns true if a machine condition has the same state of another; state is defined
// by the union of following fields: Type, Status, Reason, Severity and Message (it excludes LastTransitionTime).
func hasSameState(i, j *v1beta1.Condition) bool {
	return i.Type == j.Type &&
		i.Status == j.Status &&
		i.Reason == j.Reason &&
		i.Severity == j.Severity &&
		i.Message == j.Message
}

// lexicographicLess returns true if a condition is less than another with regards to the
// to order of conditions designed for convenience of the consumer, i.e. kubectl.
func lexicographicLess(i, j *v1beta1.Condition) bool {
	return i.Type < j.Type
}
