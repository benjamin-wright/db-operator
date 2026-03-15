package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DatabasePhase represents the current lifecycle phase of a PostgresDatabase.
// +kubebuilder:validation:Enum=Pending;Ready;Failed
type DatabasePhase string

const (
	// DatabasePhasePending means the database is being provisioned.
	DatabasePhasePending DatabasePhase = "Pending"
	// DatabasePhaseReady means the database is running and accepting connections.
	DatabasePhaseReady DatabasePhase = "Ready"
	// DatabasePhaseFailed means the database provisioning or reconciliation failed.
	DatabasePhaseFailed DatabasePhase = "Failed"
)

// PostgresDatabaseSpec defines the desired state of PostgresDatabase.
type PostgresDatabaseSpec struct {
	// DatabaseName is the name of the PostgreSQL database to create inside the instance.
	// Must be 1–63 characters, matching PostgreSQL identifier rules.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	DatabaseName string `json:"databaseName"`

	// PostgresVersion is the major version of PostgreSQL to deploy (e.g. "16").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum="14";"15";"16";"17"
	PostgresVersion string `json:"postgresVersion"`

	// StorageSize is the size of the PersistentVolume requested for this instance
	// (e.g. "1Gi", "10Gi").
	// +kubebuilder:validation:Required
	StorageSize resource.Quantity `json:"storageSize"`
}

// PostgresDatabaseStatus defines the observed state of PostgresDatabase.
type PostgresDatabaseStatus struct {
	// Phase is the current lifecycle phase of the database instance.
	// +kubebuilder:default=Pending
	Phase DatabasePhase `json:"phase,omitempty"`

	// SecretName is the name of the Kubernetes Secret containing the admin
	// credentials (username and password) for this PostgreSQL instance.
	// +optional
	SecretName string `json:"secretName,omitempty"`

	// Conditions contains detailed status conditions for the PostgresDatabase.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pgdb,categories=games-hub
// +kubebuilder:printcolumn:name="Database",type=string,JSONPath=`.spec.databaseName`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.postgresVersion`
// +kubebuilder:printcolumn:name="Storage",type=string,JSONPath=`.spec.storageSize`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PostgresDatabase is the Schema for the postgresdatabases API.
// It represents a self-contained PostgreSQL instance managed by the db-operator.
type PostgresDatabase struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PostgresDatabaseSpec   `json:"spec,omitempty"`
	Status PostgresDatabaseStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PostgresDatabaseList contains a list of PostgresDatabase.
type PostgresDatabaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresDatabase `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgresDatabase{}, &PostgresDatabaseList{})
}
