package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AvailabilityType defines the availability tier of a Shop.
// +kubebuilder:validation:Enum=standard;high
type AvailabilityType string

const (
	AvailabilityStandard AvailabilityType = "standard"
	AvailabilityHigh     AvailabilityType = "high"
)

// DatabaseType defines which database backend the Shop uses.
// +kubebuilder:validation:Enum=postgres;redis
type DatabaseType string

const (
	DatabasePostgres DatabaseType = "postgres"
	DatabaseRedis    DatabaseType = "redis"
)

// ShopSpec defines the desired state of Shop.
type ShopSpec struct {
	// Name is the human readable name of the shop.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// Availability controls how many replicas the shop runs.
	// standard -> 2 replicas, high -> 3 replicas.
	// +kubebuilder:default=standard
	Availability AvailabilityType `json:"availability,omitempty"`

	// WalletRef references the Wallet CR holding the shop payout account.
	WalletRef corev1.LocalObjectReference `json:"walletRef"`

	// DatabaseType selects the database backend for the shop.
	// +kubebuilder:default=postgres
	DatabaseType DatabaseType `json:"databaseType,omitempty"`

	// Image is the container image of the shop application.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Replicas is the computed number of replicas. It is derived from
	// Availability by the controller and should not normally be set by users.
	// +optional
	// +kubebuilder:validation:Minimum=0
	Replicas *int32 `json:"replicas,omitempty"`
}

// ShopPhase represents the lifecycle phase of a Shop.
// +kubebuilder:validation:Enum=Pending;Provisioning;Ready;Failed
type ShopPhase string

const (
	ShopPhasePending      ShopPhase = "Pending"
	ShopPhaseProvisioning ShopPhase = "Provisioning"
	ShopPhaseReady        ShopPhase = "Ready"
	ShopPhaseFailed       ShopPhase = "Failed"
)

// Condition types reported on a Shop's status.
const (
	// ConditionReady is True when the shop and all of its child resources are
	// provisioned and healthy.
	ConditionReady = "Ready"
	// ConditionDatabaseReady is True when the backing database (CNPG Cluster or
	// Redis CR) reports a healthy/ready state.
	ConditionDatabaseReady = "DatabaseReady"
	// ConditionDiscordReady is True when the referenced DiscordChannel is Ready.
	ConditionDiscordReady = "DiscordReady"
	// ConditionWalletReady is True when the referenced Wallet exists and is Ready.
	ConditionWalletReady = "WalletReady"
)

// ShopStatus defines the observed state of Shop.
type ShopStatus struct {
	// Phase is the current lifecycle phase of the shop.
	// +optional
	Phase ShopPhase `json:"phase,omitempty"`

	// ReadyReplicas is the number of ready replicas of the shop backend.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// ReplicaCount is the desired number of replicas derived from availability.
	// +optional
	ReplicaCount int32 `json:"replicaCount,omitempty"`

	// URL is the public ingress URL of the shop.
	// +optional
	URL string `json:"url,omitempty"`

	// ObservedGeneration is the .metadata.generation last processed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=shp
// +kubebuilder:printcolumn:name="Availability",type=string,JSONPath=`.spec.availability`
// +kubebuilder:printcolumn:name="Database",type=string,JSONPath=`.spec.databaseType`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.status.url`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Shop is the Schema for the shops API.
type Shop struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ShopSpec   `json:"spec,omitempty"`
	Status ShopStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ShopList contains a list of Shop.
type ShopList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Shop `json:"items"`
}

// ReplicasForAvailability returns the replica count for a given availability tier.
func ReplicasForAvailability(a AvailabilityType) int32 {
	switch a {
	case AvailabilityHigh:
		return 3
	default:
		// standard and any unset/unknown value default to 2.
		return 2
	}
}

func init() {
	SchemeBuilder.Register(&Shop{}, &ShopList{})
}
