package controllers

import (
	"context"

	"github.com/go-logr/logr"
	commonLabels "github.com/medik8s/common/pkg/labels"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/medik8s/node-healthcheck-operator/controllers/resources"
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

	return labelsNeedReconcile(oldNode.Labels, newNode.Labels) ||
		conditionsNeedReconcile(oldNode.Status.Conditions, newNode.Status.Conditions) ||
		annotationsNeedReconcile(oldNode.Annotations, newNode.Annotations)
}

func labelsNeedReconcile(oldLabels, newLabels map[string]string) bool {
	// Check if the ExcludeFromRemediation label was added or removed
	_, existsInOldLabels := oldLabels[commonLabels.ExcludeFromRemediation]
	_, existsInNewLabels := newLabels[commonLabels.ExcludeFromRemediation]

	return existsInOldLabels != existsInNewLabels
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

func annotationsNeedReconcile(oldAnnotations, newAnnotations map[string]string) bool {
	// Check if the RemediationManuallyConfirmedHealthy annotation was added or removed
	_, existsInOldAnnotations := oldAnnotations[resources.RemediationManuallyConfirmedHealthyAnnotationKey]
	_, existsInNewAnnotations := newAnnotations[resources.RemediationManuallyConfirmedHealthyAnnotationKey]

	return existsInOldAnnotations != existsInNewAnnotations
}

type ObjectWithStatus interface {
	GetStatus() interface{}
}

func patchStatus(ctx context.Context, cl client.Client, log logr.Logger, actual, orig client.Object) error {
	mergeFrom := client.MergeFrom(orig)

	// check if there are any changes.
	// reflect.DeepEqual does not work, it has many false positives!
	if patchBytes, err := mergeFrom.Data(actual); err != nil {
		log.Error(err, "failed to create patch")
		return err
	} else if string(patchBytes) == "{}" {
		// no change
		return nil
	} else {
		status, err := getStatus(actual)
		if err != nil {
			return err
		}
		log.Info("Patching status", "new status", status, "patch", string(patchBytes))
	}

	return cl.Status().Patch(ctx, actual, mergeFrom)
}

func getStatus(obj client.Object) (map[string]interface{}, error) {
	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}
	status, found, err := unstructured.NestedMap(u, "status")
	if err != nil || !found {
		return nil, err
	}
	return status, nil
}
