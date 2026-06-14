package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	shopv1alpha1 "github.com/shophub-platform/shop-operator/api/v1alpha1"
)

// newScheme builds a scheme with the built-in and project types registered.
func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := shopv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

// registerExternalTypes teaches the scheme about the external operator CRDs as
// Unstructured so the fake client can track them (CNPG, Redis, monitors).
func registerExternalTypes(s *runtime.Scheme) {
	for _, gvk := range []schema.GroupVersionKind{cnpgClusterGVK, redisGVK, serviceMonitorGVK, podMonitorGVK} {
		s.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
		lgvk := gvk
		lgvk.Kind += "List"
		s.AddKnownTypeWithName(lgvk, &unstructured.UnstructuredList{})
	}
}

// reconcileN drives the reconciler n times for the given object.
func reconcileN(t *testing.T, r reconcile.Reconciler, name, ns string, n int) {
	t.Helper()
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: ns}}
	for i := 0; i < n; i++ {
		if _, err := r.Reconcile(context.Background(), req); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
	}
}

// objMeta is a small helper for building namespaced metadata.
func objMeta(name, ns string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: ns}
}
