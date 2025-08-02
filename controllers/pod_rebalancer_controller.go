package controllers

import (
	"context"
	"sort"
	"time"

	"github.com/go-logr/logr"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lokeshllkumar/kube-balance/internal/profiles"
	"github.com/lokeshllkumar/kube-balance/pkg/eviction"
)

// annotation used to mark a node as degraded
const NodeDegradedAnnotation = "kube-balance.io/degraded-io"

// label used to identify the workload type of a pod
const WorkloadTypeLabel = "workload.k8s.io/type"

// annotation to be used on a pod's owner to prevent immediate re-eviction after one of its pods has jsut been evicted
const EvictionCooldownAnnotation = "kube-balance.io/eviction-cooldown-until"

// reconciles the Node and Pod objects to perform rebalancing
type PodRebalancer struct {
	client.Client
	Scheme                      *runtime.Scheme
	Log                         logr.Logger
	Evictor                     *eviction.Evictor
	ProfilerWatcher             *profiles.WorkloadProfileWatcher
	RecheckInterval             time.Duration
	MaxEvictionsPerNodePerCycle int
	Recorder                    record.EventRecorder
}

// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=pods/eviction,verbs=create
// +kubebuilder:rbac:groups="policy",resources=poddisruptionbudgets,verbs=get;list;watch
// +kubebuilder:rbac:groups="apps",resources=deployments;statefulsets;replicasets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="kube-balance.io",resources=workloadprofiles,verbs=get;list;watch

// reconciliation loop for the PodRebalancer controller
func (r *PodRebalancer) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("kube-balancer", req.NamespacedName)

	// fetching all worload profiles from the watcher's cache
	workloadProfiles := r.ProfilerWatcher.GetProfiles()
	if len(workloadProfiles) == 0 {
		log.Info("no workload profiles found, skipping rebalancing; ensure WorkloadProfile CRs (custom resources) are created")
		return ctrl.Result{
			RequeueAfter: r.RecheckInterval,
		}, nil
	}

	// listing all nodes in the cluster
	nodeList := &core.NodeList{}
	if err := r.List(ctx, nodeList); err != nil {
		log.Error(err, "failed to list nodes")
		return ctrl.Result{}, err
	}

	// idenitfying degraded nodes
	degradedNodes := map[string]*core.Node{}
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		if _, ok := node.Annotations[NodeDegradedAnnotation]; ok {
			degradedNodes[node.Name] = node
			log.V(1).Info("identified degraded node", "node", node.Name)
			r.Recorder.Eventf(node, core.EventTypeNormal, "NodeDegraded", "Node %s marked as degraded", node.Name)
		}
	}

	if len(degradedNodes) == 0 {
		log.V(1).Info("no degraded nodes found, skipping rebalancing")
		return ctrl.Result{
			RequeueAfter: r.RecheckInterval,
		}, nil
	}

	// listing all pods in the cluster
	podList := &core.PodList{}
	if err := r.List(ctx, podList); err != nil {
		log.Error(err, "failed to list pods")
		return ctrl.Result{}, err
	}

	// processing each degraded node
	for nodeName, _ := range degradedNodes {
		log.Info("processing degraded node", "node", nodeName)

		var podsOnDegradedNode []*core.Pod
		for i := range podList.Items {
			pod := &podList.Items[i]
			if pod.Spec.NodeName == nodeName && (pod.Status.Phase == core.PodRunning || pod.Status.Phase == core.PodPending) {
				podsOnDegradedNode = append(podsOnDegradedNode, pod)
			}
		}

		if len(podsOnDegradedNode) == 0 {
			log.V(1).Info("no running pods found on degraded node", "node", nodeName)
			continue
		}

		// sorting pods by QoS class and then their eviction priority
		sort.Slice(podsOnDegradedNode, func(i int, j int) bool {
			podA := podsOnDegradedNode[i]
			podB := podsOnDegradedNode[j]

			qosA := getPodQoSClass(podA)
			qosB := getPodQoSClass(podB)
			if qosA != qosB {
				return qosClassToEvictionRank(qosA) > qosClassToEvictionRank(qosB)
			}

			profileA, okA := workloadProfiles[podA.Labels[WorkloadTypeLabel]]
			profileB, okB := workloadProfiles[podB.Labels[WorkloadTypeLabel]]
			if !okA && !okB {
				return false
			}
			if !okA {
				return true
			}
			if !okB {
				return false
			}

			return profileA.Spec.EvictionPriority > profileB.Spec.EvictionPriority
		})

		evictedCount := 0
		for _, pod := range podsOnDegradedNode {
			if evictedCount >= r.MaxEvictionsPerNodePerCycle {
				log.V(1).Info("reached max evictions for node in the current cycle", "node", nodeName, "maxEvictions", r.MaxEvictionsPerNodePerCycle)
				break
			}

			// checking if the pod's owner is in a cooldown period
			owner, err := r.getPodOwner(ctx, pod)
			if err != nil {
				log.Error(err, "failed to get pod owner, skipping cooldown check", "pod", pod.Name)
			} else if owner != nil {
				if cooldownUntilStr, ok := owner.GetAnnotations()[EvictionCooldownAnnotation]; ok {
					if cooldownUntil, err := time.Parse(time.RFC3339, cooldownUntilStr); err == nil && time.Now().Before(cooldownUntil) {
						log.V(1).Info("pod owner is in eviction cooldown period, skipping pod",
							"pod", pod.Name, "namespace", pod.Namespace, "owner", owner.GetName(), "cooldownUntil", cooldownUntil.Format(time.RFC3339))
						r.Recorder.Eventf(pod, core.EventTypeNormal, "EvictionSkipped", "Pod %s skipped due to owner %s being in cooldown until %s", pod.Name, owner.GetName(), cooldownUntil.Format(time.RFC3339))
						continue
					}
				}
			}

			// checking Pod Disruption Budget before eviction
			if err := r.checkPDB(ctx, pod); err != nil {
				log.V(1).Info("pod cannot be evicted due to PDB violation or check error", "pod", pod.Name, "namespace", pod.Namespace, "error", err.Error())
				r.Recorder.Eventf(pod, core.EventTypeWarning, "PDBViolation", "Pod %s cannot be evicted due to PDB violation: %v", pod.Name, err)
				continue
			}

			workloadType := pod.Labels[WorkloadTypeLabel]
			profile, profileFound := workloadProfiles[workloadType]

			if profileFound {
				log.Info("attempting to evist pod from degraded node",
					"pod", pod.Name,
					"namespace", pod.Namespace,
					"node", nodeName,
					"workloadType", workloadType,
					"qosClass", getPodQoSClass(pod),
					"evictionPriority", profile.Spec.EvictionPriority,
				)

				// eviction logic
				err := r.Evictor.EvictPod(ctx, pod)
				if err != nil {
					if errors.IsTooManyRequests(err) {
						log.Info("too many eviction requests, backing off", "pod", pod.Name)
						r.Recorder.Eventf(pod, core.EventTypeWarning, "EvictionRateLimited", "Eviction of pod %s rate limited by K8s API server", pod.Name)
						return ctrl.Result{
							RequeueAfter: 10 * time.Second,
						}, nil
					}
					log.Error(err, "failed to evict pod", "pod", pod.Name, "namespace", pod.Namespace)
					r.Recorder.Eventf(pod, core.EventTypeWarning, "EvictionFailed", "Failed to evict pod %s: %v", pod.Name, err)
					continue
				}

				log.Info("successfully evicted pod", "pod", pod.Name, "namespace", pod.Namespace)
				r.Recorder.Eventf(pod, core.EventTypeNormal, "PodEvicted", "Pod %s evicted from degraded node %s", pod.Name, nodeName)
				evictedCount++

				// setting cooldown annotation on the pod's owner
				if owner != nil {
					cooldownUntil := time.Now().Add(r.RecheckInterval * 2) // cooldown for a minimum of 2 recheck intervals
					patch := client.MergeFrom(owner.DeepCopyObject().(client.Object))
					annotations := owner.GetAnnotations()
					if annotations == nil {
						annotations = make(map[string]string)
					}
					annotations[EvictionCooldownAnnotation] = cooldownUntil.Format(time.RFC3339)
					owner.SetAnnotations(annotations)
					if err := r.Patch(ctx, owner, patch); err != nil {
						log.Error(err, "failed to add eviction cooldown annotation to the pod owner", "owner", owner.GetName(), "namespace", owner.GetNamespace())
						r.Recorder.Eventf(owner, core.EventTypeWarning, "CooldownAnnotationFailed", "Failed to add cooldown annotation to owner %s: %v", owner.GetName(), err)
					} else {
						log.V(1).Info("added eviction cooldown annotation to pod owner", "owner", owner.GetName(), "cooldownUntil", cooldownUntil.Format(time.RFC3339))
						r.Recorder.Eventf(owner, core.EventTypeNormal, "CooldownSet", "Cooldown set on owner %s until %s", owner.GetName(), cooldownUntil.Format(time.RFC3339))
					}
				}

				return ctrl.Result{
					RequeueAfter: 5 * time.Second,
				}, nil
			} else {
				log.V(1).Info("pod ha no defined workload profile, skipping eviction consideration",
					"pod", pod.Name, "namespace", pod.Namespace, "workloadType", workloadType)
			}
		}
	}

	return ctrl.Result{
		RequeueAfter: r.RecheckInterval,
	}, nil
}
