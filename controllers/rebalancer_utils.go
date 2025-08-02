package controllers

import (
	"context"
	"fmt"

	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

// determines the QoS class of a pod
func getPodQoSClass(pod *core.Pod) core.PodQOSClass {
	if pod.Spec.Containers == nil {
		return core.PodQOSBestEffort
	}

	// for return QOS
	guaranteed := true
	burstable := false

	for _, container := range pod.Spec.Containers {
		// best effort
		if container.Resources.Requests == nil && container.Resources.Limits == nil {
			guaranteed = false
			burstable = false
			break
		}

		// burstable - if requests are not equal to limits for CPU and memory
		if container.Resources.Requests.Cpu().Cmp(*container.Resources.Limits.Cpu()) != 0 ||
			container.Resources.Requests.Memory().Cmp(*container.Resources.Limits.Memory()) != 0 {
			guaranteed = false
			burstable = true
		}

		// guaranteed - if requests are not set
		if container.Resources.Requests.Cpu().IsZero() || container.Resources.Requests.Memory().IsZero() {
			guaranteed = false
		}
	}

	if guaranteed {
		return core.PodQOSGuaranteed
	}
	if burstable {
		return core.PodQOSBurstable
	}
	return core.PodQOSBestEffort
}

// assigns a rank for eviction priority
func qosClassToEvictionRank(qos core.PodQOSClass) int {
	switch qos {
	case core.PodQOSBestEffort:
		return 3
	case core.PodQOSBurstable:
		return 2
	case core.PodQOSGuaranteed:
		return 1
	default:
		return 0 // handling edge case, typically shouldn't happen
	}
}

// attempts to find the Deployment, StatefulSet, or ReplicaSet that owns the pod
func (r *PodRebalancer) getPodOwner(ctx context.Context, pod *core.Pod) (client.Object, error) {
	for _, ownerRef := range pod.OwnerReferences {
		if ownerRef.Controller != nil && *ownerRef.Controller {
			switch ownerRef.Kind {
			case "ReplicaSet":
				rs := &apps.ReplicaSet{}
				if err := r.Get(ctx, types.NamespacedName{
					Name:      ownerRef.Name,
					Namespace: pod.Namespace,
				}, rs); err != nil {
					return nil, fmt.Errorf("failed to get ReplicaSet %s: %w", ownerRef.Name, err)
				}

				// for when a ReplicaSet might be owned by a deployment
				for _, rsOwnerRef := range rs.OwnerReferences {
					if rsOwnerRef.Controller != nil && *rsOwnerRef.Controller && rsOwnerRef.Kind == "Deployment" {
						deploy := &apps.Deployment{}
						if err := r.Get(ctx, types.NamespacedName{
							Name:      rsOwnerRef.Name,
							Namespace: pod.Namespace,
						}, deploy); err != nil {
							return nil, fmt.Errorf("failed to get Deployment %s: %w", rsOwnerRef.Name, err)
						}

						return deploy, nil
					}
				}
				return rs, nil
			case "StatefulSet":
				ss := &apps.StatefulSet{}
				if err := r.Get(ctx, types.NamespacedName{
					Name:      ownerRef.Name,
					Namespace: pod.Namespace,
				}, ss); err != nil {
					return nil, fmt.Errorf("failed to get StatefulSet %s: %w", ownerRef.Name, err)
				}
				return ss, nil
			case "Deployment":
				deploy := &apps.Deployment{}
				if err := r.Get(ctx, types.NamespacedName{
					Name:      ownerRef.Name,
					Namespace: pod.Namespace,
				}, deploy); err != nil {
					return nil, fmt.Errorf("failed to get deployment %s: %w", ownerRef.Name, err)
				}
				return deploy, nil
			}
		}
	}

	return nil, nil // when no controller owner is found
}

// checks if evicting a given pod would violate any PodDisruptionBudget
func (r *PodRebalancer) checkPDB(ctx context.Context, pod *core.Pod) error {
	pdbList := &policy.PodDisruptionBudgetList{}
	if err := r.List(ctx, pdbList, &client.ListOptions{
		Namespace: pod.Namespace,
	}); err != nil {
		return fmt.Errorf("failed to list PodDisruptionBudgets in namespace %s: %w", pod.Namespace, err)
	}

	for _, pdb := range pdbList.Items {
		selector, err := meta.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil {
			r.Log.Error(err, "invalid PDB selector", "pdb", pdb.Name)
			continue
		}

		if selector.Matches(labels.Set(pod.Labels)) {
			if pdb.Status.DisruptionsAllowed == 0 {
				return fmt.Errorf("eviction would violate PodDisruptionBudget %s (disruptionsAllowed: 0)", pdb.Name)
			}
		}
	}

	return nil
}

// sets up the controller with the Manager by informing it which resources it must watches and how it must handle events
func (r *PodRebalancer) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&core.Node{}).
		Watches(&core.Pod{}, &handler.EnqueueRequestForObject{}).
		Watches(&apps.Deployment{}, &handler.EnqueueRequestForObject{}).
		Watches(&apps.StatefulSet{}, &handler.EnqueueRequestForObject{}).
		Watches(&apps.ReplicaSet{}, &handler.EnqueueRequestForObject{}).
		Complete(r)
}
