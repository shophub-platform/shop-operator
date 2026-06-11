package controller

import (
	"fmt"

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

	// ingressDomain is the base domain for shop ingress hosts: <name>.shophub.local.
	ingressDomain = "shophub.local"

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

// shopHost returns the public ingress host for the shop.
func shopHost(shop *shopv1alpha1.Shop) string {
	return fmt.Sprintf("%s.%s", shop.Name, ingressDomain)
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

// buildBackendDeployment (step 7): replicas + envFrom ConfigMap & Secret.
func buildBackendDeployment(dep *appsv1.Deployment, shop *shopv1alpha1.Shop, replicas int32) {
	dep.Labels = labelsFor(shop, "backend")
	r := replicas
	dep.Spec.Replicas = &r
	dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: selectorFor(shop, "backend")}
	dep.Spec.Template.ObjectMeta.Labels = labelsFor(shop, "backend")
	dep.Spec.Template.Spec.Containers = []corev1.Container{
		{
			Name:  "backend",
			Image: shop.Spec.Image,
			Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: backendPort}},
			EnvFrom: []corev1.EnvFromSource{
				{ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName(shop)}}},
				{SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretName(shop)}}},
			},
			ReadinessProbe: httpProbe("/health", backendPort),
			LivenessProbe:  httpProbe("/health", backendPort),
			Resources:      defaultResources(),
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
			Name:  "frontend",
			Image: frontendImage(shop),
			Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: frontendPort}},
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
	className := "nginx"
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

// grafanaDashboardJSON returns a minimal but valid Grafana dashboard scoped to
// this shop's metrics. Kept intentionally small; richer panels are added in F4.
func grafanaDashboardJSON(shop *shopv1alpha1.Shop) string {
	return fmt.Sprintf(`{
  "annotations": {"list": []},
  "editable": true,
  "panels": [
    {
      "type": "timeseries",
      "title": "HTTP requests (rate)",
      "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0},
      "targets": [
        {"expr": "sum(rate(http_requests_total{shop=\"%[1]s\"}[5m]))", "legendFormat": "rps"}
      ]
    },
    {
      "type": "timeseries",
      "title": "CPU usage",
      "gridPos": {"h": 8, "w": 12, "x": 12, "y": 0},
      "targets": [
        {"expr": "sum(rate(container_cpu_usage_seconds_total{pod=~\"%[1]s-.*\"}[5m]))", "legendFormat": "cores"}
      ]
    }
  ],
  "schemaVersion": 39,
  "tags": ["shophub", "%[1]s"],
  "templating": {"list": []},
  "time": {"from": "now-6h", "to": "now"},
  "title": "Shop — %[1]s",
  "uid": "shop-%[1]s"
}`, shop.Name)
}
