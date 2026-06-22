package controller

import (
	"fmt"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	shopv1alpha1 "github.com/shophub-platform/shop-operator/api/v1alpha1"
)

// Container ports for the shop application components.
const (
	backendPort  = 8081
	frontendPort = 80
	postgresPort = 5432
	redisPort    = 6379

	// frontendImageAnnotation lets a Shop override the frontend image; if unset
	// defaultFrontendImage is used. Spec.Image is treated as the backend image.
	frontendImageAnnotation = "shop.shophub.io/frontend-image"
	defaultFrontendImage    = "ghcr.io/shophub-platform/shop-frontend:latest"
)

// ---------------------------------------------------------------------------
// Naming helpers — every child resource is derived deterministically from the
// Shop name so reconciliation is idempotent.
// ---------------------------------------------------------------------------

func backendName(shop *shopv1alpha1.Shop) string  { return shop.Name + "-backend" }
func frontendName(shop *shopv1alpha1.Shop) string { return shop.Name + "-frontend" }
func configMapName(shop *shopv1alpha1.Shop) string { return shop.Name + "-config" }
func secretName(shop *shopv1alpha1.Shop) string    { return shop.Name + "-secret" }
func databaseName(shop *shopv1alpha1.Shop) string  { return shop.Name + "-db" }
func ingressName(shop *shopv1alpha1.Shop) string   { return shop.Name }
func dashboardName(shop *shopv1alpha1.Shop) string { return shop.Name + "-dashboard" }
func discordChannelName(shop *shopv1alpha1.Shop) string { return shop.Name }

// ingressBaseDomain returns the base domain for ingress hosts.
// INGRESS_DOMAIN env overrides the default (e.g. "127.0.0.1.nip.io" for local dev).
func ingressBaseDomain() string {
	if v := os.Getenv("INGRESS_DOMAIN"); v != "" {
		return v
	}
	return "shophub.local"
}

// containerImagePullPolicy returns the pull policy for shop container images.
// Set IMAGE_PULL_POLICY=Never when running against Docker Desktop / kind where
// images are loaded locally via ctr/kind load and cannot be pulled from a registry.
func containerImagePullPolicy() corev1.PullPolicy {
	if v := os.Getenv("IMAGE_PULL_POLICY"); v != "" {
		return corev1.PullPolicy(v)
	}
	return corev1.PullIfNotPresent
}

// shopHost returns the public ingress host for the shop.
func shopHost(shop *shopv1alpha1.Shop) string {
	return fmt.Sprintf("%s.%s", shop.Name, ingressBaseDomain())
}

// shopURL returns the public URL of the shop.
func shopURL(shop *shopv1alpha1.Shop) string {
	return "http://" + shopHost(shop)
}

// frontendImage returns the frontend image, honouring the annotation override.
func frontendImage(shop *shopv1alpha1.Shop) string {
	if v := shop.Annotations[frontendImageAnnotation]; v != "" {
		return v
	}
	if v := os.Getenv("DEFAULT_FRONTEND_IMAGE"); v != "" {
		return v
	}
	return defaultFrontendImage
}

// labelsFor builds the common label set; component distinguishes child resources.
func labelsFor(shop *shopv1alpha1.Shop, component string) map[string]string {
	l := map[string]string{
		"app.kubernetes.io/name":       "shop",
		"app.kubernetes.io/instance":   shop.Name,
		"app.kubernetes.io/managed-by": "shop-operator",
		"shop.shophub.io/shop":         shop.Name,
	}
	if component != "" {
		l["app.kubernetes.io/component"] = component
	}
	return l
}

// selectorFor returns a minimal, stable label selector (Deployment/Service
// selectors are immutable, so they must never change between reconciles).
func selectorFor(shop *shopv1alpha1.Shop, component string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/instance":  shop.Name,
		"app.kubernetes.io/component": component,
	}
}

// ---------------------------------------------------------------------------
// Mutators — each takes the live object fetched by CreateOrUpdate and sets the
// desired state in place. They must be idempotent.
// ---------------------------------------------------------------------------

// buildConfigMap (step 5): shop application configuration.
func buildConfigMap(cm *corev1.ConfigMap, shop *shopv1alpha1.Shop, data map[string]string) {
	cm.Labels = labelsFor(shop, "config")
	cm.Data = data
}

// buildSecret (step 6): connection string + webhook URL.
func buildSecret(sec *corev1.Secret, shop *shopv1alpha1.Shop, stringData map[string]string) {
	sec.Labels = labelsFor(shop, "secret")
	sec.Type = corev1.SecretTypeOpaque
	sec.StringData = stringData
}

// buildBackendDeployment (step 7): backend + blockchain listener sidecar.
func buildBackendDeployment(dep *appsv1.Deployment, shop *shopv1alpha1.Shop, replicas int32) {
	dep.Labels = labelsFor(shop, "backend")
	r := replicas
	dep.Spec.Replicas = &r
	dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: selectorFor(shop, "backend")}
	dep.Spec.Template.ObjectMeta.Labels = labelsFor(shop, "backend")

	envFrom := []corev1.EnvFromSource{
		{ConfigMapRef: &corev1.ConfigMapEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: configMapName(shop)}}},
		{SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName(shop)}}},
	}

	pullPolicy := containerImagePullPolicy()
	dep.Spec.Template.Spec.Containers = []corev1.Container{
		{
			Name:            "backend",
			Image:           shop.Spec.Image,
			ImagePullPolicy: pullPolicy,
			Ports:           []corev1.ContainerPort{{Name: "http", ContainerPort: backendPort}},
			Env:             backendDBEnv(shop),
			EnvFrom:         envFrom,
			ReadinessProbe:  httpProbe("/health", backendPort),
			LivenessProbe:   httpProbe("/health", backendPort),
			Resources:       defaultResources(),
		},
		{
			// Blockchain listener — same image, overridden command, shared pod network
			// so LISTENER_BACKEND_URL=http://localhost:8081 reaches the backend container.
			Name:            "listener",
			Image:           shop.Spec.Image,
			ImagePullPolicy: pullPolicy,
			Command:         []string{"./listener"},
			Env:             backendDBEnv(shop),
			EnvFrom:         envFrom,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("32Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				},
			},
		},
	}
}

// backendDBEnv injects the database credentials the shop backend expects
// (DB_NAME/DB_USER/DB_PASSWORD/DB_SSLMODE) from the CNPG-generated app Secret.
// DB_HOST/DB_PORT come from the ConfigMap.
func backendDBEnv(shop *shopv1alpha1.Shop) []corev1.EnvVar {
	if shop.Spec.DatabaseType == shopv1alpha1.DatabaseRedis {
		return nil
	}
	appSecret := databaseName(shop) + "-app"
	return []corev1.EnvVar{
		secretEnv("DB_NAME", appSecret, "dbname"),
		secretEnv("DB_USER", appSecret, "username"),
		secretEnv("DB_PASSWORD", appSecret, "password"),
		{Name: "DB_SSLMODE", Value: "require"},
	}
}

func secretEnv(name, secretName, key string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				Key:                  key,
			},
		},
	}
}

// buildFrontendDeployment (step 8).
func buildFrontendDeployment(dep *appsv1.Deployment, shop *shopv1alpha1.Shop, replicas int32) {
	dep.Labels = labelsFor(shop, "frontend")
	r := replicas
	dep.Spec.Replicas = &r
	dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: selectorFor(shop, "frontend")}
	dep.Spec.Template.ObjectMeta.Labels = labelsFor(shop, "frontend")
	dep.Spec.Template.Spec.Containers = []corev1.Container{
		{
			Name:            "frontend",
			Image:           frontendImage(shop),
			ImagePullPolicy: containerImagePullPolicy(),
			Ports:           []corev1.ContainerPort{{Name: "http", ContainerPort: frontendPort}},
			EnvFrom: []corev1.EnvFromSource{
				{ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName(shop)}}},
			},
			ReadinessProbe: httpProbe("/", frontendPort),
			Resources:      defaultResources(),
		},
	}
}

// buildService (step 9): a ClusterIP service in front of the given component.
func buildService(svc *corev1.Service, shop *shopv1alpha1.Shop, component string, port int32) {
	svc.Labels = labelsFor(shop, component)
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	svc.Spec.Selector = selectorFor(shop, component)
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "http",
			Port:       port,
			TargetPort: intstr.FromInt32(port),
			Protocol:   corev1.ProtocolTCP,
		},
	}
}

// buildIngress (step 9): host <name>.shophub.local, "/" → frontend, "/api" → backend.
func buildIngress(ing *networkingv1.Ingress, shop *shopv1alpha1.Shop) {
	ing.Labels = labelsFor(shop, "ingress")
	// Ingress class is configurable (INGRESS_CLASS env); defaults to nginx.
	// On k3d/k3s set INGRESS_CLASS=traefik.
	className := os.Getenv("INGRESS_CLASS")
	if className == "" {
		className = "nginx"
	}
	ing.Spec.IngressClassName = &className
	prefix := networkingv1.PathTypePrefix
	ing.Spec.Rules = []networkingv1.IngressRule{
		{
			Host: shopHost(shop),
			IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{
						{
							Path:     "/api",
							PathType: &prefix,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: backendName(shop),
									Port: networkingv1.ServiceBackendPort{Number: backendPort},
								},
							},
						},
						{
							Path:     "/",
							PathType: &prefix,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: frontendName(shop),
									Port: networkingv1.ServiceBackendPort{Number: frontendPort},
								},
							},
						},
					},
				},
			},
		},
	}
}

// buildGrafanaDashboard (step 11): a ConfigMap labelled grafana_dashboard:"1"
// so the Grafana sidecar imports it automatically.
func buildGrafanaDashboard(cm *corev1.ConfigMap, shop *shopv1alpha1.Shop) {
	l := labelsFor(shop, "dashboard")
	l["grafana_dashboard"] = "1"
	cm.Labels = l
	cm.Data = map[string]string{
		shop.Name + ".json": grafanaDashboardJSON(shop),
	}
}

// ---------------------------------------------------------------------------
// Small helpers.
// ---------------------------------------------------------------------------

func httpProbe(path string, port int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: path,
				Port: intstr.FromInt32(port),
			},
		},
		InitialDelaySeconds: 10,
		PeriodSeconds:       15,
		TimeoutSeconds:      3,
		FailureThreshold:    6,
	}
}

func defaultResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("50m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
	}
}

// grafanaDashboardJSON returns a full Grafana dashboard for one shop instance.
// %[1]s = shop.Name, %[2]s = shop.Namespace.
// The "shop" label on every metric is injected by the ServiceMonitor relabeling.
func grafanaDashboardJSON(shop *shopv1alpha1.Shop) string {
	uid := fmt.Sprintf("shop-%s", shop.Name)
	if len(uid) > 40 {
		uid = uid[:40]
	}
	return fmt.Sprintf(`{
  "annotations": {"list": []},
  "editable": true,
  "graphTooltip": 1,
  "schemaVersion": 39,
  "tags": ["shophub", "%[1]s"],
  "templating": {"list": []},
  "time": {"from": "now-6h", "to": "now"},
  "timezone": "browser",
  "title": "Shop — %[1]s",
  "uid": "%[3]s",
  "panels": [
    {
      "type": "timeseries", "id": 1,
      "title": "CPU usage",
      "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0},
      "fieldConfig": {"defaults": {"unit": "short"}},
      "targets": [
        {"expr": "sum(rate(container_cpu_usage_seconds_total{namespace=\"%[2]s\",pod=~\"%[1]s-.*\",container!=\"\"}[5m])) by (pod)", "legendFormat": "{{pod}}"}
      ]
    },
    {
      "type": "timeseries", "id": 2,
      "title": "RAM usage",
      "gridPos": {"h": 8, "w": 12, "x": 12, "y": 0},
      "fieldConfig": {"defaults": {"unit": "bytes"}},
      "targets": [
        {"expr": "sum(container_memory_working_set_bytes{namespace=\"%[2]s\",pod=~\"%[1]s-.*\",container!=\"\"}) by (pod)", "legendFormat": "{{pod}}"}
      ]
    },
    {
      "type": "timeseries", "id": 3,
      "title": "Disk usage (PVC)",
      "gridPos": {"h": 8, "w": 12, "x": 0, "y": 8},
      "fieldConfig": {"defaults": {"unit": "bytes"}},
      "targets": [
        {"expr": "kubelet_volume_stats_used_bytes{namespace=\"%[2]s\",persistentvolumeclaim=~\"%[1]s-.*\"}", "legendFormat": "{{persistentvolumeclaim}}"}
      ]
    },
    {
      "type": "timeseries", "id": 4,
      "title": "Network throughput",
      "gridPos": {"h": 8, "w": 12, "x": 12, "y": 8},
      "fieldConfig": {"defaults": {"unit": "Bps"}},
      "targets": [
        {"expr": "sum(rate(container_network_receive_bytes_total{namespace=\"%[2]s\",pod=~\"%[1]s-.*\"}[5m]))", "legendFormat": "RX"},
        {"expr": "sum(rate(container_network_transmit_bytes_total{namespace=\"%[2]s\",pod=~\"%[1]s-.*\"}[5m]))", "legendFormat": "TX"}
      ]
    },
    {
      "type": "timeseries", "id": 5,
      "title": "HTTP request rate",
      "gridPos": {"h": 8, "w": 12, "x": 0, "y": 16},
      "fieldConfig": {"defaults": {"unit": "reqps"}},
      "targets": [
        {"expr": "sum(rate(http_requests_total{shop=\"%[1]s\"}[5m])) by (status_code)", "legendFormat": "{{status_code}}"}
      ]
    },
    {
      "type": "timeseries", "id": 6,
      "title": "HTTP latency (p99 / p50)",
      "gridPos": {"h": 8, "w": 12, "x": 12, "y": 16},
      "fieldConfig": {"defaults": {"unit": "s"}},
      "targets": [
        {"expr": "histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{shop=\"%[1]s\"}[5m])) by (le))", "legendFormat": "p99"},
        {"expr": "histogram_quantile(0.50, sum(rate(http_request_duration_seconds_bucket{shop=\"%[1]s\"}[5m])) by (le))", "legendFormat": "p50"}
      ]
    },
    {
      "type": "stat", "id": 7,
      "title": "Successful requests 2xx/3xx (24h)",
      "gridPos": {"h": 8, "w": 8, "x": 0, "y": 24},
      "fieldConfig": {"defaults": {"unit": "short", "color": {"mode": "fixed", "fixedColor": "green"}}},
      "targets": [
        {"expr": "sum(increase(http_requests_total{shop=\"%[1]s\",status_code=~\"2..|3..\"}[24h]))", "legendFormat": ""}
      ]
    },
    {
      "type": "stat", "id": 8,
      "title": "Failed requests 4xx/5xx (24h)",
      "gridPos": {"h": 8, "w": 8, "x": 8, "y": 24},
      "fieldConfig": {"defaults": {"unit": "short", "color": {"mode": "fixed", "fixedColor": "red"}}},
      "targets": [
        {"expr": "sum(increase(http_requests_total{shop=\"%[1]s\",status_code=~\"4..|5..\"}[24h]))", "legendFormat": ""}
      ]
    },
    {
      "type": "table", "id": 9,
      "title": "404s by endpoint (24h)",
      "gridPos": {"h": 8, "w": 8, "x": 16, "y": 24},
      "targets": [
        {"expr": "sum by (path) (increase(http_requests_total{shop=\"%[1]s\",status_code=\"404\"}[24h]))", "legendFormat": "", "instant": true}
      ]
    },
    {
      "type": "stat", "id": 10,
      "title": "Unique visitors",
      "gridPos": {"h": 8, "w": 6, "x": 0, "y": 32},
      "fieldConfig": {"defaults": {"unit": "short"}},
      "targets": [
        {"expr": "http_unique_visitors_total{shop=\"%[1]s\"}", "legendFormat": ""}
      ]
    },
    {
      "type": "stat", "id": 11,
      "title": "Total traffic (GB)",
      "gridPos": {"h": 8, "w": 6, "x": 6, "y": 32},
      "fieldConfig": {"defaults": {"unit": "short", "decimals": 2}},
      "targets": [
        {"expr": "sum(http_response_bytes_total{shop=\"%[1]s\"}) / 1e9", "legendFormat": ""}
      ]
    },
    {
      "type": "timeseries", "id": 12,
      "title": "Orders rate",
      "gridPos": {"h": 8, "w": 12, "x": 12, "y": 32},
      "fieldConfig": {"defaults": {"unit": "short"}},
      "targets": [
        {"expr": "rate(orders_created_total{shop=\"%[1]s\"}[5m])", "legendFormat": "orders/s"}
      ]
    },
    {
      "type": "bargauge", "id": 13,
      "title": "Items stock level",
      "gridPos": {"h": 8, "w": 12, "x": 0, "y": 40},
      "fieldConfig": {"defaults": {"unit": "short"}},
      "options": {"orientation": "horizontal", "reduceOptions": {"calcs": ["lastNotNull"]}},
      "targets": [
        {"expr": "items_stock_level{shop=\"%[1]s\"}", "legendFormat": "{{item_name}}"}
      ]
    },
    {
      "type": "timeseries", "id": 14,
      "title": "Payment processing duration (p95 / p50)",
      "gridPos": {"h": 8, "w": 12, "x": 12, "y": 40},
      "fieldConfig": {"defaults": {"unit": "s"}},
      "targets": [
        {"expr": "histogram_quantile(0.95, sum(rate(payment_processing_duration_seconds_bucket{shop=\"%[1]s\"}[5m])) by (le))", "legendFormat": "p95"},
        {"expr": "histogram_quantile(0.50, sum(rate(payment_processing_duration_seconds_bucket{shop=\"%[1]s\"}[5m])) by (le))", "legendFormat": "p50"}
      ]
    }
  ]
}`, shop.Name, shop.Namespace, uid)
}
