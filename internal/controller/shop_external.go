package controller

import (
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	shopv1alpha1 "github.com/shophub-platform/shop-operator/api/v1alpha1"
)

// GroupVersionKinds of the external operator CRDs the Shop reconciler drives.
// They are handled as Unstructured so the operator does not depend on the
// external projects' Go modules.
var (
	cnpgClusterGVK = schema.GroupVersionKind{
		Group: "postgresql.cnpg.io", Version: "v1", Kind: "Cluster",
	}
	redisGVK = schema.GroupVersionKind{
		Group: "redis.redis.opstreelabs.in", Version: "v1beta2", Kind: "Redis",
	}
	serviceMonitorGVK = schema.GroupVersionKind{
		Group: "monitoring.coreos.com", Version: "v1", Kind: "ServiceMonitor",
	}
	podMonitorGVK = schema.GroupVersionKind{
		Group: "monitoring.coreos.com", Version: "v1", Kind: "PodMonitor",
	}
)

// Annotation / env overrides for the Discord guild the channel is created in.
const (
	discordGuildAnnotation = "shop.shophub.io/discord-guild-id"
	discordGuildEnv        = "DISCORD_GUILD_ID"
)

// dbInfo carries the resolved database coordinates used to build the ConfigMap
// and Secret for the shop application.
type dbInfo struct {
	Type       string
	Host       string
	Port       int32
	Ready      bool
	ConnString string
}

// ---------------------------------------------------------------------------
// Generic unstructured upsert / get.
// ---------------------------------------------------------------------------

func (r *ShopReconciler) upsertUnstructured(
	ctx context.Context,
	shop *shopv1alpha1.Shop,
	gvk schema.GroupVersionKind,
	name string,
	spec map[string]interface{},
	extraLabels map[string]string,
) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	u.SetName(name)
	u.SetNamespace(shop.Namespace)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, u, func() error {
		labels := labelsFor(shop, "")
		for k, v := range extraLabels {
			labels[k] = v
		}
		u.SetLabels(labels)
		if err := unstructured.SetNestedMap(u.Object, spec, "spec"); err != nil {
			return err
		}
		return controllerutil.SetControllerReference(shop, u, r.Scheme)
	})
	return err
}

func (r *ShopReconciler) getUnstructured(
	ctx context.Context, gvk schema.GroupVersionKind, name, namespace string,
) (*unstructured.Unstructured, error) {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, u); err != nil {
		return nil, err
	}
	return u, nil
}

// ---------------------------------------------------------------------------
// Step 4 — database provisioning (strict: block until Ready).
// ---------------------------------------------------------------------------

func (r *ShopReconciler) reconcileDatabase(
	ctx context.Context, shop *shopv1alpha1.Shop, replicas int32,
) (dbInfo, error) {
	switch shop.Spec.DatabaseType {
	case shopv1alpha1.DatabaseRedis:
		return r.reconcileRedis(ctx, shop)
	default:
		return r.reconcilePostgres(ctx, shop, replicas)
	}
}

func (r *ShopReconciler) reconcilePostgres(
	ctx context.Context, shop *shopv1alpha1.Shop, replicas int32,
) (dbInfo, error) {
	name := databaseName(shop)
	instances := int64(replicas)
	if instances < 1 {
		instances = 1
	}
	spec := map[string]interface{}{
		"instances": instances,
		"storage":   map[string]interface{}{"size": "1Gi"},
		"bootstrap": map[string]interface{}{
			"initdb": map[string]interface{}{
				"database": "shop",
				"owner":    "shop",
			},
		},
	}
	if err := r.upsertUnstructured(ctx, shop, cnpgClusterGVK, name, spec, nil); err != nil {
		return dbInfo{}, fmt.Errorf("apply CNPG Cluster: %w", err)
	}

	info := dbInfo{
		Type: "postgres",
		Host: name + "-rw", // CNPG read-write service
		Port: postgresPort,
	}

	cluster, err := r.getUnstructured(ctx, cnpgClusterGVK, name, shop.Namespace)
	if err != nil {
		return info, fmt.Errorf("get CNPG Cluster: %w", err)
	}
	phase, _, _ := unstructured.NestedString(cluster.Object, "status", "phase")
	info.Ready = phase == "Cluster in healthy state"

	// CNPG generates a Secret "<cluster>-app" with a ready-to-use connection URI.
	if info.Ready {
		var appSecret corev1.Secret
		if err := r.Get(ctx, types.NamespacedName{Name: name + "-app", Namespace: shop.Namespace}, &appSecret); err == nil {
			if uri, ok := appSecret.Data["uri"]; ok {
				info.ConnString = string(uri)
			}
		}
		if info.ConnString == "" {
			info.ConnString = fmt.Sprintf("postgres://%s:%d/shop?sslmode=disable", info.Host, info.Port)
		}
	}
	return info, nil
}

func (r *ShopReconciler) reconcileRedis(
	ctx context.Context, shop *shopv1alpha1.Shop,
) (dbInfo, error) {
	name := databaseName(shop)
	spec := map[string]interface{}{
		"kubernetesConfig": map[string]interface{}{
			"image":           "quay.io/opstree/redis:v7.0.12",
			"imagePullPolicy": "IfNotPresent",
		},
	}
	if err := r.upsertUnstructured(ctx, shop, redisGVK, name, spec, nil); err != nil {
		return dbInfo{}, fmt.Errorf("apply Redis: %w", err)
	}

	info := dbInfo{
		Type:       "redis",
		Host:       name,
		Port:       redisPort,
		ConnString: fmt.Sprintf("redis://%s:%d/0", name, redisPort),
	}

	redis, err := r.getUnstructured(ctx, redisGVK, name, shop.Namespace)
	if err != nil {
		return info, fmt.Errorf("get Redis: %w", err)
	}
	state, _, _ := unstructured.NestedString(redis.Object, "status", "state")
	info.Ready = state == "Ready"
	return info, nil
}

// ---------------------------------------------------------------------------
// Step 10 — Prometheus ServiceMonitor + PodMonitor.
// ---------------------------------------------------------------------------

func (r *ShopReconciler) reconcileMonitors(ctx context.Context, shop *shopv1alpha1.Shop) error {
	smSpec := map[string]interface{}{
		"selector": map[string]interface{}{
			"matchLabels": map[string]interface{}{
				"app.kubernetes.io/instance":  shop.Name,
				"app.kubernetes.io/component": "backend",
			},
		},
		"endpoints": []interface{}{
			map[string]interface{}{"port": "http", "path": "/metrics", "interval": "30s"},
		},
	}
	if err := r.upsertUnstructured(ctx, shop, serviceMonitorGVK, shop.Name, smSpec, nil); err != nil {
		return fmt.Errorf("apply ServiceMonitor: %w", err)
	}

	pmSpec := map[string]interface{}{
		"selector": map[string]interface{}{
			"matchLabels": map[string]interface{}{
				"app.kubernetes.io/instance": shop.Name,
			},
		},
		"podMetricsEndpoints": []interface{}{
			map[string]interface{}{"port": "http", "path": "/metrics", "interval": "30s"},
		},
	}
	if err := r.upsertUnstructured(ctx, shop, podMonitorGVK, shop.Name, pmSpec, nil); err != nil {
		return fmt.Errorf("apply PodMonitor: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Step 2 — wallet existence (and address resolution).
// ---------------------------------------------------------------------------

func (r *ShopReconciler) resolveWallet(
	ctx context.Context, shop *shopv1alpha1.Shop,
) (address string, found bool, err error) {
	var w shopv1alpha1.Wallet
	key := types.NamespacedName{Name: shop.Spec.WalletRef.Name, Namespace: shop.Namespace}
	if err := r.Get(ctx, key, &w); err != nil {
		if apierrors.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}
	address = w.Status.Address
	if address == "" {
		address = w.Spec.Address
	}
	return address, true, nil
}

// ---------------------------------------------------------------------------
// Step 3 — DiscordChannel reconcile.
//
// The DiscordChannel reconciler itself (spec 10.2) is implemented separately;
// until it sets Status.Phase=Ready this step is best-effort: the channel CR is
// created and its webhook URL consumed once available. Set REQUIRE_DISCORD=true
// to enforce the strict "block until Ready" gate from the specification.
// ---------------------------------------------------------------------------

func (r *ShopReconciler) reconcileDiscordChannel(
	ctx context.Context, shop *shopv1alpha1.Shop,
) (webhookURL string, ready bool, err error) {
	var ch shopv1alpha1.DiscordChannel
	key := types.NamespacedName{Name: discordChannelName(shop), Namespace: shop.Namespace}
	getErr := r.Get(ctx, key, &ch)
	if apierrors.IsNotFound(getErr) {
		guild := discordGuildID(shop)
		if guild == "" {
			// Cannot create a DiscordChannel without a guild ID; skip for now.
			return "", false, nil
		}
		ch = shopv1alpha1.DiscordChannel{
			ObjectMeta: metav1.ObjectMeta{
				Name:      discordChannelName(shop),
				Namespace: shop.Namespace,
				Labels:    labelsFor(shop, "discord"),
			},
			Spec: shopv1alpha1.DiscordChannelSpec{
				GuildID:          guild,
				ChannelName:      shop.Name,
				NotificationType: shopv1alpha1.NotificationAll,
			},
		}
		if err := controllerutil.SetControllerReference(shop, &ch, r.Scheme); err != nil {
			return "", false, err
		}
		if err := r.Create(ctx, &ch); err != nil {
			return "", false, fmt.Errorf("create DiscordChannel: %w", err)
		}
		return "", false, nil
	}
	if getErr != nil {
		return "", false, getErr
	}
	ready = ch.Status.Phase == shopv1alpha1.DiscordPhaseReady
	return ch.Status.WebhookURL, ready, nil
}

// requireDiscord reports whether the Discord readiness gate is enforced.
func requireDiscord() bool {
	return os.Getenv("REQUIRE_DISCORD") == "true"
}

// discordGuildID resolves the Discord guild ID from the Shop annotation or the
// operator environment.
func discordGuildID(shop *shopv1alpha1.Shop) string {
	if v := shop.Annotations[discordGuildAnnotation]; v != "" {
		return v
	}
	return os.Getenv(discordGuildEnv)
}
