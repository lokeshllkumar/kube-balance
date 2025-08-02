package eviction

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	core "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// defines an object to evict pods
type Evictor struct {
	Client client.Client
	Log    logr.Logger
}

// creates a new Evictor instance
func NewEvictor(cli client.Client, log logr.Logger) *Evictor {
	return &Evictor{
		Client: cli,
		Log:    log,
	}
}

// performs a soft eviction of a pod by gracefully terminating it via an eviction request to the K8s API server
func (e *Evictor) EvictPod(ctx context.Context, pod *core.Pod) error {
	eviction := &policy.Eviction{
		ObjectMeta: meta.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		DeleteOptions: &meta.DeleteOptions{
			GracePeriodSeconds: func(i int64) *int64 {
				return &i
			}(30),
		},
	}

	e.Log.Info("attempting to evict pod", "pod", pod.Name, "namespace", pod.Namespace, "node", pod.Spec.NodeName)

	err := e.Client.SubResource("eviction").Create(ctx, pod, eviction)
	if err != nil {
		return fmt.Errorf("failed to create eviction for pod %s/%s: %w", pod.Namespace, pod.Name, err)
	}

	e.Log.Info("eviction request sent for pod", "pod", pod.Name, "namespace", pod.Namespace)
	return nil
}
