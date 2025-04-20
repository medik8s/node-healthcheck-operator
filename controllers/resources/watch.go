package resources

import (
	"sync"

	"github.com/go-logr/logr"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/api/machine/v1beta1"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
)

type WatchManager interface {
	AddWatchesNhc(rm Manager, nhc *v1alpha1.NodeHealthCheck) error
	AddWatchesMhc(rm Manager, mhc *v1beta1.MachineHealthCheck) error
	SetController(controller.Controller)
}

type watchManager struct {
	client     client.Client
	log        logr.Logger
	controller controller.Controller
	cache      cache.Cache
	Watches    map[string]struct{}
	lock       *sync.Mutex
}

// NewWatchManager creates a new instance of WatchManager.
func NewWatchManager(c client.Client, log logr.Logger, cache cache.Cache) WatchManager {
	return &watchManager{
		client:  c,
		log:     log,
		cache:   cache,
		Watches: make(map[string]struct{}),
		lock:    &sync.Mutex{},
	}
}

func (wm *watchManager) AddWatchesNhc(rm Manager, nhc *v1alpha1.NodeHealthCheck) error {
	var remediationsToWatch []v1.ObjectReference
	//aggregate all templates to watch
	if nhc.Spec.RemediationTemplate != nil {
		remediationsToWatch = append(remediationsToWatch, *nhc.Spec.RemediationTemplate)
	}
	for _, escalatingRemediation := range nhc.Spec.EscalatingRemediations {
		remediationsToWatch = append(remediationsToWatch, escalatingRemediation.RemediationTemplate)
	}
	return wm.addWatches(rm, remediationsToWatch, utils.NHC)
}

func (wm *watchManager) AddWatchesMhc(rm Manager, mhc *v1beta1.MachineHealthCheck) error {
	var remediationsToWatch []v1.ObjectReference
	//aggregate all templates to watch
	if mhc.Spec.RemediationTemplate != nil {
		remediationsToWatch = append(remediationsToWatch, *mhc.Spec.RemediationTemplate)
	}
	return wm.addWatches(rm, remediationsToWatch, utils.MHC)
}

func (wm *watchManager) SetController(controller controller.Controller) {
	wm.controller = controller
}

func (wm *watchManager) addWatches(rm Manager, templates []v1.ObjectReference, watchType utils.WatchType) error {
	for _, ref := range templates {
		template := rm.GenerateTemplate(&ref)
		if err := wm.addRemediationTemplateCRWatch(template, watchType); err != nil {
			wm.log.Error(err, "failed to add watch for template CR", "kind", template.GetKind())
			return err
		}
		rem := rm.GenerateRemediationCRBase(template.GroupVersionKind())
		if err := wm.addRemediationCRWatch(rem, watchType); err != nil {
			wm.log.Error(err, "failed to add watch for remediation CR", "kind", rem.GetKind())
			return err
		}
	}
	return nil
}

func (wm *watchManager) addRemediationTemplateCRWatch(templateCR *unstructured.Unstructured, watchType utils.WatchType) error {
	wm.lock.Lock()
	defer wm.lock.Unlock()

	key := templateCR.GroupVersionKind().String()
	if _, exists := wm.Watches[key]; exists {
		return nil
	}
	var mapperFunc handler.MapFunc
	if watchType == utils.NHC {
		mapperFunc = utils.NHCByRemediationTemplateCRMapperFunc(wm.client, wm.log)
	} else {
		mapperFunc = utils.MHCByRemediationTemplateCRMapperFunc(wm.client, wm.log)
	}

	if err := wm.controller.Watch(
		source.Kind(wm.cache, templateCR),
		handler.EnqueueRequestsFromMapFunc(mapperFunc),
		predicate.Funcs{
			// we are just interested in update and delete events for now
			// template CR updates: validate
			// template CR deletion: update NHC/MHC status
			CreateFunc:  func(_ event.CreateEvent) bool { return false },
			GenericFunc: func(_ event.GenericEvent) bool { return false },
		},
	); err != nil {
		return err
	}
	wm.Watches[key] = struct{}{}
	wm.log.Info("added watch for remediation template CRs", "kind", templateCR.GetKind())
	return nil
}

func (wm *watchManager) addRemediationCRWatch(remediationCR *unstructured.Unstructured, watchType utils.WatchType) error {
	wm.lock.Lock()
	defer wm.lock.Unlock()

	key := remediationCR.GroupVersionKind().String()
	if _, exists := wm.Watches[key]; exists {
		return nil
	}

	if err := wm.controller.Watch(
		source.Kind(wm.cache, remediationCR),
		handler.EnqueueRequestsFromMapFunc(utils.RemediationCRMapperFunc(wm.log, watchType)),
		predicate.Funcs{
			// we are just interested in update and delete events for now
			// remediation CR update: watch conditions
			// remediation CR deletion: clean up
			CreateFunc:  func(_ event.CreateEvent) bool { return false },
			GenericFunc: func(_ event.GenericEvent) bool { return false },
		},
	); err != nil {
		return err
	}
	wm.Watches[key] = struct{}{}
	wm.log.Info("added watch for remediation CRs", "kind", remediationCR.GetKind())
	return nil
}
