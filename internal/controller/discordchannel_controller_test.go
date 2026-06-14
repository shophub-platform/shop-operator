package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	shopv1alpha1 "github.com/shophub-platform/shop-operator/api/v1alpha1"
)

// discordMock serves the subset of the Discord API the reconciler uses. If
// deleted is non-nil it is set true when a channel DELETE is received.
func discordMock(t *testing.T, deleted *bool) *httptest.Server {
	t.Helper()
	h := func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/channels"):
			_, _ = w.Write([]byte(`{"id":"chan-1"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/webhooks"):
			_, _ = w.Write([]byte(`{"id":"wh-1","token":"tok-1"}`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[]`)) // no existing channels/webhooks
		case r.Method == http.MethodDelete:
			if deleted != nil {
				*deleted = true
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}
	return httptest.NewServer(http.HandlerFunc(h))
}

func botTokenSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: objMeta(botTokenSecretName, "default"),
		Data:       map[string][]byte{"token": []byte("test-token")},
	}
}

func TestDiscordReconcile_CreatesChannelAndWebhook(t *testing.T) {
	srv := discordMock(t, nil)
	defer srv.Close()
	t.Setenv("DISCORD_API_BASE", srv.URL)
	t.Setenv("OPERATOR_NAMESPACE", "default")
	ctx := context.Background()
	s := newScheme(t)

	ch := &shopv1alpha1.DiscordChannel{
		ObjectMeta: objMeta("notif", "default"),
		Spec: shopv1alpha1.DiscordChannelSpec{
			GuildID: "g1", ChannelName: "orders", NotificationType: shopv1alpha1.NotificationAll,
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&shopv1alpha1.DiscordChannel{}).
		WithObjects(botTokenSecret(), ch).Build()
	r := &DiscordChannelReconciler{Client: cl, Scheme: s}

	reconcileN(t, r, "notif", "default", 3)

	var got shopv1alpha1.DiscordChannel
	if err := cl.Get(ctx, types.NamespacedName{Name: "notif", Namespace: "default"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != shopv1alpha1.DiscordPhaseReady {
		t.Fatalf("phase = %q, want Ready", got.Status.Phase)
	}
	if got.Status.ChannelID != "chan-1" || got.Status.WebhookID != "wh-1" {
		t.Errorf("status ids = %+v", got.Status)
	}
	var sec corev1.Secret
	if err := cl.Get(ctx, types.NamespacedName{Name: webhookSecretName(&got), Namespace: "default"}, &sec); err != nil {
		t.Errorf("webhook secret missing: %v", err)
	}
}

func TestDiscordReconcile_DeleteCleansChannel(t *testing.T) {
	deleted := false
	srv := discordMock(t, &deleted)
	defer srv.Close()
	t.Setenv("DISCORD_API_BASE", srv.URL)
	t.Setenv("OPERATOR_NAMESPACE", "default")
	ctx := context.Background()
	s := newScheme(t)

	ch := &shopv1alpha1.DiscordChannel{
		ObjectMeta: objMeta("notif2", "default"),
		Spec: shopv1alpha1.DiscordChannelSpec{
			GuildID: "g1", ChannelName: "orders", NotificationType: shopv1alpha1.NotificationAll,
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&shopv1alpha1.DiscordChannel{}).
		WithObjects(botTokenSecret(), ch).Build()
	r := &DiscordChannelReconciler{Client: cl, Scheme: s}

	reconcileN(t, r, "notif2", "default", 3)

	var got shopv1alpha1.DiscordChannel
	if err := cl.Get(ctx, types.NamespacedName{Name: "notif2", Namespace: "default"}, &got); err != nil {
		t.Fatal(err)
	}
	if err := cl.Delete(ctx, &got); err != nil {
		t.Fatal(err)
	}
	reconcileN(t, r, "notif2", "default", 1) // finalizer cleanup

	if !deleted {
		t.Error("DeleteChannel was not called during cleanup")
	}
	var sec corev1.Secret
	if err := cl.Get(ctx, types.NamespacedName{Name: webhookSecretName(&got), Namespace: "default"}, &sec); err == nil {
		t.Error("webhook secret should be deleted during cleanup")
	}
}
