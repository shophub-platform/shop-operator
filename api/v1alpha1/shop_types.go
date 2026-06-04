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

// ShopStatus defines the observed state of Shop.
type ShopStatus struct {
	// Phase is the current lifecycle phase of the shop.
	// +optional
	Phase ShopPhase `json:"phase,omitempty"`

	// ReadyReplicas is the number of ready replicas.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

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
