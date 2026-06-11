package controller

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	shopv1alpha1 "github.com/shophub-platform/shop-operator/api/v1alpha1"
	"github.com/shophub-platform/shop-operator/internal/discord"
)

const (
	// discordFinalizer guards channel cleanup on deletion.
	discordFinalizer = "shop.shophub.io/discordchannel-cleanup"
	// botTokenSecretName is the Secret (in the operator namespace) holding the
	// Discord bot token.
	botTokenSecretName = "discord-bot-token"
	// webhookSecretPrefix + <name> is the Secret holding the webhook URL.
	webhookSecretPrefix = "discord-webhook-"
	// webhookSecretKey is the data key under which the webhook URL is stored.
	webhookSecretKey = "url"
)

// DiscordChannelReconciler reconciles a DiscordChannel object.
type DiscordChannelReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=shop.shophub.io,resources=discordchannels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=shop.shophub.io,resources=discordchannels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=shop.shophub.io,resources=discordchannels/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile implements the F5 (spec 10.2) DiscordChannel reconciliation logic:
// it creates a Discord text channel and webhook, stores the webhook URL in a
// Secret, and deletes the channel on cleanup via a finalizer.
func (r *DiscordChannelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var ch shopv1alpha1.DiscordChannel
	if err := r.Get(ctx, req.NamespacedName, &ch); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Deletion: run cleanup then drop the finalizer.
	if !ch.ObjectMeta.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&ch, discordFinalizer) {
			if err := r.cleanup(ctx, &ch); err != nil {
				logger.Error(err, "discord cleanup failed")
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&ch, discordFinalizer)
			if err := r.Update(ctx, &ch); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure the finalizer is present before creating external resources.
	if !controllerutil.ContainsFinalizer(&ch, discordFinalizer) {
		controllerutil.AddFinalizer(&ch, discordFinalizer)
		if err := r.Update(ctx, &ch); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Nothing to do if already provisioned.
	if ch.Status.Phase == shopv1alpha1.DiscordPhaseReady &&
		ch.Status.ChannelID != "" && ch.Status.WebhookURL != "" {
		return ctrl.Result{}, nil
	}

	// Read the bot token from the operator-namespace Secret.
	token, err := r.botToken(ctx)
	if err != nil {
		r.setReadyCondition(&ch, metav1.ConditionFalse, "TokenUnavailable", err.Error())
		ch.Status.Phase = shopv1alpha1.DiscordPhasePending
		if uerr := r.Status().Update(ctx, &ch); uerr != nil {
			return ctrl.Result{}, uerr
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	dc := discord.NewClient(token)

	// Step 1: resolve the text channel. Idempotent — reuse an existing channel
	// with the same name (or a previously recorded ID) instead of creating a
	// duplicate. We keep the ID in a local var so a status round-trip cannot
	// lose it mid-reconcile.
	channelID := ch.Status.ChannelID
	if channelID == "" {
		if id, found, err := dc.FindChannelByName(ctx, ch.Spec.GuildID, ch.Spec.ChannelName); err != nil {
			return r.fail(ctx, &ch, "ChannelLookupFailed", err)
		} else if found {
			channelID = id
		} else {
			id, err := dc.CreateChannel(ctx, ch.Spec.GuildID, ch.Spec.ChannelName)
			if err != nil {
				return r.fail(ctx, &ch, "ChannelCreateFailed", err)
			}
			channelID = id
		}
		ch.Status.ChannelID = channelID
		ch.Status.Phase = shopv1alpha1.DiscordPhaseCreating
		if err := r.Status().Update(ctx, &ch); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Step 2: resolve the webhook (reuse existing by name) and store its URL.
	webhookID := ch.Status.WebhookID
	webhookURL := ch.Status.WebhookURL
	if webhookURL == "" {
		if id, url, found, err := dc.FindWebhook(ctx, channelID, ch.Spec.ChannelName); err != nil {
			return r.fail(ctx, &ch, "WebhookLookupFailed", err)
		} else if found {
			webhookID, webhookURL = id, url
		} else {
			id, url, err := dc.CreateWebhook(ctx, channelID, ch.Spec.ChannelName)
			if err != nil {
				return r.fail(ctx, &ch, "WebhookCreateFailed", err)
			}
			webhookID, webhookURL = id, url
		}
		if err := r.storeWebhookSecret(ctx, &ch, webhookURL); err != nil {
			return r.fail(ctx, &ch, "WebhookSecretFailed", err)
		}
	}

	ch.Status.ChannelID = channelID
	ch.Status.WebhookID = webhookID
	ch.Status.WebhookURL = webhookURL
	ch.Status.Phase = shopv1alpha1.DiscordPhaseReady
	r.setReadyCondition(&ch, metav1.ConditionTrue, "ChannelReady", "discord channel and webhook ready")
	if err := r.Status().Update(ctx, &ch); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("DiscordChannel ready", "discordchannel", ch.Name, "channelID", channelID)
	return ctrl.Result{}, nil
}

// cleanup deletes the Discord channel and the webhook Secret.
func (r *DiscordChannelReconciler) cleanup(ctx context.Context, ch *shopv1alpha1.DiscordChannel) error {
	logger := log.FromContext(ctx)

	token, err := r.botToken(ctx)
	if err != nil {
		// Without a token we cannot call Discord; do not block deletion.
		logger.Info("bot token unavailable during cleanup; skipping channel delete", "error", err.Error())
	} else {
		dc := discord.NewClient(token)
		channelID := ch.Status.ChannelID
		if channelID == "" {
			// Status may have been lost; locate the channel by name.
			if id, found, ferr := dc.FindChannelByName(ctx, ch.Spec.GuildID, ch.Spec.ChannelName); ferr == nil && found {
				channelID = id
			}
		}
		if channelID != "" {
			if err := dc.DeleteChannel(ctx, channelID); err != nil {
				return err
			}
		}
	}

	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      webhookSecretName(ch),
		Namespace: ch.Namespace,
	}}
	if err := r.Delete(ctx, sec); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// storeWebhookSecret creates/updates the Secret discord-webhook-<name>.
func (r *DiscordChannelReconciler) storeWebhookSecret(ctx context.Context, ch *shopv1alpha1.DiscordChannel, url string) error {
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      webhookSecretName(ch),
		Namespace: ch.Namespace,
	}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sec, func() error {
		sec.Type = corev1.SecretTypeOpaque
		sec.StringData = map[string]string{webhookSecretKey: url}
		return controllerutil.SetControllerReference(ch, sec, r.Scheme)
	})
	return err
}

// botToken reads the Discord bot token from the operator-namespace Secret.
func (r *DiscordChannelReconciler) botToken(ctx context.Context) (string, error) {
	ns := operatorNamespace()
	var sec corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Name: botTokenSecretName, Namespace: ns}, &sec); err != nil {
		return "", fmt.Errorf("read %s/%s: %w", ns, botTokenSecretName, err)
	}
	for _, k := range []string{"token", "bot-token", "DISCORD_BOT_TOKEN"} {
		if v, ok := sec.Data[k]; ok && len(v) > 0 {
			return string(v), nil
		}
	}
	return "", fmt.Errorf("secret %s/%s has no token key (token|bot-token|DISCORD_BOT_TOKEN)", ns, botTokenSecretName)
}

func (r *DiscordChannelReconciler) fail(ctx context.Context, ch *shopv1alpha1.DiscordChannel, reason string, err error) (ctrl.Result, error) {
	ch.Status.Phase = shopv1alpha1.DiscordPhaseFailed
	r.setReadyCondition(ch, metav1.ConditionFalse, reason, err.Error())
	_ = r.Status().Update(ctx, ch)
	return ctrl.Result{}, err
}

func (r *DiscordChannelReconciler) setReadyCondition(ch *shopv1alpha1.DiscordChannel, status metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&ch.Status.Conditions, metav1.Condition{
		Type:               shopv1alpha1.ConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: ch.Generation,
	})
}

// webhookSecretName returns discord-webhook-<name>.
func webhookSecretName(ch *shopv1alpha1.DiscordChannel) string {
	return webhookSecretPrefix + ch.Name
}

// operatorNamespace resolves the namespace the operator runs in (where the
// discord-bot-token Secret lives).
func operatorNamespace() string {
	if v := os.Getenv("OPERATOR_NAMESPACE"); v != "" {
		return v
	}
	if v := os.Getenv("POD_NAMESPACE"); v != "" {
		return v
	}
	if b, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	return "default"
}

// SetupWithManager sets up the controller with the Manager.
func (r *DiscordChannelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&shopv1alpha1.DiscordChannel{}).
		Owns(&corev1.Secret{}).
		Named("discordchannel").
		Complete(r)
}
