# kube-balance

A POC Kubernetes controller designed to proactively mitigate the "noisy neighbour" problem by rebalancing workloads across nodes. It moves beyong standard resource limits and reactive autoscaling by using a custom, configurable logic to identify performance-degraded (specifically with respect to CPU and memory usage) nodes and prioritize the eviction of less-critical or "noisy" workloads. The plugin leverages K8s CRDs (Custom Resource Defintions) and RBAC to provide a native, extensible solution.

## Features

- CRD (Custom Resource Definition) for Workload Profiling: Defines `WorkloadProfile` as a cluster-scoped custom resource, allowing operators to decalaratively define workload types (e.g.: `cpu-intensive`, `critical-service`) and the associated `eviction-priority` to specify the criticality of the service and the priority of eviction
- Performance-aware Rebalancing: Detects degraded nodes based on an annotation (`kub-balance.io/degraded-io "true"`), which serves as a placeholder for integration with real-time metrics in a production environment
- Eviction Logic:
    - QoS (Quality of Service) Class Prioritization: Pods are first sorted for eviction based on their K8s QoS class: `BestEffort` > `Burstable` > `Guaranteed`
    - CRD-based Prioritization: Within the same QoS class, pods are further prioritized using their `evictionPriority` field from their `WorkloadProfile` CR
    - PDB (Pod Disruption Budget) Awareness: Respect `PodDisruptionBudget` resources, ensuring that application availability is not compromised during rebalancing.
- Owner-based Cooldown: The controller applies a cooldown annotation (`kube-balance.io/eviction-cooldown-until`) to the owning controller of an evicted pod, preenting the immediate eviction of the subsequent pods from that same workload.

## Getting Started 

- Prerequisites
    - Go 1.20+
    - Docker
    - kubectl
    - Kind or Minikube (to run local clusters)
    - make
    - controller-gen (to generate boilerplate code for the utility "Deep Copy" functions)

## For Local Development and Testing

- Clone the Repository
```bash
git clone https://github.com/lokeshllkumar/kube-balance.git
cd kube-balance
```
- Clean Up Environment: Ensure that that are no existing cluster and old build artifacts.
```bash

kind delete cluster # minikube delete
make clean
go clean -modcache
```

> We're using Kind for spinning up the local cluster, but you can also use Minikube

- Build and Push the Controller Image: Build and push the controller's image to a container registry. It's recommended to use your own personal Dockerhub registry (which you must specify in the `Makefile`), but you're free to stick with the existing the registry that's already given. 
```bash
kind create cluster --config config/cluster/kind_config.yaml # minkube start --nodes 2

go mod tidy

make generate

make docker-build

make push
```
- Deploy the Project to the Local Cluster: Deploy the controller, its RBAC rules, and the sample workload profiles to the cluster that we just created.
```bash
make deploy
```
- Trigger Rebalancing And Observe Behaviour
```bash
make install-profiles

make test-apps
```
- Open 2 terminal windows:
    - Window 1 (Controller Logs)
```bash
kubectl get pods -n kube-system -l control-plane=controller-manager
```
    - Window 2 (K8s Events)
```bash
kubectl get events -w --sort-by='.lastTimestamp'
```
- Simulate Degradation: Annotate a node where your sample test apps are running.
```bash
make annotate-node NODE-NAME=<node-name>
```
You should now be able to see which pods are being considered for eviction. You should alos see pods being moved to a different node based on their priorities.
- Clean Up: Remove all resources from the cluster and delete the cluster as well.
```bash
make delete test-apps
make undeploy
make unannotate-node NODE_NAME=<node-name>

kind delete cluster # minikube delete
```

## In Your Own Cluster

The process is vastly similar to that for local deployment and testing, with 2 critical changes: defining how the image is hosted and pulled for the image and triggering the rebalancing logic.
- Image Hosting and Pulling
    - Pick a Registry: Opt for Dockerhub if you're looking to use a public registry or a cloud-based for registry like Amazon ECR and Google Artifact Registry.
    - Configure the Makefile: The `REGISTRY` variable must be set to the remote registry's address.
- Triggering the Rebalancing Logic
    - Monitoring Performance Metrics: Use a monitoring solution like Prometheus and configure it to the scrape metrics from your cluster nodes, such as disk I/O wait times or network packet loss.
    - Define Alerting Rules: Create alerting rules in Prometheus that fire when a node's performance metric exceeds a predefined threshold.
    - Automate Annotation: You could consider using a separate controller that watches for these Prometheus alerts. When an alert fires, the controller would automatically patch the unhealthy node with the `kube-balance.io/degraded-io: "true"` annotation.
    - KubeBalance Reacts: The KubeBalance controller, which is watching for the degradation annotation, will the detect the degraded node and begin the rebalancing process.

## Design

Some of the key componenets are briefly discussed as follows:
- Controller Runtime: KubeBalance is built on the `controller-runtime` framework, which provides a standard way to implement K8s controllers with features like reconciliation loops, leader election, and a shared cache.
- CRs (Custom Resources): The `WorkloadProfile` CRD defines a new API type, allowing a user-friendly, declarative way ot configure workload behaviour.
- RBAC (Role-based Access Control): The controller operates with a `ServiceAccount` and a `ClusterRole` that grant it specific, minimal permissions to interact with the API, ensuring a posture that is secure by default.
- Eviction API: Pods are rebalanced using a soft eviction, a mechansim that allows the owning controller to recreate the pod,  gracefully.