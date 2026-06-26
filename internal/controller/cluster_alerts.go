package controller

import (
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var clusterAlertsLog = logf.Log.WithName("cluster-alerts")

// ClusterAlertsSetup is a controller-runtime Runnable that creates cluster-wide
// monitoring resources once on manager start.
//
// Env vars:
//   - CLUSTER_ALERTS_NAMESPACE   namespace for alert resources (default: monitoring)
//   - CLUSTER_DISCORD_WEBHOOK_URL Discord webhook for cluster-level alerts;
//     if empty the setup is skipped gracefully.
type ClusterAlertsSetup struct {
	client.Client
}

func (s *ClusterAlertsSetup) Start(ctx context.Context) error {
	webhookURL := os.Getenv("CLUSTER_DISCORD_WEBHOOK_URL")
	if webhookURL == "" {
		clusterAlertsLog.Info("CLUSTER_DISCORD_WEBHOOK_URL not set — cluster-wide alerts skipped")
		return nil
	}

	ns := os.Getenv("CLUSTER_ALERTS_NAMESPACE")
	if ns == "" {
		ns = "monitoring"
	}

	if err := s.ensureWebhookSecret(ctx, ns, webhookURL); err != nil {
		return err
	}
	if err := s.ensureNodeDownRule(ctx, ns); err != nil {
		return err
	}
	if err := s.ensureAlertmanagerConfig(ctx, ns); err != nil {
		return err
	}

	clusterAlertsLog.Info("cluster-wide alerts configured", "namespace", ns)
	return nil
}

// ensureWebhookSecret creates/updates the Secret holding the cluster Discord webhook URL.
func (s *ClusterAlertsSetup) ensureWebhookSecret(ctx context.Context, ns, webhookURL string) error {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shophub-cluster-discord",
			Namespace: ns,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, s.Client, sec, func() error {
		sec.Type = corev1.SecretTypeOpaque
		sec.StringData = map[string]string{"webhookURL": webhookURL}
		sec.Labels = clusterAlertLabels()
		return nil
	})
	if err != nil {
		return fmt.Errorf("cluster alerts: Secret: %w", err)
	}
	return nil
}

// ensureNodeDownRule creates/updates the PrometheusRule for node-down alerts.
// The rule uses kube_node_status_condition which is provided by kube-state-metrics.
func (s *ClusterAlertsSetup) ensureNodeDownRule(ctx context.Context, ns string) error {
	pr := &unstructured.Unstructured{}
	pr.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "monitoring.coreos.com", Version: "v1", Kind: "PrometheusRule",
	})
	pr.SetName("shophub-cluster-alerts")
	pr.SetNamespace(ns)

	_, err := controllerutil.CreateOrUpdate(ctx, s.Client, pr, func() error {
		labels := clusterAlertLabels()
		// kube-prometheus-stack's Prometheus picks up rules with this label.
		labels["release"] = "monitoring"
		pr.SetLabels(labels)

		return unstructured.SetNestedField(pr.Object, map[string]interface{}{
			"groups": []interface{}{
				map[string]interface{}{
					"name": "shophub.cluster",
					"rules": []interface{}{
						map[string]interface{}{
							"alert": "ClusterNodeDown",
							"expr":  `kube_node_status_condition{condition="Ready",status="true"} == 0`,
							"for":   "5m",
							"labels": map[string]interface{}{
								"severity":      "critical",
								"cluster_alert": "true",
							},
							"annotations": map[string]interface{}{
								"summary":     "Kubernetes node is not Ready",
								"description": "Node {{ $labels.node }} has been in NotReady state for more than 5 minutes.",
							},
						},
					},
				},
			},
		}, "spec")
	})
	if err != nil {
		return fmt.Errorf("cluster alerts: PrometheusRule: %w", err)
	}
	return nil
}

// ensureAlertmanagerConfig creates/updates the AlertmanagerConfig that routes
// cluster_alert=true alerts to the cluster Discord channel.
func (s *ClusterAlertsSetup) ensureAlertmanagerConfig(ctx context.Context, ns string) error {
	amc := &unstructured.Unstructured{}
	amc.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "AlertmanagerConfig",
	})
	amc.SetName("shophub-cluster-alerts")
	amc.SetNamespace(ns)

	_, err := controllerutil.CreateOrUpdate(ctx, s.Client, amc, func() error {
		amc.SetLabels(clusterAlertLabels())

		return unstructured.SetNestedField(amc.Object, map[string]interface{}{
			"route": map[string]interface{}{
				"receiver": "cluster-discord",
				"matchers": []interface{}{
					map[string]interface{}{
						"name":      "cluster_alert",
						"value":     "true",
						"matchType": "=",
					},
				},
			},
			"receivers": []interface{}{
				map[string]interface{}{
					"name": "cluster-discord",
					"discordConfigs": []interface{}{
						map[string]interface{}{
							"sendResolved": true,
							"apiURL": map[string]interface{}{
								"name": "shophub-cluster-discord",
								"key":  "webhookURL",
							},
							"title":   "Cluster Alert: {{ .GroupLabels.alertname }}",
							"message": "{{ range .Alerts }}{{ .Annotations.description }}\n{{ end }}",
						},
					},
				},
			},
		}, "spec")
	})
	if err != nil {
		return fmt.Errorf("cluster alerts: AlertmanagerConfig: %w", err)
	}
	return nil
}

func clusterAlertLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "shop-operator",
		"app.kubernetes.io/component":  "cluster-alerts",
	}
}
