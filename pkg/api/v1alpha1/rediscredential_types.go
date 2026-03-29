package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RedisCredentialPhase represents the current lifecycle phase of a RedisCredential.
// +kubebuilder:validation:Enum=Pending;Ready;Failed
type RedisCredentialPhase string

const (
	// RedisCredentialPhasePending means the credential is being provisioned.
	RedisCredentialPhasePending RedisCredentialPhase = "Pending"
	// RedisCredentialPhaseReady means the credential and its Secret have been created.
	RedisCredentialPhaseReady RedisCredentialPhase = "Ready"
	// RedisCredentialPhaseFailed means the credential could not be provisioned.
	RedisCredentialPhaseFailed RedisCredentialPhase = "Failed"
)

// RedisACLCategory represents a Redis ACL category that can be granted to a user.
// +kubebuilder:validation:Enum=read;write;set;sortedset;list;hash;string;bitmap;hyperloglog;geo;stream;pubsub;admin;fast;slow;blocking;dangerous;connection;transaction;scripting;keyspace;all
type RedisACLCategory string

const (
	RedisACLCategoryRead        RedisACLCategory = "read"
	RedisACLCategoryWrite       RedisACLCategory = "write"
	RedisACLCategorySet         RedisACLCategory = "set"
	RedisACLCategorySortedSet   RedisACLCategory = "sortedset"
	RedisACLCategoryList        RedisACLCategory = "list"
	RedisACLCategoryHash        RedisACLCategory = "hash"
	RedisACLCategoryString      RedisACLCategory = "string"
	RedisACLCategoryBitmap      RedisACLCategory = "bitmap"
	RedisACLCategoryHyperLogLog RedisACLCategory = "hyperloglog"
	RedisACLCategoryGeo         RedisACLCategory = "geo"
	RedisACLCategoryStream      RedisACLCategory = "stream"
	RedisACLCategoryPubSub      RedisACLCategory = "pubsub"
	RedisACLCategoryAdmin       RedisACLCategory = "admin"
	RedisACLCategoryFast        RedisACLCategory = "fast"
	RedisACLCategorySlow        RedisACLCategory = "slow"
	RedisACLCategoryBlocking    RedisACLCategory = "blocking"
	RedisACLCategoryDangerous   RedisACLCategory = "dangerous"
	RedisACLCategoryConnection  RedisACLCategory = "connection"
	RedisACLCategoryTransaction RedisACLCategory = "transaction"
	RedisACLCategoryScripting   RedisACLCategory = "scripting"
	RedisACLCategoryKeyspace    RedisACLCategory = "keyspace"
	RedisACLCategoryAll         RedisACLCategory = "all"
)

// RedisCredentialSpec defines the desired state of RedisCredential.
type RedisCredentialSpec struct {
	// DatabaseRef is the name of the RedisDatabase resource in the same namespace
	// that this credential targets.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DatabaseRef string `json:"databaseRef"`

	// Username is the Redis ACL user to create inside the target instance.
	// Must be 1–63 characters.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Username string `json:"username"`

	// SecretName is the name of the Kubernetes Secret that will be created (or
	// updated) with the generated credentials. The Secret is written in the same
	// namespace as the RedisCredential.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	SecretName string `json:"secretName"`

	// KeyPatterns is the list of Redis key patterns the user can access (e.g. "user:*", "cache:*").
	// +optional
	KeyPatterns []string `json:"keyPatterns,omitempty"`

	// ACLCategories is the list of Redis ACL categories to grant to the user.
	// +optional
	ACLCategories []RedisACLCategory `json:"aclCategories,omitempty"`

	// Commands is the list of individual Redis commands to allow for the user.
	// +optional
	Commands []string `json:"commands,omitempty"`
}

// RedisCredentialStatus defines the observed state of RedisCredential.
type RedisCredentialStatus struct {
	// Phase is the current lifecycle phase of the credential.
	// +kubebuilder:default=Pending
	Phase RedisCredentialPhase `json:"phase,omitempty"`

	// Conditions contains detailed status conditions for the RedisCredential.
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
// +kubebuilder:resource:scope=Namespaced,shortName=rcred,categories=games-hub
// +kubebuilder:printcolumn:name="Database",type=string,JSONPath=`.spec.databaseRef`
// +kubebuilder:printcolumn:name="Username",type=string,JSONPath=`.spec.username`
// +kubebuilder:printcolumn:name="Secret",type=string,JSONPath=`.spec.secretName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RedisCredential is the Schema for the rediscredentials API.
// It defines a Redis ACL user whose credentials are stored in a Kubernetes Secret.
type RedisCredential struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RedisCredentialSpec   `json:"spec,omitempty"`
	Status RedisCredentialStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RedisCredentialList contains a list of RedisCredential.
type RedisCredentialList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RedisCredential `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RedisCredential{}, &RedisCredentialList{})
}
