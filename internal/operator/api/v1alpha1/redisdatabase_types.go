package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RedisDatabasePhase represents the current lifecycle phase of a RedisDatabase.
// +kubebuilder:validation:Enum=Pending;Ready;Failed
type RedisDatabasePhase string

const (
	// RedisDatabasePhasePending means the database is being provisioned.
	RedisDatabasePhasePending RedisDatabasePhase = "Pending"
	// RedisDatabasePhaseReady means the database is running and accepting connections.
	RedisDatabasePhaseReady RedisDatabasePhase = "Ready"
	// RedisDatabasePhaseFailed means the database provisioning or reconciliation failed.
	RedisDatabasePhaseFailed RedisDatabasePhase = "Failed"
)

// RedisDatabaseSpec defines the desired state of RedisDatabase.
type RedisDatabaseSpec struct {
	// StorageSize is the size of the PersistentVolume requested for this instance
	// (e.g. "1Gi", "10Gi").
	// +kubebuilder:validation:Required
	StorageSize resource.Quantity `json:"storageSize"`
}

// RedisDatabaseStatus defines the observed state of RedisDatabase.
type RedisDatabaseStatus struct {
	// Phase is the current lifecycle phase of the database instance.
	// +kubebuilder:default=Pending
	Phase RedisDatabasePhase `json:"phase,omitempty"`

	// SecretName is the name of the Kubernetes Secret containing the admin
	// credentials (password) for this Redis instance.
	// +optional
	SecretName string `json:"secretName,omitempty"`

	// Conditions contains detailed status conditions for the RedisDatabase.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=rdb,categories=games-hub
// +kubebuilder:printcolumn:name="Storage",type=string,JSONPath=`.spec.storageSize`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RedisDatabase is the Schema for the redisdatabases API.
// It represents a self-contained Redis 8 instance managed by the db-operator.
type RedisDatabase struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RedisDatabaseSpec   `json:"spec,omitempty"`
	Status RedisDatabaseStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RedisDatabaseList contains a list of RedisDatabase.
type RedisDatabaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RedisDatabase `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RedisDatabase{}, &RedisDatabaseList{})
}
