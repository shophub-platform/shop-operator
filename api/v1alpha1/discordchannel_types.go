package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NotificationType defines the category of notifications sent to a channel.
// +kubebuilder:validation:Enum=orders;alerts;all
type NotificationType string

const (
	NotificationOrders NotificationType = "orders"
	NotificationAlerts NotificationType = "alerts"
	NotificationAll    NotificationType = "all"
)

// DiscordChannelSpec defines the desired state of DiscordChannel.
type DiscordChannelSpec struct {
	// GuildID is the Discord server (guild) identifier.
	// +kubebuilder:validation:MinLength=1
	GuildID string `json:"guildID"`

	// ChannelName is the name of the channel to create or use.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	ChannelName string `json:"channelName"`

	// NotificationType selects which notifications are routed to the channel.
	// +kubebuilder:default=all
	NotificationType NotificationType `json:"notificationType,omitempty"`
}

// DiscordChannelPhase represents the lifecycle phase of a DiscordChannel.
// +kubebuilder:validation:Enum=Pending;Creating;Ready;Failed
type DiscordChannelPhase string

const (
	DiscordPhasePending  DiscordChannelPhase = "Pending"
	DiscordPhaseCreating DiscordChannelPhase = "Creating"
	DiscordPhaseReady    DiscordChannelPhase = "Ready"
	DiscordPhaseFailed   DiscordChannelPhase = "Failed"
)

// DiscordChannelStatus defines the observed state of DiscordChannel.
type DiscordChannelStatus struct {
	// WebhookURL is the webhook endpoint used to post notifications.
	// +optional
	WebhookURL string `json:"webhookURL,omitempty"`

	// Phase is the current lifecycle phase of the channel.
	// +optional
	Phase DiscordChannelPhase `json:"phase,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=dchan
// +kubebuilder:printcolumn:name="Guild",type=string,JSONPath=`.spec.guildID`
// +kubebuilder:printcolumn:name="Channel",type=string,JSONPath=`.spec.channelName`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.notificationType`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DiscordChannel is the Schema for the discordchannels API.
type DiscordChannel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DiscordChannelSpec   `json:"spec,omitempty"`
	Status DiscordChannelStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DiscordChannelList contains a list of DiscordChannel.
type DiscordChannelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DiscordChannel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DiscordChannel{}, &DiscordChannelList{})
}
