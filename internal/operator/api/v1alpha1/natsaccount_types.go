package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NatsAccountPhase represents the current lifecycle phase of a NatsAccount.
// +kubebuilder:validation:Enum=Pending;Ready;Failed
type NatsAccountPhase string

const (
	// NatsAccountPhasePending means the account is being provisioned.
	NatsAccountPhasePending NatsAccountPhase = "Pending"
	// NatsAccountPhaseReady means the account is active on its target cluster.
	NatsAccountPhaseReady NatsAccountPhase = "Ready"
	// NatsAccountPhaseFailed means the account could not be provisioned.
	NatsAccountPhaseFailed NatsAccountPhase = "Failed"
)

// NatsExportType distinguishes between stream exports and service (request/reply) exports.
// +kubebuilder:validation:Enum=stream;service
type NatsExportType string

const (
	// NatsExportTypeStream represents a one-way pub/sub stream export.
	NatsExportTypeStream NatsExportType = "stream"
	// NatsExportTypeService represents a request/reply service export.
	NatsExportTypeService NatsExportType = "service"
)

// NatsSubjectPermission defines allow and deny lists for a subject scope.
type NatsSubjectPermission struct {
	// Allow is the list of subjects permitted for this scope.
	// +optional
	Allow []string `json:"allow,omitempty"`

	// Deny is the list of subjects denied for this scope. Deny takes precedence over Allow.
	// +optional
	Deny []string `json:"deny,omitempty"`
}

// NatsUserPermissions defines the publish and subscribe permissions for a NATS user.
type NatsUserPermissions struct {
	// Publish defines the subjects the user may publish to.
	// +optional
	Publish *NatsSubjectPermission `json:"publish,omitempty"`

	// Subscribe defines the subjects the user may subscribe to.
	// +optional
	Subscribe *NatsSubjectPermission `json:"subscribe,omitempty"`
}

// NatsUser defines a user within a NATS account.
type NatsUser struct {
	// Username is the name of the NATS user.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Username string `json:"username"`

	// SecretName is the name of the Kubernetes Secret into which the operator
	// writes the generated credentials. The Secret is written in the same
	// namespace as the NatsAccount.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	SecretName string `json:"secretName"`

	// Permissions defines publish and subscribe permissions for this user.
	// When omitted, the user inherits account-level defaults.
	// +optional
	Permissions *NatsUserPermissions `json:"permissions,omitempty"`
}

// NatsExport defines a subject exported from this account to other accounts.
type NatsExport struct {
	// Subject is the subject or wildcard pattern exported from this account.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Subject string `json:"subject"`

	// Type is whether this export is a one-way stream or a request/reply service.
	// +kubebuilder:validation:Required
	Type NatsExportType `json:"type"`

	// TokenRequired indicates that importing accounts must supply an activation token.
	// When true, this is a private export.
	// +optional
	TokenRequired bool `json:"tokenRequired,omitempty"`
}

// NatsImport defines a subject imported into this account from another account.
type NatsImport struct {
	// Account is the name of the NatsAccount CR (in the same namespace) to import from.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Account string `json:"account"`

	// Subject is the subject or wildcard pattern in the source account to import.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Subject string `json:"subject"`

	// LocalSubject remaps the imported subject in the local account namespace.
	// When omitted, the remote subject is used without modification.
	// +optional
	LocalSubject string `json:"localSubject,omitempty"`

	// Type is whether this import is a one-way stream or a request/reply service.
	// +kubebuilder:validation:Required
	Type NatsExportType `json:"type"`
}

// NatsAccountSpec defines the desired state of NatsAccount.
type NatsAccountSpec struct {
	// ClusterRef is the name of the NatsCluster resource in the same namespace
	// that this account belongs to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ClusterRef string `json:"clusterRef"`

	// Users is the list of NATS users defined within this account. The operator
	// generates a password for each user and writes credentials to the named Secret.
	// +optional
	Users []NatsUser `json:"users,omitempty"`

	// Exports is the list of subjects this account exposes to other accounts.
	// +optional
	Exports []NatsExport `json:"exports,omitempty"`

	// Imports is the list of subjects this account brings in from other accounts.
	// +optional
	Imports []NatsImport `json:"imports,omitempty"`
}

// NatsAccountStatus defines the observed state of NatsAccount.
type NatsAccountStatus struct {
	// Phase is the current lifecycle phase of the account.
	// +kubebuilder:default=Pending
	Phase NatsAccountPhase `json:"phase,omitempty"`

	// Conditions contains detailed status conditions for the NatsAccount.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=natsacct,categories=games-hub
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// NatsAccount is the Schema for the natsaccounts API.
// Each CR represents one account within the referenced NatsCluster. Multiple
// accounts in a single cluster are created by deploying multiple NatsAccount CRs.
type NatsAccount struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NatsAccountSpec   `json:"spec,omitempty"`
	Status NatsAccountStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NatsAccountList contains a list of NatsAccount.
type NatsAccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NatsAccount `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NatsAccount{}, &NatsAccountList{})
}
