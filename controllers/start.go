package controllers

import (
	"context"
	"fmt"
	"os"
	"time"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// DefaultCRName is the name of default NHC resource the controller creates in case
// there are no others already.
const DefaultCRName = "nhc-worker-default"

// DefaultPoisonPillTemplateName is the name of the poison-pill template the default
// NHC uses.
const DefaultPoisonPillTemplateName = "poison-pill-default-template"

// NewNodeHealthcheckController setups the Node Health Check controller
// with the controller manager and ensures at least one NHC resource exists.
// If there is no NHC resource it will create one with the name in #DefaultCRName
// that works with poison-pill template by the name in #DefaultPoisonPillTemplateName
// on the same namespace this controller is deployed.
func NewNodeHealthcheckController(mgr manager.Manager) error {
	upgradeChecker, err := newClusterUpgradeStatusChecker(mgr)
	if err != nil {
		return errors.Wrap(err, "unable to determine a cluster upgrade status upgradeChecker")
	}
	if err := (&NodeHealthCheckReconciler{
		Client: mgr.GetClient(),
		// the dynamic client is here only because the fake client from client-go
		// couldn't List correctly unstructuredList. The fake dynamic client works.
		// See https://github.com/kubernetes-sigs/controller-runtime/issues/702
		DynamicClient:               dynamic.NewForConfigOrDie(mgr.GetConfig()),
		Log:                         ctrl.Log.WithName("controllers").WithName("NodeHealthCheck"),
		Scheme:                      mgr.GetScheme(),
		clusterUpgradeStatusChecker: upgradeChecker,
	}).SetupWithManager(mgr); err != nil {
		return errors.Wrap(err, "unable to create controller")
	}
	ns, err := getDeploymentNamespace()
	if err != nil {
		return errors.Wrap(err, "unable to get the deployment namespace")
	}
	if err = createDefaultNHC(mgr, ns); err != nil {
		return errors.Wrap(err, "failed to create a default NHC resource")
	}
	return nil
}

// getDeploymentNamespace returns the Namespace this operator is deployed on.
func getDeploymentNamespace() (string, error) {
	// deployNamespaceEnvVar is the constant for env variable DEPLOYMENT_NAMESPACE
	// which specifies the Namespace to watch.
	// An empty value means the operator is running with cluster scope.
	var deployNamespaceEnvVar = "DEPLOYMENT_NAMESPACE"

	ns, found := os.LookupEnv(deployNamespaceEnvVar)
	if !found {
		return "", fmt.Errorf("%s must be set", deployNamespaceEnvVar)
	}
	return ns, nil
}

func createDefaultNHC(mgr ctrl.Manager, namespace string) error {
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
			RemediationTemplate: &v1.ObjectReference{
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
