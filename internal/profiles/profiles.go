package profiles

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	cache "k8s.io/client-go/tools/cache"
	controller_cache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api_v1 "github.com/lokeshllkumar/kube-balance/api/v1alpha1"
)

// watches WorkloadProfile custom resources and maintains a cached map
type WorkloadProfileWatcher struct {
	client.Client
	Cache controller_cache.Cache
	Log   logr.Logger

	// protects the profiles map for concurrent access
	profilesMu sync.RWMutex
	// cached map of workload profiles by name
	profiles map[string]api_v1.WorkloadProfile
}

// creates a new WorkloadProfileWatcher instance
func NewWorkloadProfileWatcher(cli client.Client, c controller_cache.Cache, log logr.Logger) *WorkloadProfileWatcher {
	return &WorkloadProfileWatcher{
		Client:   cli,
		Cache:    c,
		Log:      log,
		profiles: make(map[string]api_v1.WorkloadProfile),
	}
}

// returns the currently cached workload profiles
func (wpw *WorkloadProfileWatcher) GetProfiles() map[string]api_v1.WorkloadProfile {
	wpw.profilesMu.RLock()
	defer wpw.profilesMu.RUnlock()

	copiedProfiles := make(map[string]api_v1.WorkloadProfile, len(wpw.profiles))
	for k, v := range wpw.profiles {
		copiedProfiles[k] = v
	}
	return copiedProfiles
}

// implements the manager.Runnable interface to set up an infromer that watches WorkloadProfile custom resources and update the local cache
func (wpw *WorkloadProfileWatcher) Start(ctx context.Context) error {
	informer, err := wpw.Cache.GetInformer(ctx, &api_v1.WorkloadProfile{})
	if err != nil {
		return fmt.Errorf("failed to get WorkloadProfile informer: %v", err)
	}

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			wp := obj.(*api_v1.WorkloadProfile)
			wpw.profilesMu.Lock()
			wpw.profiles[wp.Name] = *wp.DeepCopy()
			wpw.profilesMu.Unlock()
			wpw.Log.V(1).Info("added workload profile to cache", "name", wp.Name)
		},
		UpdateFunc: func(oldObj interface{}, newObj interface{}) {
			wp := newObj.(*api_v1.WorkloadProfile)
			wpw.profilesMu.Lock()
			wpw.profiles[wp.Name] = *wp.DeepCopy()
			wpw.profilesMu.Unlock()
			wpw.Log.V(1).Info("updated workload profile in cache", "name", wp.Name)
		},
		DeleteFunc: func(obj interface{}) {
			wp, ok := obj.(*api_v1.WorkloadProfile)
			if !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					wpw.Log.Error(fmt.Errorf("error decoding object, invalid type"), "Failed to decode object for delete event")
					return
				}
				wp, ok = tombstone.Obj.(*api_v1.WorkloadProfile)
				if !ok {
					wpw.Log.Error(fmt.Errorf("error decoding object tombstone, invalid type"), "Failed to decode object for delete event")
					return
				}
			}
			wpw.profilesMu.Lock()
			delete(wpw.profiles, wp.Name)
			wpw.profilesMu.Unlock()
			wpw.Log.V(1).Info("deleted workload profile from cache", "name", wp.Name)
		},
	})

	wpw.Log.Info("WorkloadProfileWatcher is ready to receive events via the manager's cache")
	return nil
}
