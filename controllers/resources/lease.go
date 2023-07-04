package resources

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/medik8s/common/pkg/lease"

	coordv1 "k8s.io/api/coordination/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

var (
	templateSuffix = "Template"
	holderIdentity = "Node-Healthcheck"
	//LeaseBuffer is used to make sure we have a bit of buffer before extending the lease, so it won't be taken by another component
	LeaseBuffer         = time.Minute
	RequeueIfLeaseTaken = time.Minute
	//DefaultLeaseDuration is the default time lease would be held before it would need extending assuming escalation timeout does not exist (i.e. without escalation)
	DefaultLeaseDuration = 10 * time.Minute
	//max times lease would be extended - this is a conceptual variable used to calculate max time lease can be held
	maxTimesToExtendLease = 2
)

type LeaseManager interface {
	// ObtainNodeLease will attempt to get a node lease with the correct duration, the duration is affected by whether escalation is used and the remediation timeOut.
	//The first return value (bool) is an indicator whether the lease was obtained, and the second return value (*time.Duration) is an indicator on when a new reconcile should be scheduled (mainly in order to extend the lease)
	ObtainNodeLease(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) (bool, *time.Duration, error)
	//ManageLease extends or releases a lease based on the CR status, type of remediation and how long the lease is already leased
	ManageLease(ctx context.Context, remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) (time.Duration, error)
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
	nodeName := remediationCR.GetName()
	leaseDuration := m.getTimeoutForRemediation(remediationCR, nhc)
	leaseDurationWithBuffer := leaseDuration + LeaseBuffer

	node := &v1.Node{}
	if err := m.client.Get(context.Background(), types.NamespacedName{Name: nodeName}, node); err != nil {
		m.log.Error(err, "couldn't obtain node lease node error getting node", "node name", nodeName)
		return false, nil, err
	}

	if err := m.commonLeaseManager.RequestLease(context.Background(), node, leaseDurationWithBuffer); err != nil {
		if _, ok := err.(lease.AlreadyHeldError); ok {
			m.log.Info("can't acquire node lease, it is already owned by another owner", "already held error", err)
			return false, &RequeueIfLeaseTaken, err
		}

		m.log.Error(err, "couldn't obtain lease for node", "node name", nodeName)
		return false, nil, err
	}

	//all good lease created with wanted duration
	return true, &leaseDuration, nil

}

func (m *nhcLeaseManager) getTimeoutForRemediation(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) time.Duration {
	leaseDuration := DefaultLeaseDuration
	if len(nhc.Spec.EscalatingRemediations) == 0 {
		return DefaultLeaseDuration
	}
	remediationKind := remediationCR.GetKind()
	for _, esRemediation := range nhc.Spec.EscalatingRemediations {
		if strings.TrimSuffix(esRemediation.RemediationTemplate.Kind, "Template") == remediationKind {
			leaseDuration = esRemediation.Timeout.Duration
			break
		}
	}
	return leaseDuration
}

func (m *nhcLeaseManager) getTimeoutForRemediations(ctx context.Context, node *v1.Node, nhc *remediationv1alpha1.NodeHealthCheck) (time.Duration, error) {
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
			if currentLeaseDuration := m.getTimeoutForRemediation(&remediationCr, nhc); currentLeaseDuration > highestRemediationLeaseDuration {
				highestRemediationLeaseDuration = currentLeaseDuration
			}
		}

	}
	return highestRemediationLeaseDuration, nil
}

func (m *nhcLeaseManager) ManageLease(ctx context.Context, remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) (time.Duration, error) {
	node := &v1.Node{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: remediationCR.GetName()}, node); err != nil {
		m.log.Error(err, "managing lease - couldn't fetch node", "node name", remediationCR.GetName())
		return 0, err
	}
	l, err := m.commonLeaseManager.GetLease(ctx, node)
	if err != nil {
		if errors.IsNotFound(err) {
			return 0, nil
		}
		m.log.Error(err, "managing lease - couldn't fetch lease", "node name", remediationCR.GetName())
		return 0, err
	}

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
		leaseExpectedDuration, err := m.getTimeoutForRemediations(ctx, node, nhc)
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

	leaseDuration, err := m.getTimeoutForRemediations(ctx, node, nhc)
	if err != nil {
		return false, err
	}
	isLeaseOverdue := time.Now().After(l.Spec.AcquireTime.Add(time.Duration(maxTimesToExtendLease+1 /*1 is offsetting the lease creation*/) * leaseDuration))
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
