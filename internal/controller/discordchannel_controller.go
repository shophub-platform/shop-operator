package controller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	shopv1alpha1 "github.com/shophub-platform/shop-operator/api/v1alpha1"
)

// DiscordChannelReconciler reconciles a DiscordChannel object.
type DiscordChannelReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=shop.shophub.io,resources=discordchannels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=shop.shophub.io,resources=discordchannels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=shop.shophub.io,resources=discordchannels/finalizers,verbs=update

// Reconcile is the F1 skeleton: it only logs detected changes.
func (r *DiscordChannelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var ch shopv1alpha1.DiscordChannel
	if err := r.Get(ctx, req.NamespacedName, &ch); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("DiscordChannel deleted", "discordchannel", req.NamespacedName.String())
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch DiscordChannel")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info("Reconciling discordchannel",
		"discordchannel", ch.Name,
		"namespace", ch.Namespace,
		"guildID", ch.Spec.GuildID,
		"channelName", ch.Spec.ChannelName,
		"notificationType", ch.Spec.NotificationType,
		"generation", ch.Generation,
	)

	// F1: webhook creation not implemented yet.
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DiscordChannelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&shopv1alpha1.DiscordChannel{}).
		Named("discordchannel").
		Complete(r)
}
