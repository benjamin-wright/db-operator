package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CredentialPhase represents the current lifecycle phase of a PostgresCredential.
// +kubebuilder:validation:Enum=Pending;Ready;Failed
type CredentialPhase string

const (
	// CredentialPhasePending means the credential is being provisioned.
	CredentialPhasePending CredentialPhase = "Pending"
	// CredentialPhaseReady means the credential and its Secret have been created.
	CredentialPhaseReady CredentialPhase = "Ready"
	// CredentialPhaseFailed means the credential could not be provisioned.
	CredentialPhaseFailed CredentialPhase = "Failed"
)

// DatabasePermission represents a PostgreSQL privilege that can be granted to a user.
// +kubebuilder:validation:Enum=SELECT;INSERT;UPDATE;DELETE;TRUNCATE;REFERENCES;TRIGGER;ALL
type DatabasePermission string

const (
	PermissionSelect     DatabasePermission = "SELECT"
	PermissionInsert     DatabasePermission = "INSERT"
	PermissionUpdate     DatabasePermission = "UPDATE"
	PermissionDelete     DatabasePermission = "DELETE"
	PermissionTruncate   DatabasePermission = "TRUNCATE"
	PermissionReferences DatabasePermission = "REFERENCES"
	PermissionTrigger    DatabasePermission = "TRIGGER"
	PermissionAll        DatabasePermission = "ALL"
)

// DatabasePermissionEntry maps a set of table-level privileges to one or more
// logical databases within the target PostgreSQL instance.
type DatabasePermissionEntry struct {
	// Databases is the list of PostgreSQL database names this entry applies to.
	// Each database will be created inside the target instance if it does not already exist.
	// +kubebuilder:validation:MinItems=1
	Databases []string `json:"databases"`

	// Tables is the list of table names within those databases that these privileges apply to.
	// If empty or omitted, the privileges apply to all tables in those databases.
	// +optional
	// +kubebuilder:validation:MinItems=1
	Tables []string `json:"tables,omitempty"`

	// Permissions is the set of table-level privileges to grant in those databases.
	// +kubebuilder:validation:MinItems=1
	Permissions []DatabasePermission `json:"permissions"`
}

// PostgresCredentialSpec defines the desired state of PostgresCredential.
// +kubebuilder:validation:XValidation:rule="!self.databaseOwner || size(self.permissions) > 0",message="databaseOwner: true requires at least one permissions entry"
type PostgresCredentialSpec struct {
	// DatabaseRef is the name of the PostgresDatabase resource in the same namespace
	// that this credential targets.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DatabaseRef string `json:"databaseRef"`

	// Username is the PostgreSQL role/user to create inside the target database.
	// Must be 1–63 characters.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Username string `json:"username"`

	// SecretName is the name of the Kubernetes Secret that will be created (or
	// updated) with the generated credentials. The Secret is written in the same
	// namespace as the PostgresCredential.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	SecretName string `json:"secretName"`

	// Permissions is the list of per-database privilege entries for this credential.
	// Each entry specifies one or more databases and the privileges to grant in them.
	// +optional
	Permissions []DatabasePermissionEntry `json:"permissions,omitempty"`

	// DatabaseOwner, when true, makes this credential the OWNER of every database listed
	// in spec.permissions[*].databases. The role is granted ALL privileges on the database
	// and on the public schema, enabling DDL operations.
	//
	// At most one credential per (databaseRef, database) may set databaseOwner: true.
	// A second credential setting databaseOwner: true against the same database is rejected
	// (status Failed, reason OwnerConflict).
	//
	// When other credentials are reconciled against an owner-managed database, the operator
	// also runs ALTER DEFAULT PRIVILEGES FOR ROLE <owner> so that tables created later by
	// the owner are auto-granted to those credentials.
	// +optional
	DatabaseOwner bool `json:"databaseOwner,omitempty"`
}

// PostgresCredentialStatus defines the observed state of PostgresCredential.
type PostgresCredentialStatus struct {
	// Phase is the current lifecycle phase of the credential.
	// +kubebuilder:default=Pending
	Phase CredentialPhase `json:"phase,omitempty"`

	// Conditions contains detailed status conditions for the PostgresCredential.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// SecretName is the name of the Kubernetes Secret that was created for this credential.
	// +optional
	SecretName string `json:"secretName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pgcred,categories=games-hub
// +kubebuilder:printcolumn:name="Database",type=string,JSONPath=`.spec.databaseRef`
// +kubebuilder:printcolumn:name="Username",type=string,JSONPath=`.spec.username`
// +kubebuilder:printcolumn:name="Secret",type=string,JSONPath=`.spec.secretName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PostgresCredential is the Schema for the postgrescredentials API.
// It defines a PostgreSQL user whose credentials are stored in a Kubernetes Secret.
type PostgresCredential struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PostgresCredentialSpec   `json:"spec,omitempty"`
	Status PostgresCredentialStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PostgresCredentialList contains a list of PostgresCredential.
type PostgresCredentialList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresCredential `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgresCredential{}, &PostgresCredentialList{})
}
