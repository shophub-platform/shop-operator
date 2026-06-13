package controller

import (
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	shopv1alpha1 "github.com/shophub-platform/shop-operator/api/v1alpha1"
	"github.com/shophub-platform/shop-operator/internal/wallet"
)

const (
	// walletFinalizer guards deletion of the encrypted-key Secret.
	walletFinalizer = "shop.shophub.io/wallet-cleanup"
	// walletEncKeySecretName is the Secret (in the operator namespace) holding
	// the AES passphrase used to encrypt generated private keys.
	walletEncKeySecretName = "wallet-encryption-key"
	// walletEncKeyEnv overrides the passphrase via environment variable.
	walletEncKeyEnv = "WALLET_ENCRYPTION_KEY"
	// walletKeySecretSuffix + name is the Secret holding the encrypted key.
	walletKeySecretSuffix = "-wallet-key"
	// encryptedKeyField is the data key under which the ciphertext is stored.
	encryptedKeyField = "encrypted-private-key"
)

// WalletReconciler reconciles a Wallet object.
type WalletReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=shop.shophub.io,resources=wallets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=shop.shophub.io,resources=wallets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=shop.shophub.io,resources=wallets/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile implements the F5 (spec 10.3) Wallet reconciliation logic: validate
// a provided address, or generate a new key pair, encrypt the private key with
// AES-GCM into a Secret, and clean up that Secret on deletion via a finalizer.
func (r *WalletReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var w shopv1alpha1.Wallet
	if err := r.Get(ctx, req.NamespacedName, &w); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Deletion: drop the encrypted-key Secret, then remove the finalizer.
	if !w.ObjectMeta.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&w, walletFinalizer) {
			if err := r.cleanup(ctx, &w); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&w, walletFinalizer)
			if err := r.Update(ctx, &w); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure the finalizer is present before creating any Secret.
	if !controllerutil.ContainsFinalizer(&w, walletFinalizer) {
		controllerutil.AddFinalizer(&w, walletFinalizer)
		if err := r.Update(ctx, &w); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Idempotent: nothing to do once resolved.
	if w.Status.Phase == shopv1alpha1.WalletPhaseReady && w.Status.Address != "" {
		return ctrl.Result{}, nil
	}

	// Case 1: an address was provided — validate its format and record it.
	if w.Spec.Address != "" {
		norm, ok := wallet.NormalizeAddress(w.Spec.Address)
		if !ok {
			return r.fail(ctx, &w, "InvalidAddress",
				fmt.Errorf("invalid ethereum address %q", w.Spec.Address))
		}
		w.Status.Address = norm
		w.Status.Phase = shopv1alpha1.WalletPhaseReady
		r.setReadyCondition(&w, metav1.ConditionTrue, "AddressValidated", "provided address is valid")
		if err := r.Status().Update(ctx, &w); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("Wallet ready (provided address)", "wallet", w.Name, "address", norm)
		return ctrl.Result{}, nil
	}

	// Case 2: no address — generate a key pair and store the encrypted key.
	acct, err := wallet.GenerateAccount()
	if err != nil {
		return r.fail(ctx, &w, "KeyGenerationFailed", err)
	}

	encKey, err := r.encryptionKey(ctx)
	if err != nil {
		return r.fail(ctx, &w, "EncryptionKeyUnavailable", err)
	}
	ciphertext, err := wallet.Encrypt(acct.PrivateKey, encKey)
	if err != nil {
		return r.fail(ctx, &w, "EncryptFailed", err)
	}

	if err := r.storeKeySecret(ctx, &w, ciphertext); err != nil {
		return r.fail(ctx, &w, "SecretFailed", err)
	}

	w.Status.Address = acct.Address
	w.Status.EncryptedPrivateKeyRef = &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: walletSecretName(&w)},
		Key:                  encryptedKeyField,
	}
	w.Status.Phase = shopv1alpha1.WalletPhaseReady
	r.setReadyCondition(&w, metav1.ConditionTrue, "AccountGenerated", "generated key pair and stored encrypted private key")
	if err := r.Status().Update(ctx, &w); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("Wallet ready (generated)", "wallet", w.Name, "address", acct.Address)
	return ctrl.Result{}, nil
}

// cleanup deletes the encrypted-key Secret (provided-address wallets have none).
func (r *WalletReconciler) cleanup(ctx context.Context, w *shopv1alpha1.Wallet) error {
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      walletSecretName(w),
		Namespace: w.Namespace,
	}}
	if err := r.Delete(ctx, sec); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// storeKeySecret creates/updates the Secret holding the AES-GCM ciphertext.
func (r *WalletReconciler) storeKeySecret(ctx context.Context, w *shopv1alpha1.Wallet, ciphertext []byte) error {
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      walletSecretName(w),
		Namespace: w.Namespace,
	}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sec, func() error {
		sec.Type = corev1.SecretTypeOpaque
		sec.Data = map[string][]byte{encryptedKeyField: ciphertext}
		return controllerutil.SetControllerReference(w, sec, r.Scheme)
	})
	return err
}

// encryptionKey resolves the AES passphrase from env or the operator-namespace
// Secret and derives a 32-byte key.
func (r *WalletReconciler) encryptionKey(ctx context.Context) ([]byte, error) {
	if v := os.Getenv(walletEncKeyEnv); v != "" {
		return wallet.DeriveKey([]byte(v)), nil
	}
	ns := operatorNamespace()
	var sec corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Name: walletEncKeySecretName, Namespace: ns}, &sec); err != nil {
		return nil, fmt.Errorf("read %s/%s: %w (set %s env or create the secret)",
			ns, walletEncKeySecretName, err, walletEncKeyEnv)
	}
	for _, k := range []string{"key", "passphrase"} {
		if v, ok := sec.Data[k]; ok && len(v) > 0 {
			return wallet.DeriveKey(v), nil
		}
	}
	return nil, fmt.Errorf("secret %s/%s has no key (key|passphrase)", ns, walletEncKeySecretName)
}

func (r *WalletReconciler) fail(ctx context.Context, w *shopv1alpha1.Wallet, reason string, err error) (ctrl.Result, error) {
	w.Status.Phase = shopv1alpha1.WalletPhaseFailed
	r.setReadyCondition(w, metav1.ConditionFalse, reason, err.Error())
	_ = r.Status().Update(ctx, w)
	return ctrl.Result{}, err
}

func (r *WalletReconciler) setReadyCondition(w *shopv1alpha1.Wallet, status metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&w.Status.Conditions, metav1.Condition{
		Type:               shopv1alpha1.ConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: w.Generation,
	})
}

// walletSecretName returns <name>-wallet-key.
func walletSecretName(w *shopv1alpha1.Wallet) string {
	return w.Name + walletKeySecretSuffix
}

// SetupWithManager sets up the controller with the Manager.
func (r *WalletReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&shopv1alpha1.Wallet{}).
		Owns(&corev1.Secret{}).
		Named("wallet").
		Complete(r)
}
