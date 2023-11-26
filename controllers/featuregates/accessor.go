package featuregates

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"

	osconfigv1 "github.com/openshift/api/config/v1"
	osclientset "github.com/openshift/client-go/config/clientset/versioned"
	configinformersv1 "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
)

const (
	// Minimum server version to use feature gate accessor
	// OCP 4.14+ = K8s 1.27
	minFeatureGateVersionMajor = 1
	minFeatureGateVersionMinor = 27
)

var (
	leadingDigits = regexp.MustCompile(`^(\d+)`)
)

type Accessor interface {
	// Start implements the manager's Runnable interface
	Start(context.Context) error
	// IsMachineAPIOperatorMHCDisabled returns if the Machine API Operator's MachineHealthCheck controller is disabled
	IsMachineAPIOperatorMHCDisabled() bool
}

type accessor struct {
	config                                 *rest.Config
	log                                    logr.Logger
	featureGateMHCControllerDisabledEvents chan<- event.GenericEvent
	featureGateAccessor                    featuregates.FeatureGateAccess
	isMaoMhcDisabled                       bool
	isMaoMhcDisabledLock                   *sync.Mutex
}

var _ Accessor = &accessor{}

func NewAccessor(cfg *rest.Config, featureGateMHCControllerDisabledEvents chan<- event.GenericEvent) Accessor {
	return &accessor{
		config:                                 cfg,
		log:                                    ctrl.Log.WithName("FeatureGateAccessor"),
		featureGateMHCControllerDisabledEvents: featureGateMHCControllerDisabledEvents,
		isMaoMhcDisabledLock:                   &sync.Mutex{},
	}
}

func (fga *accessor) Start(ctx context.Context) error {
	osClient, err := osclientset.NewForConfig(fga.config)
	if err != nil {
		fga.log.Error(err, "failed to get openshift client")
		return err
	}
	if featureGateSupported, err := isFeatureGateAccessorSupported(osClient, fga.log); err != nil {
		fga.log.Error(err, "failed to check version for feature gate support")
		return err
	} else if !featureGateSupported {
		fga.log.Info("feature gate accessor not supported")
		return nil
	}

	configSharedInformer := configinformersv1.NewSharedInformerFactoryWithOptions(osClient, time.Hour)
	clusterVersionsInformer := configSharedInformer.Config().V1().ClusterVersions()
	featureGatesInformer := configSharedInformer.Config().V1().FeatureGates()

	recorder := events.NewLoggingEventRecorder("accessor")

	// using the same version for desiredVersion and missingVersion will let the accessor just use
	// the current deployed version, which is what we want...
	version := "n/a"
	fga.featureGateAccessor = featuregates.NewFeatureGateAccess(
		version, version, clusterVersionsInformer, featureGatesInformer, recorder,
	)
	fga.featureGateAccessor.SetChangeHandler(fga.featureGateChanged)

	configSharedInformer.Start(ctx.Done())
	if !cache.WaitForNamedCacheSync("feature gate accessor", ctx.Done(),
		clusterVersionsInformer.Informer().HasSynced, featureGatesInformer.Informer().HasSynced) {
		err = fmt.Errorf("failed to sync informers")
		fga.log.Error(err, "failed to setup feature gate accessor")
		return err
	}

	go fga.featureGateAccessor.Run(ctx)
	select {
	case <-fga.featureGateAccessor.InitialFeatureGatesObserved():
		fga.log.Info("FeatureGates initialized")
	case <-time.After(1 * time.Minute):
		err := errors.New("timed out waiting for FeatureGate detection")
		fga.log.Error(err, "unable to start accessor")
		return err
	}

	return nil
}

func (fga *accessor) IsMachineAPIOperatorMHCDisabled() bool {
	fga.isMaoMhcDisabledLock.Lock()
	defer fga.isMaoMhcDisabledLock.Unlock()
	return fga.isMaoMhcDisabled
}

func (fga *accessor) featureGateChanged(fc featuregates.FeatureChange) {
	fga.isMaoMhcDisabledLock.Lock()
	defer fga.isMaoMhcDisabledLock.Unlock()

	for _, fg := range fc.New.Enabled {
		if fg == osconfigv1.FeatureGateMachineAPIOperatorDisableMachineHealthCheckController {
			if !fga.isMaoMhcDisabled {
				fga.isMaoMhcDisabled = true
				fga.log.Info("MachineAPIOperatorDisableMachineHealthCheckController feature gate is enabled")
				// trigger reconcile of all MHCs
				fga.featureGateMHCControllerDisabledEvents <- event.GenericEvent{}
			}
			return
		}
	}
	if fga.isMaoMhcDisabled {
		fga.isMaoMhcDisabled = false
		fga.log.Info("MachineAPIOperatorDisableMachineHealthCheckController feature gate is disabled")
	}
}

func isFeatureGateAccessorSupported(cs *osclientset.Clientset, log logr.Logger) (bool, error) {
	versionInfo, err := cs.ServerVersion()
	if err != nil {
		log.Error(err, "failed to get server version")
		return false, err
	}
	majorVer, err := strconv.Atoi(versionInfo.Major)
	if err != nil {
		log.Error(err, "couldn't parse k8s major version", "major version", versionInfo.Major)
		return false, err
	}
	minorVer, err := strconv.Atoi(leadingDigits.FindString(versionInfo.Minor))
	if err != nil {
		log.Error(err, "couldn't parse k8s minor version", "minor version", versionInfo.Minor)
		return false, err
	}
	if majorVer > minFeatureGateVersionMajor || (majorVer == minFeatureGateVersionMajor) && minorVer >= minFeatureGateVersionMinor {
		return true, nil
	}
	return false, nil

}

type FakeAccessor struct {
	IsMaoMhcDisabled bool
}

var _ Accessor = &FakeAccessor{}

func (ds *FakeAccessor) Start(_ context.Context) error {
	return nil
}
func (ds *FakeAccessor) IsMachineAPIOperatorMHCDisabled() bool {
	return ds.IsMaoMhcDisabled
}
