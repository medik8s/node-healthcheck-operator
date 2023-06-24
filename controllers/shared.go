package controllers

import (
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func nodeUpdateNeedsReconcile(ev event.UpdateEvent) bool {
	var oldNode *v1.Node
	var newNode *v1.Node
	var ok bool
	if oldNode, ok = ev.ObjectOld.(*v1.Node); !ok {
		return false
	}
	if newNode, ok = ev.ObjectNew.(*v1.Node); !ok {
		return false
	}
	return conditionsNeedReconcile(oldNode.Status.Conditions, newNode.Status.Conditions)
}

func conditionsNeedReconcile(oldConditions, newConditions []v1.NodeCondition) bool {
	// Check if the Ready condition exists on the new node.
	// If not, the node was just created and hasn't updated its status yet
	readyConditionFound := false
	for _, cond := range newConditions {
		if cond.Type == v1.NodeReady {
			readyConditionFound = true
			break
		}
	}
	if !readyConditionFound {
		return false
	}

	// Check if conditions changed
	if len(oldConditions) != len(newConditions) {
		return true
	}
	for _, condOld := range oldConditions {
		conditionFound := false
		for _, condNew := range newConditions {
			if condOld.Type == condNew.Type {
				if condOld.Status != condNew.Status {
					return true
				}
				conditionFound = true
			}
		}
		if !conditionFound {
			return true
		}
	}
	return false
}
