package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NatsClusterPhase represents the current lifecycle phase of a NatsCluster.
// +kubebuilder:validation:Enum=Pending;Ready;Failed
type NatsClusterPhase string

const (
	// NatsClusterPhasePending means the cluster is being provisioned.
	NatsClusterPhasePending NatsClusterPhase = "Pending"
	// NatsClusterPhaseReady means the cluster is running and accepting connections.
	NatsClusterPhaseReady NatsClusterPhase = "Ready"
	// NatsClusterPhaseFailed means the cluster provisioning or reconciliation failed.
	NatsClusterPhaseFailed NatsClusterPhase = "Failed"
)

// NatsJetStreamConfig configures JetStream persistence for a NatsCluster.
// When present, a PersistentVolume is provisioned and JetStream is enabled.
type NatsJetStreamConfig struct {
	// StorageSize is the size of the PersistentVolume requested for JetStream storage
	// (e.g. "1Gi", "10Gi").
	// +kubebuilder:validation:Required
	StorageSize resource.Quantity `json:"storageSize"`
}

// NatsClusterSpec defines the desired state of NatsCluster.
type NatsClusterSpec struct {
	// NatsVersion is the NATS server version to deploy (e.g. "2.10").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	NatsVersion string `json:"natsVersion"`

	// JetStream enables JetStream persistence. When set, a PersistentVolume is
	// provisioned for storage. When omitted, JetStream is disabled.
	// +optional
	JetStream *NatsJetStreamConfig `json:"jetStream,omitempty"`
}

// NatsClusterStatus defines the observed state of NatsCluster.
type NatsClusterStatus struct {
	// Phase is the current lifecycle phase of the NATS cluster.
	// +kubebuilder:default=Pending
	Phase NatsClusterPhase `json:"phase,omitempty"`

	// Conditions contains detailed status conditions for the NatsCluster.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=nats,categories=games-hub
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.natsVersion`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// NatsCluster is the Schema for the natsclusters API.
// It represents a single NATS server instance managed by the db-operator.
type NatsCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NatsClusterSpec   `json:"spec,omitempty"`
	Status NatsClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NatsClusterList contains a list of NatsCluster.
type NatsClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NatsCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NatsCluster{}, &NatsClusterList{})
}
