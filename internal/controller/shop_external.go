package controller

import (
	"context"
	"fmt"
	"os"

	appsv1 "k8s.io/api/apps/v1"
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
	prometheusRuleGVK = schema.GroupVersionKind{
		Group: "monitoring.coreos.com", Version: "v1", Kind: "PrometheusRule",
	}
	alertmanagerConfigGVK = schema.GroupVersionKind{
		Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "AlertmanagerConfig",
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

	// OpsTree Redis operator (v1beta2 standalone) does not populate status.state;
	// check the StatefulSet it creates instead.
	var sts appsv1.StatefulSet
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: shop.Namespace}, &sts); err == nil {
		info.Ready = sts.Status.ReadyReplicas >= 1
	}
	return info, nil
}

// ---------------------------------------------------------------------------
// Step 10 — Prometheus ServiceMonitor + PodMonitor.
// ---------------------------------------------------------------------------

func (r *ShopReconciler) reconcileMonitors(ctx context.Context, shop *shopv1alpha1.Shop) error {
	// relabeling adds a static shop=<name> label to every scraped metric so
	// PromQL queries like {shop="myshop"} work across the shared Prometheus.
	shopRelabeling := []interface{}{
		map[string]interface{}{
			"targetLabel": "shop",
			"replacement": shop.Name,
		},
	}

	smSpec := map[string]interface{}{
		"selector": map[string]interface{}{
			"matchLabels": map[string]interface{}{
				"app.kubernetes.io/instance":  shop.Name,
				"app.kubernetes.io/component": "backend",
			},
		},
		"endpoints": []interface{}{
			map[string]interface{}{
				"port":        "http",
				"path":        "/metrics",
				"interval":    "30s",
				"relabelings": shopRelabeling,
			},
		},
	}
	if err := r.upsertUnstructured(ctx, shop, serviceMonitorGVK, shop.Name, smSpec,
		map[string]string{"release": "monitoring"}); err != nil {
		return fmt.Errorf("apply ServiceMonitor: %w", err)
	}

	pmSpec := map[string]interface{}{
		"selector": map[string]interface{}{
			"matchLabels": map[string]interface{}{
				"app.kubernetes.io/instance":  shop.Name,
				"app.kubernetes.io/component": "backend",
			},
		},
		"podMetricsEndpoints": []interface{}{
			map[string]interface{}{
				"port":        "http",
				"path":        "/metrics",
				"interval":    "30s",
				"relabelings": shopRelabeling,
			},
		},
	}
	if err := r.upsertUnstructured(ctx, shop, podMonitorGVK, shop.Name, pmSpec,
		map[string]string{"release": "monitoring"}); err != nil {
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

// ---------------------------------------------------------------------------
// Step 10.5 — PrometheusRule + AlertmanagerConfig (per-shop alarms).
// ---------------------------------------------------------------------------

func (r *ShopReconciler) reconcileAlerts(ctx context.Context, shop *shopv1alpha1.Shop) error {
	// PrometheusRule — four per-shop alert rules.
	// Label release:kube-prometheus-stack makes Prometheus Operator pick it up.
	prSpec := map[string]interface{}{
		"groups": []interface{}{
			map[string]interface{}{
				"name": "shop." + shop.Name,
				"rules": []interface{}{
					shopAlertRule(
						"ShopPodRestarting",
						fmt.Sprintf(`increase(kube_pod_container_status_restarts_total{namespace="%s",pod=~"%s-.*"}[15m]) > 5`,
							shop.Namespace, shop.Name),
						"0m", "warning",
						fmt.Sprintf("Pod restart loop in shop %s", shop.Name),
						fmt.Sprintf("A pod in shop %s has restarted more than 5 times in the last 15 minutes.", shop.Name),
						shop.Name,
					),
					shopAlertRule(
						"ShopHigh5xxRate",
						fmt.Sprintf(`sum(rate(http_requests_total{shop="%s",status_code=~"5.."}[5m])) / sum(rate(http_requests_total{shop="%s"}[5m])) > 0.05`,
							shop.Name, shop.Name),
						"5m", "critical",
						fmt.Sprintf("HTTP 5xx error rate > 5%% in shop %s", shop.Name),
						fmt.Sprintf("HTTP 5xx error rate for shop %s has exceeded 5%% for the last 5 minutes.", shop.Name),
						shop.Name,
					),
					shopAlertRule(
						"ShopHighCPU",
						fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{namespace="%s",pod=~"%s-.*",container!=""}[5m])) / sum(kube_pod_container_resource_limits{namespace="%s",pod=~"%s-.*",resource="cpu"}) > 0.9`,
							shop.Namespace, shop.Name, shop.Namespace, shop.Name),
						"10m", "warning",
						fmt.Sprintf("CPU usage > 90%% in shop %s", shop.Name),
						fmt.Sprintf("CPU usage for shop %s has exceeded 90%% of its limit for 10 minutes.", shop.Name),
						shop.Name,
					),
					shopAlertRule(
						"ShopDiskAlmostFull",
						fmt.Sprintf(`kubelet_volume_stats_used_bytes{namespace="%s",persistentvolumeclaim=~"%s-.*"} / kubelet_volume_stats_capacity_bytes{namespace="%s",persistentvolumeclaim=~"%s-.*"} > 0.85`,
							shop.Namespace, shop.Name, shop.Namespace, shop.Name),
						"5m", "critical",
						fmt.Sprintf("PVC disk usage > 85%% in shop %s", shop.Name),
						fmt.Sprintf("A PVC for shop %s is more than 85%% full.", shop.Name),
						shop.Name,
					),
				},
			},
		},
	}
	if err := r.upsertUnstructured(ctx, shop, prometheusRuleGVK, shop.Name+"-alerts", prSpec,
		map[string]string{"release": "monitoring"}); err != nil {
		return fmt.Errorf("apply PrometheusRule: %w", err)
	}

	// AlertmanagerConfig — routes alerts labelled shop=<name> to the shop's
	// Discord webhook. The URL comes from the Secret created in step 6.
	amcSpec := map[string]interface{}{
		"route": map[string]interface{}{
			"receiver": "discord",
			"matchers": []interface{}{
				map[string]interface{}{
					"name":      "shop",
					"value":     shop.Name,
					"matchType": "=",
				},
			},
		},
		"receivers": []interface{}{
			map[string]interface{}{
				"name": "discord",
				"discordConfigs": []interface{}{
					map[string]interface{}{
						"sendResolved": true,
						"apiURL": map[string]interface{}{
							"name": secretName(shop),
							"key":  "DISCORD_WEBHOOK_URL",
						},
						"title":   "ShopHub Alert: {{ .GroupLabels.alertname }}",
						"message": "{{ range .Alerts }}{{ .Annotations.description }}\n{{ end }}",
					},
				},
			},
		},
	}
	if err := r.upsertUnstructured(ctx, shop, alertmanagerConfigGVK, shop.Name+"-alerts", amcSpec, nil); err != nil {
		return fmt.Errorf("apply AlertmanagerConfig: %w", err)
	}
	return nil
}

// shopAlertRule builds a single Prometheus alert rule map.
func shopAlertRule(name, expr, forDur, severity, summary, description, shop string) map[string]interface{} {
	return map[string]interface{}{
		"alert": name,
		"expr":  expr,
		"for":   forDur,
		"labels": map[string]interface{}{
			"severity": severity,
			"shop":     shop,
		},
		"annotations": map[string]interface{}{
			"summary":     summary,
			"description": description,
		},
	}
}

// discordGuildID resolves the Discord guild ID from the Shop annotation or the
// operator environment.
func discordGuildID(shop *shopv1alpha1.Shop) string {
	if v := shop.Annotations[discordGuildAnnotation]; v != "" {
		return v
	}
	return os.Getenv(discordGuildEnv)
}
