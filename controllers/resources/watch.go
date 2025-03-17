package resources

import (
	"sync"

	"github.com/go-logr/logr"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
)

type WatchManager struct {
	Client     client.Client
	Log        logr.Logger
	Controller controller.Controller
	Cache      cache.Cache
	Watches    map[string]struct{}
	Lock       *sync.Mutex
}

// NewWatchManager creates a new instance of WatchManager.
func NewWatchManager(c client.Client, log logr.Logger, cache cache.Cache) *WatchManager {
	return &WatchManager{
		Client:  c,
		Log:     log,
		Cache:   cache,
		Watches: make(map[string]struct{}),
		Lock:    &sync.Mutex{},
	}
}

func (wm *WatchManager) AddWatchesNhc(rm Manager, nhc *v1alpha1.NodeHealthCheck) error {
	var remediationsToWatch []v1.ObjectReference
	//aggregate all templates to watch
	if nhc.Spec.RemediationTemplate != nil {
		remediationsToWatch = append(remediationsToWatch, *nhc.Spec.RemediationTemplate)
	}
	for _, escalatingRemediation := range nhc.Spec.EscalatingRemediations {
		remediationsToWatch = append(remediationsToWatch, escalatingRemediation.RemediationTemplate)
	}
	return wm.addWatches(rm, remediationsToWatch)
}

func (wm *WatchManager) addWatches(rm Manager, templates []v1.ObjectReference) error {
	for _, ref := range templates {
		template := rm.GenerateTemplate(&ref)
		if err := wm.addRemediationTemplateCRWatch(template); err != nil {
			wm.Log.Error(err, "failed to add watch for template CR", "kind", template.GetKind())
			return err
		}
		rem := rm.GenerateRemediationCRBase(template.GroupVersionKind())
		if err := wm.addRemediationCRWatch(rem); err != nil {
			wm.Log.Error(err, "failed to add watch for remediation CR", "kind", rem.GetKind())
			return err
		}
	}
	return nil
}

func (wm *WatchManager) addRemediationTemplateCRWatch(templateCR *unstructured.Unstructured) error {
	wm.Lock.Lock()
	defer wm.Lock.Unlock()

	key := templateCR.GroupVersionKind().String()
	if _, exists := wm.Watches[key]; exists {
		return nil
	}

	if err := wm.Controller.Watch(
		source.Kind(wm.Cache, templateCR),
		handler.EnqueueRequestsFromMapFunc(utils.NHCByRemediationTemplateCRMapperFunc(wm.Client, wm.Log)),
		predicate.Funcs{
			CreateFunc:  func(_ event.CreateEvent) bool { return false },
			GenericFunc: func(_ event.GenericEvent) bool { return false },
		},
	); err != nil {
		return err
	}
	wm.Watches[key] = struct{}{}
	wm.Log.Info("added watch for remediation template CRs", "kind", templateCR.GetKind())
	return nil
}

func (wm *WatchManager) addRemediationCRWatch(remediationCR *unstructured.Unstructured) error {
	wm.Lock.Lock()
	defer wm.Lock.Unlock()

	key := remediationCR.GroupVersionKind().String()
	if _, exists := wm.Watches[key]; exists {
		return nil
	}

	if err := wm.Controller.Watch(
		source.Kind(wm.Cache, remediationCR),
		handler.EnqueueRequestsFromMapFunc(utils.NHCByRemediationCRMapperFunc(wm.Log)),
		predicate.Funcs{
			CreateFunc:  func(_ event.CreateEvent) bool { return false },
			GenericFunc: func(_ event.GenericEvent) bool { return false },
		},
	); err != nil {
		return err
	}
	wm.Watches[key] = struct{}{}
	wm.Log.Info("added watch for remediation CRs", "kind", remediationCR.GetKind())
	return nil
}
