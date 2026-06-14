package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	shopv1alpha1 "github.com/shophub-platform/shop-operator/api/v1alpha1"
)

func testShop() *shopv1alpha1.Shop {
	return &shopv1alpha1.Shop{
		ObjectMeta: metav1.ObjectMeta{Name: "myshop", Namespace: "default"},
		Spec: shopv1alpha1.ShopSpec{
			Name:         "myshop",
			Availability: shopv1alpha1.AvailabilityHigh,
			DatabaseType: shopv1alpha1.DatabasePostgres,
			Image:        "be:img",
			WalletRef:    corev1.LocalObjectReference{Name: "w"},
		},
	}
}

func TestNamingAndURL(t *testing.T) {
	s := testShop()
	if got := backendName(s); got != "myshop-backend" {
		t.Errorf("backendName = %q", got)
	}
	if got := frontendName(s); got != "myshop-frontend" {
		t.Errorf("frontendName = %q", got)
	}
	if got := configMapName(s); got != "myshop-config" {
		t.Errorf("configMapName = %q", got)
	}
	if got := secretName(s); got != "myshop-secret" {
		t.Errorf("secretName = %q", got)
	}
	if got := shopHost(s); got != "myshop.shophub.local" {
		t.Errorf("shopHost = %q", got)
	}
	if got := shopURL(s); got != "http://myshop.shophub.local" {
		t.Errorf("shopURL = %q", got)
	}
}

func TestFrontendImage(t *testing.T) {
	s := testShop()
	if got := frontendImage(s); got != defaultFrontendImage {
		t.Errorf("default frontendImage = %q, want %q", got, defaultFrontendImage)
	}
	s.Annotations = map[string]string{frontendImageAnnotation: "fe:custom"}
	if got := frontendImage(s); got != "fe:custom" {
		t.Errorf("override frontendImage = %q, want fe:custom", got)
	}
}

func TestBuildBackendDeployment(t *testing.T) {
	s := testShop()
	dep := &appsv1.Deployment{}
	buildBackendDeployment(dep, s, 3)

	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 3 {
		t.Fatalf("replicas = %v, want 3", dep.Spec.Replicas)
	}
	c := dep.Spec.Template.Spec.Containers
	if len(c) != 1 || c[0].Image != "be:img" {
		t.Fatalf("container = %+v, want image be:img", c)
	}
	if c[0].Ports[0].ContainerPort != backendPort {
		t.Errorf("port = %d, want %d", c[0].Ports[0].ContainerPort, backendPort)
	}
	// envFrom must reference both the ConfigMap and the Secret.
	var hasCM, hasSecret bool
	for _, ef := range c[0].EnvFrom {
		if ef.ConfigMapRef != nil && ef.ConfigMapRef.Name == configMapName(s) {
			hasCM = true
		}
		if ef.SecretRef != nil && ef.SecretRef.Name == secretName(s) {
			hasSecret = true
		}
	}
	if !hasCM || !hasSecret {
		t.Errorf("envFrom missing refs: cm=%v secret=%v", hasCM, hasSecret)
	}
}

func TestBuildService(t *testing.T) {
	s := testShop()
	svc := &corev1.Service{}
	buildService(svc, s, "backend", backendPort)

	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("type = %q, want ClusterIP", svc.Spec.Type)
	}
	if svc.Spec.Selector["app.kubernetes.io/component"] != "backend" {
		t.Errorf("selector = %v", svc.Spec.Selector)
	}
	if svc.Spec.Ports[0].Port != backendPort {
		t.Errorf("port = %d, want %d", svc.Spec.Ports[0].Port, backendPort)
	}
}

func TestBuildIngress(t *testing.T) {
	s := testShop()
	ing := &networkingv1.Ingress{}
	buildIngress(ing, s)

	if ing.Spec.Rules[0].Host != "myshop.shophub.local" {
		t.Fatalf("host = %q", ing.Spec.Rules[0].Host)
	}
	paths := ing.Spec.Rules[0].HTTP.Paths
	got := map[string]string{}
	for _, p := range paths {
		got[p.Path] = p.Backend.Service.Name
	}
	if got["/api"] != backendName(s) {
		t.Errorf("/api -> %q, want %q", got["/api"], backendName(s))
	}
	if got["/"] != frontendName(s) {
		t.Errorf("/ -> %q, want %q", got["/"], frontendName(s))
	}
}

func TestBuildConfigMapAndSecret(t *testing.T) {
	s := testShop()

	cm := &corev1.ConfigMap{}
	buildConfigMap(cm, s, map[string]string{"DB_HOST": "h", "WALLET_ADDRESS": "0xabc"})
	if cm.Data["DB_HOST"] != "h" || cm.Data["WALLET_ADDRESS"] != "0xabc" {
		t.Errorf("configmap data = %v", cm.Data)
	}

	sec := &corev1.Secret{}
	buildSecret(sec, s, map[string]string{"DATABASE_URL": "postgres://x"})
	if sec.Type != corev1.SecretTypeOpaque {
		t.Errorf("secret type = %q", sec.Type)
	}
	if sec.StringData["DATABASE_URL"] != "postgres://x" {
		t.Errorf("secret data = %v", sec.StringData)
	}
}

func TestBuildGrafanaDashboard(t *testing.T) {
	s := testShop()
	cm := &corev1.ConfigMap{}
	buildGrafanaDashboard(cm, s)

	if cm.Labels["grafana_dashboard"] != "1" {
		t.Errorf("missing grafana_dashboard label: %v", cm.Labels)
	}
	if _, ok := cm.Data["myshop.json"]; !ok {
		t.Errorf("dashboard json key missing: %v", cm.Data)
	}
}

func TestReplicasForAvailability(t *testing.T) {
	if got := shopv1alpha1.ReplicasForAvailability(shopv1alpha1.AvailabilityHigh); got != 3 {
		t.Errorf("high = %d, want 3", got)
	}
	if got := shopv1alpha1.ReplicasForAvailability(shopv1alpha1.AvailabilityStandard); got != 2 {
		t.Errorf("standard = %d, want 2", got)
	}
}
