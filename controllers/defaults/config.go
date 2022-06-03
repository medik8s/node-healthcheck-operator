package defaults

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

const (
	// DefaultCRName is the name of default NHC resource the controller creates in case
	// there are no others already.
	DefaultCRName = "nhc-worker-default"

	deprecatedTemplateName = "poison-pill-default-template"
)

var defaultTemplateRef = &corev1.ObjectReference{
	Kind:       "SelfNodeRemediationTemplate",
	APIVersion: "self-node-remediation.medik8s.io/v1alpha1",
	Name:       "self-node-remediation-resource-deletion-template",
}

// CreateOrUpdateDefaultNHC creates the default config
func CreateOrUpdateDefaultNHC(mgr ctrl.Manager, namespace string, log logr.Logger) error {

	defaultTemplateRef.Namespace = namespace

	list := remediationv1alpha1.NodeHealthCheckList{}

	var err error
	stop := make(chan struct{})
	wait.Until(func() {
		err = mgr.GetAPIReader().List(
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

	if len(list.Items) > 0 {
		// If we have already some NHC then this is a restart, an upgrade, or a redeploy
		// For upgrades we need to check if we still have the old default config
		// with the deprecated Poison Pill remediator deployed, and update it to the new Self Node Remediation.
		log.Info("there are existing NHC resources, checking if we need to update the default config", "numOfNHC", len(list.Items))

		for i := range list.Items {
			if list.Items[i].Spec.RemediationTemplate.Name == deprecatedTemplateName {
				log.Info("updating config from old Poison Pill to new Self Node Remediation", "NHC name", list.Items[i].Name)
				list.Items[i].Spec.RemediationTemplate = defaultTemplateRef
				if err := mgr.GetClient().Update(context.Background(), &list.Items[i]); err != nil {
					return errors.Wrap(err, "failed to update default NHC")
				}
				break
			}
		}

		return nil
	}

	nhc := remediationv1alpha1.NodeHealthCheck{
		ObjectMeta: metav1.ObjectMeta{
			Name: DefaultCRName,
		},
		Spec: remediationv1alpha1.NodeHealthCheckSpec{
			Selector: metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "node-role.kubernetes.io/worker",
					Operator: metav1.LabelSelectorOpExists,
				}},
			},
			RemediationTemplate: defaultTemplateRef,
		},
	}

	err = mgr.GetClient().Create(context.Background(), &nhc, &client.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return errors.Wrap(err, "failed to create a default node-healthcheck resource")
	}
	log.Info("created a default NHC resource", "name", DefaultCRName)
	return nil
}
