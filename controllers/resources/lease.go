package resources

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/medik8s/common/pkg/lease"
	pkgerrors "github.com/pkg/errors"

	coordv1 "k8s.io/api/coordination/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

var (
	templateSuffix = "Template"
	holderIdentity = "NHC"
	//LeaseBuffer is used to make sure we have a bit of buffer before extending the lease so it won't be taken by another component
	LeaseBuffer          = time.Minute
	RequeueIfLeaseTaken  = time.Minute
	DefaultLeaseDuration = 10 * time.Minute
	//multiple of the lease duration
	maxDurationFactorToHoldLease = 3
)

type LeaseManager interface {
	// ObtainNodeLease will attempt to get a node lease with the correct duration, the duration is affected by whether escalation is used and the remediation timeOut.
	//The first return value (bool) is an indicator whether the lease was obtained, and the second return value (*time.Duration) is an indicator on when a new reconcile should be scheduled (mainly in order to extend the lease)
	ObtainNodeLease(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) (bool, *time.Duration, error)
	//UpdateReconcileResults extends leases which needs to be extended and release those that needs to be releases, it is called at the end of reconcile and returns an updated  timeout and error to be returned by reconcile method (which are used for the next iteration of lease management)
	UpdateReconcileResults(ctx context.Context, nhc *remediationv1alpha1.NodeHealthCheck, initialRequeue time.Duration, initialErr error) (time.Duration, error)
}

type nhcLeaseManager struct {
	client             client.Client
	commonLeaseManager lease.Manager
	log                logr.Logger
}

func NewLeaseManager(client client.Client, log logr.Logger) (LeaseManager, error) {
	newManager, err := lease.NewManager(client, holderIdentity)
	if err != nil {
		log.Error(err, "couldn't initialize lease manager")
		return nil, err
	}
	return &nhcLeaseManager{
		client:             client,
		commonLeaseManager: newManager,
		log:                log.WithName("nhc lease manager"),
	}, nil
}

func (m *nhcLeaseManager) ObtainNodeLease(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) (bool, *time.Duration, error) {
	return m.obtainNodeLease(remediationCR, nhc)
}

func (m *nhcLeaseManager) UpdateReconcileResults(ctx context.Context, nhc *remediationv1alpha1.NodeHealthCheck, initialRequeue time.Duration, initialErr error) (time.Duration, error) {
	return m.updateReconcileResults(ctx, nhc, initialRequeue, initialErr)
}

func (m *nhcLeaseManager) obtainNodeLease(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) (bool, *time.Duration, error) {
	nodeName := remediationCR.GetName()
	leaseDuration := m.calculateLeaseDurationBySingleRemediation(remediationCR, nhc)
	leaseDurationWithBuffer := leaseDuration + LeaseBuffer

	node := &v1.Node{}
	if err := m.client.Get(context.Background(), types.NamespacedName{Name: nodeName}, node); err != nil {
		m.log.Error(err, "couldn't obtain node lease node error getting node", "node name", nodeName)
		return false, nil, err
	}

	if err := m.commonLeaseManager.RequestLease(context.Background(), node, leaseDurationWithBuffer); err != nil {
		l, _ := m.commonLeaseManager.GetLease(context.Background(), node)
		if l != nil && l.Spec.HolderIdentity != nil && *l.Spec.HolderIdentity != holderIdentity {
			m.log.Info("can't acquire node lease, it is already owned by another owner", "current owner", *l.Spec.HolderIdentity)
			return false, &RequeueIfLeaseTaken, nil
		}

		m.log.Error(err, "couldn't obtain lease for node", "node name", nodeName)
		return false, nil, err
	}

	//all good lease created with wanted duration
	return true, &leaseDuration, nil

}

func (m *nhcLeaseManager) calculateLeaseDurationBySingleRemediation(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) time.Duration {
	leaseDuration := DefaultLeaseDuration
	if len(nhc.Spec.EscalatingRemediations) == 0 {
		return DefaultLeaseDuration
	} else {
		remediationKind := remediationCR.GetKind()
		for _, esRemediation := range nhc.Spec.EscalatingRemediations {
			if strings.TrimSuffix(esRemediation.RemediationTemplate.Kind, "Template") == remediationKind {
				leaseDuration = esRemediation.Timeout.Duration
				break
			}
		}
	}
	return leaseDuration
}

func (m *nhcLeaseManager) calculateLeaseDurationByAllRemediations(ctx context.Context, node *v1.Node, nhc *remediationv1alpha1.NodeHealthCheck) (time.Duration, error) {
	highestRemediationLeaseDuration := DefaultLeaseDuration
	if len(nhc.Spec.EscalatingRemediations) > 0 {

		remediationCrs, err := listRemediationCRs(ctx, m.client, nhc, func(cr unstructured.Unstructured) bool {
			return cr.GetName() == node.GetName()
		})
		if err != nil {
			m.log.Error(err, "couldn't fetch remediation CRs in order to calculate required lease duration", "node name", node.Name)
			return 0, err
		}

		highestRemediationLeaseDuration = 0
		for _, remediationCr := range remediationCrs {
			if currentLeaseDuration := m.calculateLeaseDurationBySingleRemediation(&remediationCr, nhc); currentLeaseDuration > highestRemediationLeaseDuration {
				highestRemediationLeaseDuration = currentLeaseDuration
			}
		}

	}
	return highestRemediationLeaseDuration, nil
}

// needs to release leases that needs to be released and extend those that needs extending return minimum timeout for next check
func (m *nhcLeaseManager) manageLeases(ctx context.Context, nhc *remediationv1alpha1.NodeHealthCheck) (time.Duration, error) {
	nodes := &v1.NodeList{}
	if err := m.client.List(ctx, nodes); err != nil {
		m.log.Error(err, "couldn't fetch nodes in order to manage leases")
		return 0, err
	}
	var minRequeue time.Duration
	var allErrors []error
	for _, node := range nodes.Items {
		nodeLease, err := m.commonLeaseManager.GetLease(ctx, &node)
		if err != nil && apierrors.IsNotFound(err) {
			continue
		} else if err != nil {
			allErrors = append(allErrors, err)
		} else {
			requeue, err := m.manageLease(ctx, nodeLease, &node, nhc)
			if err != nil {
				allErrors = append(allErrors, err)
			} else if minRequeue == 0 { //update minRequeue if not initialized or new requeue is lower
				minRequeue = requeue
			} else if minRequeue > requeue && requeue > 0 {
				minRequeue = requeue
			}

		}
	}

	var resultError error
	if len(allErrors) != 0 {
		resultError = errors.Join(allErrors...)
	}

	return minRequeue, resultError
}

func (m *nhcLeaseManager) manageLease(ctx context.Context, l *coordv1.Lease, node *v1.Node, nhc *remediationv1alpha1.NodeHealthCheck) (time.Duration, error) {
	if ok, err := m.isLeaseOverdue(ctx, node, l, nhc); err != nil {
		return 0, err
	} else if ok { //release the lease - lease is overdue
		m.log.Info("managing lease - lease is overdue about to be removed", "lease name", l.Name)
		return 0, m.commonLeaseManager.InvalidateLease(ctx, node)
	} else if exist, err := m.isRemediationsExist(ctx, node, nhc); err != nil {
		return 0, err
	} else if !exist && m.isLeaseOwner(l) {
		m.log.Info("managing lease - lease has no remediations so  about to be removed", "lease name", l.Name)
		//release the lease - no remediations
		return 0, m.commonLeaseManager.InvalidateLease(ctx, node)
	} else {
		m.log.Info("managing lease - about to try to acquire/extended the lease", "lease name", l.Name, "lease has remediations", exist, "NHC is lease owner", m.isLeaseOwner(l))
		leaseExpectedDuration, err := m.calculateLeaseDurationByAllRemediations(ctx, node, nhc)
		if err != nil {
			return 0, err
		}
		now := time.Now()
		expectedExpiry := now.Add(leaseExpectedDuration)
		actualExpiry := l.Spec.RenewTime.Add(time.Second * time.Duration(int(*l.Spec.LeaseDurationSeconds)))
		if actualExpiry.Before(expectedExpiry) {
			err := m.commonLeaseManager.RequestLease(ctx, node, leaseExpectedDuration+LeaseBuffer)
			if err != nil {
				m.log.Error(err, "couldn't renew lease", "lease name", l.Name)
				return 0, err
			}
		}
		return leaseExpectedDuration, nil
	}
}

func (m *nhcLeaseManager) isLeaseOverdue(ctx context.Context, node *v1.Node, l *coordv1.Lease, nhc *remediationv1alpha1.NodeHealthCheck) (bool, error) {
	if l.Spec.AcquireTime == nil {
		err := fmt.Errorf("lease Spec.AcquireTime is nil")
		m.log.Error(err, "lease Spec.AcquireTime is nil", "lease name", l.Name)
		return false, err
	}

	leaseDuration, err := m.calculateLeaseDurationByAllRemediations(ctx, node, nhc)
	if err != nil {
		return false, err
	}
	isLeaseOverdue := time.Now().After(l.Spec.AcquireTime.Add(time.Duration(maxDurationFactorToHoldLease) * leaseDuration))
	return isLeaseOverdue, nil
}

func (m *nhcLeaseManager) isRemediationsExist(ctx context.Context, node *v1.Node, nhc *remediationv1alpha1.NodeHealthCheck) (bool, error) {
	remediationCrs, err := listRemediationCRs(ctx, m.client, nhc, func(cr unstructured.Unstructured) bool {
		return cr.GetName() == node.GetName()
	})

	if err != nil {
		m.log.Error(err, "couldn't fetch remediations for node", "node name", node.GetName())
		return false, err
	}

	return len(remediationCrs) > 0, nil
}

func (m *nhcLeaseManager) updateReconcileResults(ctx context.Context, nhc *remediationv1alpha1.NodeHealthCheck, initialRequeue time.Duration, initialErr error) (time.Duration, error) {
	originalRequeue, originalErr := initialRequeue, initialErr
	updatedRequeue, updatedErr := initialRequeue, initialErr
	if requeue, err := m.manageLeases(ctx, nhc); err != nil {
		if initialErr == nil {
			updatedErr = err
		} else {
			updatedErr = pkgerrors.Wrap(initialErr, err.Error())
		}
		//requeue returned from lease manager is lower than the original request requeue
	} else if requeue > 0 && (initialRequeue == 0 || requeue < initialRequeue) {
		updatedRequeue = requeue
	}
	//Log
	m.logManageLeaseChanges(originalRequeue, updatedRequeue, originalErr, updatedErr)
	return updatedRequeue, updatedErr
}

func (m *nhcLeaseManager) logManageLeaseChanges(originalRequeue time.Duration, updatedRequeue time.Duration, originalErr error, updatedErr error) {
	if originalRequeue != updatedRequeue && originalErr != updatedErr {
		m.log.Info("updated requeue and err values",
			"original requeue value", originalRequeue, "updated requeue value", updatedRequeue,
			"original error value", originalErr, "updated error value", updatedErr)
	} else if originalRequeue != updatedRequeue {
		m.log.Info("updated requeue value",
			"original requeue value", originalRequeue, "updated requeue value", updatedRequeue)
	} else if originalErr != updatedErr {
		m.log.Info("updated error value",
			"original error value", originalErr, "updated error value", updatedErr)
	} else if updatedErr == nil && updatedRequeue == 0 { //no changes were made and reconcile was successful
		m.log.Info("successful reconcile, no changes made")
	}
}

func (m *nhcLeaseManager) isLeaseOwner(l *coordv1.Lease) bool {
	if l.Spec.HolderIdentity == nil {
		return false
	}
	return *l.Spec.HolderIdentity == holderIdentity
}
