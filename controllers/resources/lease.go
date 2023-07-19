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

type LeaseOverDueError struct {
	msg string
}

func (e LeaseOverDueError) Error() string {
	return e.msg
}

type LeaseManager interface {
	// ObtainNodeLease will attempt to get a node lease with the correct duration, the duration is affected by whether escalation is used and the remediation timeOut.
	//The first return value (*time.Duration) is an indicator on when a new reconcile should be scheduled (mainly in order to extend the lease)
	ObtainNodeLease(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) (*time.Duration, error)
	//ManageLease extends or releases a lease based on the CR status, type of remediation and how long the lease is already leased
	ManageLease(ctx context.Context, remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) (time.Duration, error)
	// InvalidateLease extends or releases a lease based on the CR status, type of remediation and how long the lease is already leased
	InvalidateLease(ctx context.Context, remediationCR *unstructured.Unstructured) error
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

func (m *nhcLeaseManager) ObtainNodeLease(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) (*time.Duration, error) {
	nodeName := remediationCR.GetName()
	leaseDuration := m.getLeaseDurationForRemediation(remediationCR, nhc)
	leaseDurationWithBuffer := leaseDuration + LeaseBuffer

	node := &v1.Node{}
	if err := m.client.Get(context.Background(), types.NamespacedName{Name: nodeName}, node); err != nil {
		m.log.Error(err, "couldn't obtain node lease node error getting node", "node name", nodeName)
		return nil, err
	}

	if err := m.commonLeaseManager.RequestLease(context.Background(), node, leaseDurationWithBuffer); err != nil {
		if _, ok := err.(lease.AlreadyHeldError); ok {
			m.log.Info("can't acquire node lease, it is already owned by another owner", "already held error", err)
			return &RequeueIfLeaseTaken, err
		}

		m.log.Error(err, "couldn't obtain lease for node", "node name", nodeName)
		return nil, err
	}

	//all good lease created with wanted duration
	return &leaseDuration, nil

}

func (m *nhcLeaseManager) ManageLease(ctx context.Context, remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) (time.Duration, error) {
	node := &v1.Node{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: remediationCR.GetName()}, node); err != nil {
		m.log.Error(err, "managing lease - couldn't fetch node", "node name", remediationCR.GetName())
		return 0, err
	}
	nodeLease, err := m.commonLeaseManager.GetLease(ctx, node)
	if err != nil {
		if errors.IsNotFound(err) {
			return 0, nil
		}
		m.log.Error(err, "managing lease - couldn't fetch lease", "node name", remediationCR.GetName())
		return 0, err
	}
	isDeleted := remediationCR.GetDeletionTimestamp() != nil
	if isDeleted && m.isLeaseOwner(nodeLease) {
		m.log.Info("managing lease - lease has no remediations so  about to be removed", "lease name", nodeLease.Name)
		//release the lease - no remediations
		return 0, m.commonLeaseManager.InvalidateLease(ctx, node)
	} else if ok, err := m.isLeaseOverdue(nodeLease, nhc, remediationCR); err != nil {
		return 0, err
	} else if ok { //release the lease - lease is overdue
		m.log.Info("managing lease - lease is overdue about to be removed", "lease name", nodeLease.Name)
		if err = m.commonLeaseManager.InvalidateLease(ctx, node); err != nil {
			m.log.Error(err, "failed to invalidate overdue lease", "node name", remediationCR.GetName())
			return 0, err
		}

		return 0, LeaseOverDueError{msg: fmt.Sprintf("failed to extend lease, it is overdue. node name: %s", remediationCR.GetName())}
	}

	leaseExpectedDuration := m.getLeaseDurationForRemediation(remediationCR, nhc)
	m.log.Info("managing lease - about to try to acquire/extended the lease", "lease name", nodeLease.Name, "lease is deleted", isDeleted, "NHC is lease owner", m.isLeaseOwner(nodeLease), "lease expiration time", m.calcLeaseExpiration(nodeLease, remediationCR, nhc))
	now := time.Now()
	expectedExpiry := now.Add(leaseExpectedDuration)
	actualExpiry := nodeLease.Spec.RenewTime.Add(time.Second * time.Duration(int(*nodeLease.Spec.LeaseDurationSeconds)))
	if actualExpiry.Before(expectedExpiry) {
		err := m.commonLeaseManager.RequestLease(ctx, node, leaseExpectedDuration+LeaseBuffer)
		if err != nil {
			m.log.Error(err, "couldn't renew lease", "lease name", nodeLease.Name)
			return 0, err
		}
	}
	return leaseExpectedDuration, nil
}

func (m *nhcLeaseManager) InvalidateLease(ctx context.Context, remediationCR *unstructured.Unstructured) error {
	node := &v1.Node{}
	if err := m.client.Get(ctx, client.ObjectKey{Name: remediationCR.GetName()}, node); err != nil {
		m.log.Error(err, "failed to get node", "node name", remediationCR.GetName())
		return err
	}

	err := m.commonLeaseManager.InvalidateLease(ctx, node)
	if err != nil {
		m.log.Error(err, "failed to invalidate lease", "node name", remediationCR.GetName())
		return err
	}
	return nil
}

func (m *nhcLeaseManager) getLeaseDurationForRemediation(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) time.Duration {
	if timeout := m.getTimeoutForRemediation(remediationCR, nhc); timeout != 0 {
		return timeout
	}
	return DefaultLeaseDuration
}

func (m *nhcLeaseManager) getTimeoutForRemediation(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) time.Duration {
	var leaseDuration time.Duration
	remediationKind := remediationCR.GetKind()
	for _, esRemediation := range nhc.Spec.EscalatingRemediations {
		if strings.TrimSuffix(esRemediation.RemediationTemplate.Kind, "Template") == remediationKind {
			leaseDuration = esRemediation.Timeout.Duration
			break
		}
	}
	return leaseDuration
}

func (m *nhcLeaseManager) isLeaseOverdue(l *coordv1.Lease, nhc *remediationv1alpha1.NodeHealthCheck, remediationCR *unstructured.Unstructured) (bool, error) {
	if l.Spec.AcquireTime == nil {
		err := fmt.Errorf("lease Spec.AcquireTime is nil")
		m.log.Error(err, "lease Spec.AcquireTime is nil", "lease name", l.Name)
		return false, err
	}

	isLeaseOverdue := time.Now().After(m.calcLeaseExpiration(l, remediationCR, nhc))
	return isLeaseOverdue, nil
}

func (m *nhcLeaseManager) calcLeaseExpiration(l *coordv1.Lease, remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) time.Time {
	leaseDuration := m.getLeaseDurationForRemediation(remediationCR, nhc)
	prevRemediationsDuration := m.sumPrevRemediationsDuration(remediationCR, nhc)
	return l.Spec.AcquireTime.Add(time.Duration(maxTimesToExtendLease+1 /*1 is offsetting the lease creation*/)*leaseDuration + prevRemediationsDuration)
}

func (m *nhcLeaseManager) isRemediationsExist(remediationCrs []unstructured.Unstructured) bool {
	return len(remediationCrs) > 0
}

func (m *nhcLeaseManager) isLeaseOwner(l *coordv1.Lease) bool {
	if l.Spec.HolderIdentity == nil {
		return false
	}
	return *l.Spec.HolderIdentity == holderIdentity
}

func (m *nhcLeaseManager) sumPrevRemediationsDuration(remediationCR *unstructured.Unstructured, nhc *remediationv1alpha1.NodeHealthCheck) time.Duration {
	if len(nhc.Spec.EscalatingRemediations) == 0 {
		return 0
	}

	var currentEscalatingRemediation *remediationv1alpha1.EscalatingRemediation
	remediationKind := remediationCR.GetKind()
	for _, esRemediation := range nhc.Spec.EscalatingRemediations {
		if strings.TrimSuffix(esRemediation.RemediationTemplate.Kind, "Template") == remediationKind {
			currentEscalatingRemediation = &esRemediation
			break
		}
	}
	//remediation isn't found in escalation - should not happen
	if currentEscalatingRemediation == nil {
		msg := fmt.Sprintf("existing remediation of kind:%s not found in escalation list: %v", remediationCR.GetKind(), nhc.Spec.EscalatingRemediations)
		m.log.Error(fmt.Errorf("remediation not found in escalation"), msg)
		return 0
	}

	var sumDurations time.Duration
	for _, esRemediation := range nhc.Spec.EscalatingRemediations {
		if currentEscalatingRemediation.Order > esRemediation.Order {
			sumDurations += esRemediation.Timeout.Duration
		}
	}

	return sumDurations
}
