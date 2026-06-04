package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NetworkType defines the blockchain network for a Wallet.
// +kubebuilder:validation:Enum=sepolia;mainnet
type NetworkType string

const (
	NetworkSepolia NetworkType = "sepolia"
	NetworkMainnet NetworkType = "mainnet"
)

// WalletSpec defines the desired state of Wallet.
type WalletSpec struct {
	// Network is the blockchain network the wallet operates on.
	// +kubebuilder:default=sepolia
	Network NetworkType `json:"network,omitempty"`

	// Address is the wallet address. If empty, the controller generates a new
	// account and records the resulting address in the status.
	// +optional
	// +kubebuilder:validation:Pattern=`^(0x[a-fA-F0-9]{40})?$`
	Address string `json:"address,omitempty"`
}

// WalletPhase represents the lifecycle phase of a Wallet.
// +kubebuilder:validation:Enum=Pending;Generating;Ready;Failed
type WalletPhase string

const (
	WalletPhasePending    WalletPhase = "Pending"
	WalletPhaseGenerating WalletPhase = "Generating"
	WalletPhaseReady      WalletPhase = "Ready"
	WalletPhaseFailed     WalletPhase = "Failed"
)

// WalletStatus defines the observed state of Wallet.
type WalletStatus struct {
	// Address is the resolved (provided or generated) wallet address.
	// +optional
	Address string `json:"address,omitempty"`

	// EncryptedPrivateKeyRef references a Secret holding the encrypted private key.
	// +optional
	EncryptedPrivateKeyRef *corev1.SecretKeySelector `json:"encryptedPrivateKeyRef,omitempty"`

	// Phase is the current lifecycle phase of the wallet.
	// +optional
	Phase WalletPhase `json:"phase,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=wlt
// +kubebuilder:printcolumn:name="Network",type=string,JSONPath=`.spec.network`
// +kubebuilder:printcolumn:name="Address",type=string,JSONPath=`.status.address`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Wallet is the Schema for the wallets API.
type Wallet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WalletSpec   `json:"spec,omitempty"`
	Status WalletStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WalletList contains a list of Wallet.
type WalletList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Wallet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Wallet{}, &WalletList{})
}
