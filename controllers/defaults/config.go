package defaults

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
)

const (
	// DefaultCRName is the name of default NHC resource the controller creates in case
	// there are no others already.
	DefaultCRName = "nhc-worker-default"

	deprecatedTemplateName = "poison-pill-default-template"
)

var DefaultTemplateRef = &corev1.ObjectReference{
	Kind:       "SelfNodeRemediationTemplate",
	APIVersion: "self-node-remediation.medik8s.io/v1alpha1",
	Name:       "self-node-remediation-resource-deletion-template",
}

var DefaultSelector = metav1.LabelSelector{
	MatchExpressions: []metav1.LabelSelectorRequirement{
		{
			Key:      utils.ControlPlaneRoleLabel,
			Operator: metav1.LabelSelectorOpDoesNotExist,
		},
		{
			Key:      utils.MasterRoleLabel,
			Operator: metav1.LabelSelectorOpDoesNotExist,
		},
	},
}

// UpdateDefaultNHC updates the default config. We do not create a default config anymore though
func UpdateDefaultNHC(cl client.Client, namespace string, log logr.Logger) error {

	DefaultTemplateRef.Namespace = namespace

	list := remediationv1alpha1.NodeHealthCheckList{}

	var err error
	stop := make(chan struct{})
	wait.Until(func() {
		err = cl.List(
			context.Background(),
			&list,
			&client.ListOptions{},
		)
		if err == nil {
			close(stop)
		}
	}, time.Minute*1, stop)
	if err != nil {
		return errors.Wrap(err, "failed to list NHC objects")
	}

	if len(list.Items) == 0 {
		return nil
	}

	// If we have already some NHC then this is a restart, an upgrade, or a redeploy
	// For upgrades we need to check if we have outdated default configs
	log.Info("there are existing NHC resources, checking if we need to update the default config", "numOfNHC", len(list.Items))

	for _, nhc := range list.Items {
		nhc := nhc
		updated := false

		// We need to check if we still have a default config with the deprecated Poison Pill remediator,
		// and update it to the new Self Node Remediation.
		if nhc.Spec.RemediationTemplate != nil && nhc.Spec.RemediationTemplate.Name == deprecatedTemplateName {
			log.Info("updating config from old Poison Pill to new Self Node Remediation", "NHC name", nhc.Name)
			nhc.Spec.RemediationTemplate = DefaultTemplateRef
			updated = true
		}

		// Update node selector from worker to !control-plane AND !master, in order to prevent unwanted remediation of control
		// plane nodes in case they also are workers
		if nhc.Name == DefaultCRName && nhc.Spec.Selector.MatchExpressions[0].Key == utils.WorkerRoleLabel {
			log.Info("updating default config from selecting worker role to selecting !control-plane && !master role", "NHC name", nhc.Name)
			nhc.Spec.Selector = DefaultSelector
			updated = true
		}

		if updated {
			if err := cl.Update(context.Background(), &nhc); err != nil {
				return errors.Wrap(err, "failed to update default NHC")
			}
		}
	}

	return nil
}
