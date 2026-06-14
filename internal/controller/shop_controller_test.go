package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	shopv1alpha1 "github.com/shophub-platform/shop-operator/api/v1alpha1"
)

func newReconcileShop() *shopv1alpha1.Shop {
	return &shopv1alpha1.Shop{
		ObjectMeta: objMeta("myshop", "default"),
		Spec: shopv1alpha1.ShopSpec{
			Name:         "myshop",
			Availability: shopv1alpha1.AvailabilityHigh,
			DatabaseType: shopv1alpha1.DatabasePostgres,
			Image:        "be:img",
			WalletRef:    corev1.LocalObjectReference{Name: "w"},
		},
	}
}

func TestShopReconcile_WalletMissing(t *testing.T) {
	ctx := context.Background()
	s := newScheme(t)
	registerExternalTypes(s)

	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&shopv1alpha1.Shop{}).
		WithObjects(newReconcileShop()).Build()
	r := &ShopReconciler{Client: cl, Scheme: s}

	reconcileN(t, r, "myshop", "default", 2) // 1: spec.replicas, 2: wallet missing -> pending

	var got shopv1alpha1.Shop
	if err := cl.Get(ctx, types.NamespacedName{Name: "myshop", Namespace: "default"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != shopv1alpha1.ShopPhaseProvisioning {
		t.Fatalf("phase = %q, want Provisioning", got.Status.Phase)
	}
}

// markDatabaseReady patches the generated CNPG Cluster to healthy and creates
// the CNPG-style app Secret, so the reconciler can proceed past the DB gate.
func markDatabaseReady(t *testing.T, cl client.Client, shopName string) {
	t.Helper()
	ctx := context.Background()
	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(cnpgClusterGVK)
	if err := cl.Get(ctx, types.NamespacedName{Name: shopName + "-db", Namespace: "default"}, cluster); err != nil {
		t.Fatalf("get cluster: %v", err)
	}
	_ = unstructured.SetNestedField(cluster.Object, "Cluster in healthy state", "status", "phase")
	if err := cl.Update(ctx, cluster); err != nil {
		t.Fatalf("update cluster status: %v", err)
	}
	appSecret := &corev1.Secret{
		ObjectMeta: objMeta(shopName+"-db-app", "default"),
		Data:       map[string][]byte{"uri": []byte("postgres://shop:pw@" + shopName + "-db-rw:5432/shop")},
	}
	if err := cl.Create(ctx, appSecret); err != nil {
		t.Fatalf("create app secret: %v", err)
	}
}

func TestShopReconcile_FullProvisioning(t *testing.T) {
	ctx := context.Background()
	s := newScheme(t)
	registerExternalTypes(s)

	w := &shopv1alpha1.Wallet{
		ObjectMeta: objMeta("w", "default"),
		Spec:       shopv1alpha1.WalletSpec{Address: "0x52908400098527886e0f7030069857d2e4169ee7"},
	}
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&shopv1alpha1.Shop{}).
		WithObjects(w, newReconcileShop()).Build()
	r := &ShopReconciler{Client: cl, Scheme: s}

	reconcileN(t, r, "myshop", "default", 2) // up to DB created (not ready -> pending)
	markDatabaseReady(t, cl, "myshop")
	reconcileN(t, r, "myshop", "default", 1) // DB ready -> create all children

	mustExist := func(obj client.Object, name string) {
		t.Helper()
		if err := cl.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, obj); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	mustExist(&corev1.ConfigMap{}, "myshop-config")
	mustExist(&corev1.Secret{}, "myshop-secret")
	mustExist(&appsv1.Deployment{}, "myshop-backend")
	mustExist(&appsv1.Deployment{}, "myshop-frontend")
	mustExist(&corev1.Service{}, "myshop-backend")
	mustExist(&corev1.Service{}, "myshop-frontend")
	mustExist(&networkingv1.Ingress{}, "myshop")
	mustExist(&corev1.ConfigMap{}, "myshop-dashboard")

	var be appsv1.Deployment
	if err := cl.Get(ctx, types.NamespacedName{Name: "myshop-backend", Namespace: "default"}, &be); err != nil {
		t.Fatal(err)
	}
	if be.Spec.Replicas == nil || *be.Spec.Replicas != 3 {
		t.Errorf("backend replicas = %v, want 3 (high availability)", be.Spec.Replicas)
	}

	sm := &unstructured.Unstructured{}
	sm.SetGroupVersionKind(serviceMonitorGVK)
	if err := cl.Get(ctx, types.NamespacedName{Name: "myshop", Namespace: "default"}, sm); err != nil {
		t.Errorf("missing ServiceMonitor: %v", err)
	}
}

// TestShopReconcile_RecreatesDeletedDeployment is a unit-level "chaos" check:
// deleting a managed Deployment causes the reconciler to recreate it.
func TestShopReconcile_RecreatesDeletedDeployment(t *testing.T) {
	ctx := context.Background()
	s := newScheme(t)
	registerExternalTypes(s)

	w := &shopv1alpha1.Wallet{
		ObjectMeta: objMeta("w", "default"),
		Spec:       shopv1alpha1.WalletSpec{Address: "0x52908400098527886e0f7030069857d2e4169ee7"},
	}
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&shopv1alpha1.Shop{}).
		WithObjects(w, newReconcileShop()).Build()
	r := &ShopReconciler{Client: cl, Scheme: s}

	reconcileN(t, r, "myshop", "default", 2)
	markDatabaseReady(t, cl, "myshop")
	reconcileN(t, r, "myshop", "default", 1)

	// Delete the backend Deployment, then reconcile: it must come back.
	be := &appsv1.Deployment{ObjectMeta: objMeta("myshop-backend", "default")}
	if err := cl.Delete(ctx, be); err != nil {
		t.Fatal(err)
	}
	reconcileN(t, r, "myshop", "default", 1)

	if err := cl.Get(ctx, types.NamespacedName{Name: "myshop-backend", Namespace: "default"}, &appsv1.Deployment{}); err != nil {
		t.Errorf("backend Deployment was not recreated: %v", err)
	}
}
