package resources

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/medik8s/common/pkg/lease"

	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
)

var (
	templateSuffix = "Template"
	//LeaseBuffer is used to make sure we have a bit of buffer before extending the lease, so it won't be taken by another component
	LeaseBuffer         = time.Minute
	RequeueIfLeaseTaken = time.Minute
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
	ObtainNodeLease(remediationCR *unstructured.Unstructured, currentRemediationDuration time.Duration) (*time.Duration, error)
	//ManageLease extends or releases a lease based on the CR status, type of remediation and how long the lease is already leased
	ManageLease(ctx context.Context, remediationCR *unstructured.Unstructured, currentRemediationDuration, previousRemediationsDuration time.Duration) (time.Duration, error)
	// InvalidateLease invalidates the lease for the node with the given name
	InvalidateLease(ctx context.Context, nodeName string) error
}

type nhcLeaseManager struct {
	client             client.Client
	commonLeaseManager lease.Manager
	holderIdentity     string
	log                logr.Logger
}

func NewLeaseManager(client client.Client, holderIdent string, log logr.Logger) (LeaseManager, error) {
	newManager, err := lease.NewManager(client, holderIdent)
	if err != nil {
		log.Error(err, "couldn't initialize lease manager")
		return nil, err
	}
	return &nhcLeaseManager{
		client:             client,
		commonLeaseManager: newManager,
		holderIdentity:     holderIdent,
		log:                log.WithName("nhc lease manager"),
	}, nil
}

func (m *nhcLeaseManager) ObtainNodeLease(remediationCR *unstructured.Unstructured, currentRemediationDuration time.Duration) (*time.Duration, error) {
	nodeName := remediationCR.GetName()
	leaseDurationWithBuffer := currentRemediationDuration + LeaseBuffer

	node := &corev1.Node{}
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
	return &currentRemediationDuration, nil

}

func (m *nhcLeaseManager) ManageLease(ctx context.Context, remediationCR *unstructured.Unstructured, currentRemediationDuration, previousRemediationsDuration time.Duration) (time.Duration, error) {
	node := &corev1.Node{}
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
	//nothing to do with this lease
	if !m.isLeaseOwner(nodeLease) {
		return 0, nil
	}
	if isLeaseOverdue, err := m.isLeaseOverdue(nodeLease, currentRemediationDuration, previousRemediationsDuration, remediationCR); err != nil {
		return 0, err
	} else if isLeaseOverdue { //release the lease - lease is overdue
		m.log.Info("managing lease - lease is overdue about to be removed", "lease name", nodeLease.Name)
		if err = m.commonLeaseManager.InvalidateLease(ctx, node); err != nil {
			m.log.Error(err, "failed to invalidate overdue lease", "node name", remediationCR.GetName())
			return 0, err
		}

		return 0, LeaseOverDueError{msg: fmt.Sprintf("failed to extend lease, it is overdue. node name: %s", remediationCR.GetName())}
	}

	m.log.Info("managing lease - about to try to acquire/extended the lease", "lease name", nodeLease.Name, "NHC is lease owner", m.isLeaseOwner(nodeLease), "lease expiration time", currentRemediationDuration)
	now := time.Now()
	expectedExpiry := now.Add(currentRemediationDuration)
	actualExpiry := nodeLease.Spec.RenewTime.Add(time.Second * time.Duration(int(*nodeLease.Spec.LeaseDurationSeconds)))
	if actualExpiry.Before(expectedExpiry) {
		err := m.commonLeaseManager.RequestLease(ctx, node, currentRemediationDuration+LeaseBuffer)
		if err != nil {
			m.log.Error(err, "couldn't renew lease", "lease name", nodeLease.Name)
			return 0, err
		}
	}
	return currentRemediationDuration, nil
}

func (m *nhcLeaseManager) InvalidateLease(ctx context.Context, nodeName string) error {
	// node might be deleted already, so build it manually
	node := &corev1.Node{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Node",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
	}
	err := m.commonLeaseManager.InvalidateLease(ctx, node)
	if err != nil {
		if _, ok := err.(lease.AlreadyHeldError); ok {
			// lease exists but isn't owned by us, can be ignored
			return nil
		}
		m.log.Error(err, "failed to invalidate lease", "node name", nodeName)
		return err
	}
	return nil
}

func (m *nhcLeaseManager) isLeaseOverdue(l *coordv1.Lease, currentRemediationDuration, previousRemediationsDuration time.Duration, remediationCR *unstructured.Unstructured) (bool, error) {
	if l.Spec.AcquireTime == nil {
		err := fmt.Errorf("lease Spec.AcquireTime is nil")
		m.log.Error(err, "lease Spec.AcquireTime is nil", "lease name", l.Name)
		return false, err
	}

	isLeaseOverdue := time.Now().After(m.calcLeaseExpiration(l, currentRemediationDuration, previousRemediationsDuration))
	return isLeaseOverdue, nil
}

func (m *nhcLeaseManager) calcLeaseExpiration(l *coordv1.Lease, currentRemediationDuration, previousRemediationsDuration time.Duration) time.Time {
	return l.Spec.AcquireTime.Add(time.Duration(maxTimesToExtendLease+1 /*1 is offsetting the lease creation*/)*currentRemediationDuration + previousRemediationsDuration)
}

func (m *nhcLeaseManager) isRemediationsExist(remediationCrs []unstructured.Unstructured) bool {
	return len(remediationCrs) > 0
}

func (m *nhcLeaseManager) isLeaseOwner(l *coordv1.Lease) bool {
	if l.Spec.HolderIdentity == nil {
		return false
	}
	return *l.Spec.HolderIdentity == m.holderIdentity
}
