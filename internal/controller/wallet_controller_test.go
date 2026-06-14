package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	shopv1alpha1 "github.com/shophub-platform/shop-operator/api/v1alpha1"
	"github.com/shophub-platform/shop-operator/internal/wallet"
)

func TestWalletReconcile_Generate(t *testing.T) {
	t.Setenv("WALLET_ENCRYPTION_KEY", "test-pass")
	ctx := context.Background()
	s := newScheme(t)

	w := &shopv1alpha1.Wallet{
		ObjectMeta: objMeta("gen", "default"),
		Spec:       shopv1alpha1.WalletSpec{Network: shopv1alpha1.NetworkSepolia},
	}
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&shopv1alpha1.Wallet{}).
		WithObjects(w).Build()
	r := &WalletReconciler{Client: cl, Scheme: s}

	reconcileN(t, r, "gen", "default", 3) // 1: finalizer, 2: generate, 3: steady

	var got shopv1alpha1.Wallet
	if err := cl.Get(ctx, types.NamespacedName{Name: "gen", Namespace: "default"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != shopv1alpha1.WalletPhaseReady {
		t.Fatalf("phase = %q, want Ready", got.Status.Phase)
	}
	if got.Status.Address == "" || got.Status.EncryptedPrivateKeyRef == nil {
		t.Fatalf("missing address/keyRef: %+v", got.Status)
	}

	// The stored secret must decrypt to a 32-byte private key.
	var sec corev1.Secret
	if err := cl.Get(ctx, types.NamespacedName{Name: got.Status.EncryptedPrivateKeyRef.Name, Namespace: "default"}, &sec); err != nil {
		t.Fatalf("key secret: %v", err)
	}
	pt, err := wallet.Decrypt(sec.Data[encryptedKeyField], wallet.DeriveKey([]byte("test-pass")))
	if err != nil || len(pt) != 32 {
		t.Errorf("decrypt: err=%v len=%d", err, len(pt))
	}
}

func TestWalletReconcile_ProvidedAddress(t *testing.T) {
	ctx := context.Background()
	s := newScheme(t)

	w := &shopv1alpha1.Wallet{
		ObjectMeta: objMeta("prov", "default"),
		Spec:       shopv1alpha1.WalletSpec{Address: "0x52908400098527886e0f7030069857d2e4169ee7"},
	}
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&shopv1alpha1.Wallet{}).
		WithObjects(w).Build()
	r := &WalletReconciler{Client: cl, Scheme: s}

	reconcileN(t, r, "prov", "default", 2)

	var got shopv1alpha1.Wallet
	if err := cl.Get(ctx, types.NamespacedName{Name: "prov", Namespace: "default"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != shopv1alpha1.WalletPhaseReady {
		t.Fatalf("phase = %q, want Ready", got.Status.Phase)
	}
	if got.Status.Address != "0x52908400098527886E0F7030069857D2E4169EE7" {
		t.Errorf("address = %q, want checksummed form", got.Status.Address)
	}
	if got.Status.EncryptedPrivateKeyRef != nil {
		t.Error("provided-address wallet must not create a key secret")
	}
}

func TestWalletReconcile_DeleteCleansSecret(t *testing.T) {
	t.Setenv("WALLET_ENCRYPTION_KEY", "test-pass")
	ctx := context.Background()
	s := newScheme(t)

	w := &shopv1alpha1.Wallet{ObjectMeta: objMeta("del", "default")}
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&shopv1alpha1.Wallet{}).
		WithObjects(w).Build()
	r := &WalletReconciler{Client: cl, Scheme: s}

	reconcileN(t, r, "del", "default", 3) // generate -> secret created

	var got shopv1alpha1.Wallet
	if err := cl.Get(ctx, types.NamespacedName{Name: "del", Namespace: "default"}, &got); err != nil {
		t.Fatal(err)
	}
	secName := walletSecretName(&got)

	if err := cl.Delete(ctx, &got); err != nil {
		t.Fatal(err)
	}
	reconcileN(t, r, "del", "default", 1) // finalizer cleanup

	var sec corev1.Secret
	if err := cl.Get(ctx, types.NamespacedName{Name: secName, Namespace: "default"}, &sec); err == nil {
		t.Error("key secret should be deleted by finalizer cleanup")
	}
}
