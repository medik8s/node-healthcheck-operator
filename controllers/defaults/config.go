package defaults

import (
	"context"
	"time"

	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

// DefaultCRName is the name of default NHC resource the controller creates in case
// there are no others already.
const DefaultCRName = "nhc-worker-default"

// DefaultPoisonPillTemplateName is the name of the poison-pill template the default
// NHC uses.
const DefaultPoisonPillTemplateName = "poison-pill-default-template"

// CreateDefaultNHC creates the default config
func CreateDefaultNHC(mgr ctrl.Manager, namespace string) error {
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
		// if we have already some NHC then this is a restart, an upgrade, or a redeploy
		// then we preserve whatever we have.
		ctrl.Log.Info("there are existing NHC resources, skip creating a default one", "numOfNHC", len(list.Items))
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
			RemediationTemplate: &corev1.ObjectReference{
				Kind:       "PoisonPillRemediationTemplate",
				APIVersion: "poison-pill.medik8s.io/v1alpha1",
				Name:       DefaultPoisonPillTemplateName,
				Namespace:  namespace,
			},
		},
	}

	err = mgr.GetClient().Create(context.Background(), &nhc, &client.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return errors.Wrap(err, "failed to create a default node-healthcheck resource")
	}
	ctrl.Log.Info("created a default NHC resource", "name", DefaultCRName)
	return nil
}
