package lease

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	coordv1 "k8s.io/api/coordination/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	//NSEnvVar is containing the value of the namespace leases will be managed at, in case it's empty defaultLeaseNs will be used
	NSEnvVar       = "LEASE_NAMESPACE"
	defaultLeaseNs = "medik8s-leases"
)

type Manager interface {
	//RequestLease will create a lease with leaseDuration if it does not exist or extend existing lease duration to leaseDuration.
	//It'll return an error in case it can't do either (for example if the lease is already taken).
	RequestLease(ctx context.Context, obj client.Object, leaseDuration time.Duration) error
	//InvalidateLease will release the lease.
	InvalidateLease(ctx context.Context, obj client.Object) error
	//GetLease will try to fetch a lease.
	//It'll return an error in case it can't (for example if the lease does not exist or is already taken).
	GetLease(ctx context.Context, obj client.Object) (*coordv1.Lease, error)
}

type manager struct {
	client.Client
	holderIdentity string
	namespace      string
	log            logr.Logger
}

// AlreadyHeldError is returned in case the lease that is requested is already held by a different holder
type AlreadyHeldError struct {
	holderIdentity string
}

func (e *AlreadyHeldError) Error() string {
	return fmt.Sprintf("can't update valid lease held by different owner: %s", e.holderIdentity)
}

func (l *manager) RequestLease(ctx context.Context, obj client.Object, leaseDuration time.Duration) error {
	return l.requestLease(ctx, obj, leaseDuration)
}

func (l *manager) GetLease(ctx context.Context, obj client.Object) (*coordv1.Lease, error) {
	return l.getLease(ctx, obj)
}

func (l *manager) InvalidateLease(ctx context.Context, obj client.Object) error {
	return l.invalidateLease(ctx, obj)
}

func NewManager(cl client.Client, holderIdentity string) (Manager, error) {
	return NewManagerWithCustomLogger(cl, holderIdentity, ctrl.Log.WithName("leaseManager"))

}

func setupLeaseNs(cl client.Client, log logr.Logger, leaseNsName string) error {
	ns := &v1.Namespace{}
	key := apitypes.NamespacedName{Name: leaseNsName}
	if err := cl.Get(context.Background(), key, ns); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "couldn't get lease namespace", "namespace", leaseNsName)
			return errors.Wrap(err, "couldn't get lease namespace")
		}
		if err := createLeaseNs(cl, leaseNsName); err != nil {
			log.Error(err, "couldn't create lease namespace", "namespace", leaseNsName)
			return errors.Wrap(err, "couldn't create lease namespace")
		}
	}
	return nil
}

func getLeaseNsName() string {
	leaseNsName := defaultLeaseNs
	if leaseNsOverride, ok := os.LookupEnv(NSEnvVar); ok {
		leaseNsName = leaseNsOverride
	}
	return leaseNsName
}

func createLeaseNs(cl client.Client, leaseNsName string) error {
	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: leaseNsName,
		},
	}
	return cl.Create(context.Background(), ns)
}

func NewManagerWithCustomLogger(cl client.Client, holderIdentity string, log logr.Logger) (Manager, error) {
	leaseNsName := getLeaseNsName()
	if err := setupLeaseNs(cl, log, leaseNsName); err != nil {
		return nil, err
	}
	return &manager{
		Client:         cl,
		holderIdentity: holderIdentity,
		namespace:      leaseNsName,
		log:            log,
	}, nil
}

func (l *manager) createLease(ctx context.Context, obj client.Object, duration time.Duration) error {
	log.Info("create lease")
	kind, _, err := getObjKindVersion(obj)
	if err != nil {
		l.log.Error(err, "couldn't fetch obj kind")
		return err
	}
	//no need to check for error, already checked
	owner, _ := makeExpectedOwnerOfLease(obj)
	microTimeNow := metav1.NowMicro()

	lease := &coordv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:            generateLeaseName(kind, obj.GetName()),
			Namespace:       l.namespace,
			OwnerReferences: []metav1.OwnerReference{*owner},
		},
		Spec: coordv1.LeaseSpec{
			HolderIdentity:       &l.holderIdentity,
			LeaseDurationSeconds: pointer.Int32(int32(duration.Seconds())),
			AcquireTime:          &microTimeNow,
			RenewTime:            &microTimeNow,
			LeaseTransitions:     pointer.Int32(0),
		},
	}

	if err := l.Client.Create(ctx, lease); err != nil {
		l.log.Error(err, "failed to create lease")
		return err
	}
	return nil
}

func (l *manager) requestLease(ctx context.Context, obj client.Object, leaseDuration time.Duration) error {
	log.Info("request lease")
	lease, err := l.getLease(ctx, obj)

	if err != nil {
		//couldn't get the lease try to create one
		if apierrors.IsNotFound(err) {
			if err = l.createLease(ctx, obj, leaseDuration); err != nil {
				l.log.Error(err, "couldn't create lease")
				return err
			} else {
				//lease created successfully
				return nil
			}
		} else {
			l.log.Error(err, "couldn't fetch lease")
			return err
		}
	}

	needUpdateLease := false
	setAcquireAndLeaseTransitions := false
	currentTime := metav1.NowMicro()
	if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity == l.holderIdentity {
		needUpdateLease, setAcquireAndLeaseTransitions = needUpdateOwnedLease(lease, currentTime, leaseDuration)
		if needUpdateLease {
			log.Infof("renew lease owned by %s setAcquireTime=%t", l.holderIdentity, setAcquireAndLeaseTransitions)

		}
	} else {
		// can't take over the lease if it is currently valid.
		if isValidLease(lease, currentTime.Time) {
			identity := "unknown"
			if lease.Spec.HolderIdentity != nil {
				identity = *lease.Spec.HolderIdentity
			}
			return &AlreadyHeldError{holderIdentity: identity}
		}
		needUpdateLease = true

		log.Info("taking over foreign lease")
		setAcquireAndLeaseTransitions = true
	}

	if needUpdateLease {
		if setAcquireAndLeaseTransitions {
			lease.Spec.AcquireTime = &currentTime
			if lease.Spec.LeaseTransitions != nil {
				*lease.Spec.LeaseTransitions += int32(1)
			} else {
				lease.Spec.LeaseTransitions = pointer.Int32(1)
			}
		}
		//no need to check for error, was already checked at the top getLease statement
		owner, _ := makeExpectedOwnerOfLease(obj)
		lease.ObjectMeta.OwnerReferences = []metav1.OwnerReference{*owner}
		lease.Spec.HolderIdentity = &l.holderIdentity
		lease.Spec.LeaseDurationSeconds = pointer.Int32(int32(leaseDuration.Seconds()))
		lease.Spec.RenewTime = &currentTime
		if err := l.Client.Update(ctx, lease); err != nil {
			log.Errorf("Failed to update the lease. obj %s error: %v", obj.GetName(), err)
			return err
		}
	}

	return nil
}

func (l *manager) invalidateLease(ctx context.Context, obj client.Object) error {
	log.Info("invalidating lease")
	lease, err := l.getLease(ctx, obj)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err := l.Client.Delete(ctx, lease); err != nil {
		log.Error(err, "failed to delete lease to be invalidated")
		return err
	}

	return nil
}

func makeExpectedOwnerOfLease(obj client.Object) (*metav1.OwnerReference, error) {
	kind, version, err := getObjKindVersion(obj)
	if err != nil {
		return nil, err
	}
	return &metav1.OwnerReference{
		APIVersion: version,
		Kind:       kind,
		Name:       obj.GetName(),
		UID:        obj.GetUID(),
	}, nil
}

func leaseDueTime(lease *coordv1.Lease) time.Time {
	return lease.Spec.RenewTime.Time.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
}

func needUpdateOwnedLease(lease *coordv1.Lease, currentTime metav1.MicroTime, requestedLeaseDuration time.Duration) (bool, bool) {

	if lease.Spec.RenewTime == nil || lease.Spec.LeaseDurationSeconds == nil {
		log.Info("empty renew time or duration in sec")
		return true, true
	}
	dueTime := leaseDueTime(lease)

	// if lease expired right now, then both update the lease and the acquire time (second rvalue)
	// if the acquire time has been previously nil
	if dueTime.Before(currentTime.Time) {
		return true, lease.Spec.AcquireTime == nil
	}

	deadline := currentTime.Add(requestedLeaseDuration)

	// about to expire, update the lease but not the acquired time (second value)
	return dueTime.Before(deadline), false
}

func isValidLease(lease *coordv1.Lease, currentTime time.Time) bool {

	if lease.Spec.RenewTime == nil || lease.Spec.LeaseDurationSeconds == nil {
		return false
	}

	renewTime := (*lease.Spec.RenewTime).Time
	dueTime := leaseDueTime(lease)

	// valid lease if: due time not in the past and renew time not in the future
	return !dueTime.Before(currentTime) && !renewTime.After(currentTime)
}

func (l *manager) getLease(ctx context.Context, obj client.Object) (*coordv1.Lease, error) {
	log.Info("getting lease")
	kind, _, err := getObjKindVersion(obj)
	if err != nil {
		l.log.Error(err, "couldn't fetch obj kind")
		return nil, err
	}
	nName := apitypes.NamespacedName{Namespace: l.namespace, Name: generateLeaseName(kind, obj.GetName())}
	lease := &coordv1.Lease{}

	if err := l.Client.Get(ctx, nName, lease); err != nil {
		if !apierrors.IsNotFound(err) {
			l.log.Error(err, "couldn't fetch lease")
		}
		return nil, err
	}

	return lease, nil
}

func generateLeaseName(kind string, name string) string {
	return strings.ToLower(fmt.Sprintf("%s-%s", kind, name))
}

func getObjKindVersion(obj client.Object) (string, string, error) {
	kind := obj.GetObjectKind().GroupVersionKind().Kind
	version := obj.GetObjectKind().GroupVersionKind().Version

	if len(kind) == 0 || len(version) == 0 {
		if _, ok := obj.(*v1.Node); ok {
			kind = v1.SchemeGroupVersion.WithKind("Node").Kind
			version = v1.SchemeGroupVersion.WithKind("Node").Version
		} else if _, ok := obj.(*v1.Pod); ok {
			kind = v1.SchemeGroupVersion.WithKind("Pod").Kind
			version = v1.SchemeGroupVersion.WithKind("Pod").Version
		} else {
			return "", "", fmt.Errorf("couldn't find kind or version for obj %s", obj.GetName())
		}
	}
	return kind, version, nil
}
