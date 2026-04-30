package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MigrationSetPhase represents the current lifecycle phase of a PostgresMigrationSet.
// +kubebuilder:validation:Enum=Pending;Running;Ready;Failed
type MigrationSetPhase string

const (
	// MigrationSetPhasePending means the operator is preparing to launch a Job
	// (resolving the artifact reference, waiting for an in-flight Job, or paused).
	MigrationSetPhasePending MigrationSetPhase = "Pending"
	// MigrationSetPhaseRunning means a migration Job is currently executing.
	MigrationSetPhaseRunning MigrationSetPhase = "Running"
	// MigrationSetPhaseReady means the most recent Job for the desired
	// (artifact, targetRevision) pair completed successfully.
	MigrationSetPhaseReady MigrationSetPhase = "Ready"
	// MigrationSetPhaseFailed means the most recent Job failed.
	MigrationSetPhaseFailed MigrationSetPhase = "Failed"
)

// PostgresMigrationSetSpec defines the desired state of a PostgresMigrationSet.
type PostgresMigrationSetSpec struct {
	// DatabaseRef is the name of the PostgresDatabase resource in the same
	// namespace whose instance hosts the logical database to migrate.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DatabaseRef string `json:"databaseRef"`

	// Database is the logical PostgreSQL database name to apply migrations
	// against. The database is created on demand by the operator if it does
	// not already exist.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Database string `json:"database"`

	// Artifact is an OCI registry reference (e.g. registry.example.com/migrations:v3
	// or registry.example.com/migrations@sha256:…) to an ORAS artifact whose
	// single layer is a tar+gzip of the migration SQL files.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Artifact string `json:"artifact"`

	// TargetRevision is the migration ID to apply or roll back to. Must equal
	// the numeric prefix of one of the SQL files in the artifact, parsed as
	// an integer (so `0001-init-apply.sql` matches targetRevision: 1).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	TargetRevision int64 `json:"targetRevision"`

	// JobTTL is how long completed migration Jobs are retained before the
	// operator deletes them. Defaults to 1h when unset.
	// +optional
	JobTTL *metav1.Duration `json:"jobTTL,omitempty"`

	// Paused, when true, prevents the operator from spawning new migration
	// Jobs. In-flight Jobs are not interrupted.
	// +optional
	Paused bool `json:"paused,omitempty"`
}

// PostgresMigrationSetStatus defines the observed state of a PostgresMigrationSet.
type PostgresMigrationSetStatus struct {
	// Phase is the current lifecycle phase of the migration set.
	// +kubebuilder:default=Pending
	Phase MigrationSetPhase `json:"phase,omitempty"`

	// Conditions contains detailed status conditions for the PostgresMigrationSet.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the .metadata.generation observed by the
	// reconciler when it last updated this status.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// CurrentRevision is the migration ID most recently applied successfully.
	// +optional
	CurrentRevision *int64 `json:"currentRevision,omitempty"`

	// ObservedArtifact is the digest-pinned reference (repository@sha256:…)
	// the operator most recently resolved spec.artifact to.
	// +optional
	ObservedArtifact string `json:"observedArtifact,omitempty"`

	// ActiveJob is the name of the Job currently associated with this
	// migration set, if any.
	// +optional
	ActiveJob string `json:"activeJob,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pgms,categories=games-hub
// +kubebuilder:printcolumn:name="Database",type=string,JSONPath=`.spec.databaseRef`
// +kubebuilder:printcolumn:name="LogicalDB",type=string,JSONPath=`.spec.database`
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetRevision`
// +kubebuilder:printcolumn:name="Current",type=string,JSONPath=`.status.currentRevision`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PostgresMigrationSet is the Schema for the postgresmigrationsets API.
// It declares a set of SQL migrations distributed as an ORAS artifact and the
// target revision to converge a logical PostgreSQL database to.
type PostgresMigrationSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PostgresMigrationSetSpec   `json:"spec,omitempty"`
	Status PostgresMigrationSetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PostgresMigrationSetList contains a list of PostgresMigrationSet.
type PostgresMigrationSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresMigrationSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgresMigrationSet{}, &PostgresMigrationSetList{})
}
