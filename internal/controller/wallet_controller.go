package controller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	shopv1alpha1 "github.com/shophub-platform/shop-operator/api/v1alpha1"
)

// WalletReconciler reconciles a Wallet object.
type WalletReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=shop.shophub.io,resources=wallets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=shop.shophub.io,resources=wallets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=shop.shophub.io,resources=wallets/finalizers,verbs=update

// Reconcile is the F1 skeleton: it only logs detected changes.
func (r *WalletReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var w shopv1alpha1.Wallet
	if err := r.Get(ctx, req.NamespacedName, &w); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Wallet deleted", "wallet", req.NamespacedName.String())
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch Wallet")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	generate := w.Spec.Address == ""
	logger.Info("Reconciling wallet",
		"wallet", w.Name,
		"namespace", w.Namespace,
		"network", w.Spec.Network,
		"addressProvided", !generate,
		"generation", w.Generation,
	)

	// F1: account generation not implemented yet.
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WalletReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&shopv1alpha1.Wallet{}).
		Named("wallet").
		Complete(r)
}
