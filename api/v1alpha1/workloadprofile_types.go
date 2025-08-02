package v1alpha1

import (
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var SchemeGroupVersion = schema.GroupVersion{
	Group:   "kube-balance.io",
	Version: "v1alpha1",
}

// defines the desired state of WorloadProfile
type WorkloadProfileSpec struct {
	CPURequests      string `json:"cpuRequests,omitempty"`
	MemoryRequests   string `json:"memoryRequests,omitempty"`
	EvictionPriority int    `json:"evictionPriority"`
}

// defines the observed state of WorkloadProfile
type WorkloadProfileStatus struct {
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=workloadprofiles,scope=Cluster,singular=workloadprofile
// +kubebuilder:printcolumn:name="CPU Requests",type="string",JSONPath=".spec.cpuRequests",description="Recommended CPU requests"
// +kubebuilder:printcolumn:name="Memory Requests",type="string",JSONPath=".spec.memoryRequests",description="Recommended memory requests"
// +kubebuilder:printcolumn:name="Eviction Priority",type="integer",JSONPath=".spec.evictionPriority",description="Eviction priority for the workload profile"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// schema for the API
type WorkloadProfile struct {
	meta.TypeMeta   `json:",inline"`
	meta.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkloadProfileSpec   `json:"spec,omitempty"`
	Status WorkloadProfileStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// list of several WorkloadProfile
type WorkloadProfileList struct {
	meta.TypeMeta `json:",inline"`
	meta.ListMeta `json:"metadata,omitempty"`
	Items         []WorkloadProfile `json:"items"`
}

var SchemeBuilder = &scheme.Builder{
	GroupVersion: SchemeGroupVersion,
}

func init() {
	SchemeBuilder.Register(&WorkloadProfile{}, &WorkloadProfileList{})
}
