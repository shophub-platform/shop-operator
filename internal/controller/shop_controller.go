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

// ShopReconciler reconciles a Shop object.
type ShopReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=shop.shophub.io,resources=shops,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=shop.shophub.io,resources=shops/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=shop.shophub.io,resources=shops/finalizers,verbs=update

// Reconcile is the F1 skeleton: it only logs detected changes and computes
// the desired replica count. No child resources are created yet.
func (r *ShopReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var shop shopv1alpha1.Shop
	if err := r.Get(ctx, req.NamespacedName, &shop); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Shop deleted", "shop", req.NamespacedName.String())
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch Shop")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	desiredReplicas := shopv1alpha1.ReplicasForAvailability(shop.Spec.Availability)
	logger.Info("Reconciling shop",
		"shop", shop.Name,
		"namespace", shop.Namespace,
		"availability", shop.Spec.Availability,
		"databaseType", shop.Spec.DatabaseType,
		"walletRef", shop.Spec.WalletRef.Name,
		"desiredReplicas", desiredReplicas,
		"generation", shop.Generation,
	)

	// F1: resource creation not implemented yet.
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ShopReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&shopv1alpha1.Shop{}).
		Named("shop").
		Complete(r)
}
