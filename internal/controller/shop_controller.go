package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
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
)

// requeueDelay is how long we wait before re-checking a dependency that is not
// yet ready (database/discord provisioning, deployments rolling out).
const requeueDelay = 15 * time.Second

// ShopReconciler reconciles a Shop object.
type ShopReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=shop.shophub.io,resources=shops,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=shop.shophub.io,resources=shops/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=shop.shophub.io,resources=shops/finalizers,verbs=update
// +kubebuilder:rbac:groups=shop.shophub.io,resources=discordchannels;wallets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;configmaps;secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=postgresql.cnpg.io,resources=clusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=redis.redis.opstreelabs.in,resources=redis,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors;podmonitors,verbs=get;list;watch;create;update;patch;delete

// Reconcile implements the F5 (spec 10.1) Shop reconciliation logic.
func (r *ShopReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. Fetch the Shop CR. If it no longer exists, nothing to do (children are
	//    garbage-collected via owner references).
	var shop shopv1alpha1.Shop
	if err := r.Get(ctx, req.NamespacedName, &shop); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Shop deleted", "shop", req.NamespacedName.String())
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Derive the replica count from availability and persist it on the spec for
	// visibility (printcolumn). A spec change re-triggers reconciliation.
	desired := shopv1alpha1.ReplicasForAvailability(shop.Spec.Availability)
	if shop.Spec.Replicas == nil || *shop.Spec.Replicas != desired {
		shop.Spec.Replicas = &desired
		if err := r.Update(ctx, &shop); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Pre-fill the status fields that are always known.
	shop.Status.ReplicaCount = desired
	shop.Status.URL = shopURL(&shop)
	shop.Status.ObservedGeneration = shop.Generation

	// 2. Validate the spec: the database type must be supported and the
	//    referenced Wallet must exist.
	if shop.Spec.DatabaseType != shopv1alpha1.DatabasePostgres &&
		shop.Spec.DatabaseType != shopv1alpha1.DatabaseRedis {
		return r.fail(ctx, &shop, "UnsupportedDatabase",
			fmt.Errorf("unsupported databaseType %q", shop.Spec.DatabaseType))
	}

	walletAddr, walletFound, err := r.resolveWallet(ctx, &shop)
	if err != nil {
		return r.fail(ctx, &shop, "WalletLookupFailed", err)
	}
	if !walletFound {
		r.setCondition(&shop, shopv1alpha1.ConditionWalletReady, metav1.ConditionFalse,
			"WalletNotFound", "referenced Wallet "+shop.Spec.WalletRef.Name+" not found")
		return r.pending(ctx, &shop, "waiting for Wallet "+shop.Spec.WalletRef.Name)
	}
	r.setCondition(&shop, shopv1alpha1.ConditionWalletReady, metav1.ConditionTrue,
		"WalletResolved", "wallet address resolved")

	// 3. Reconcile the DiscordChannel. Best-effort by default; strict gate when
	//    REQUIRE_DISCORD=true (see reconcileDiscordChannel).
	webhookURL, discordReady, err := r.reconcileDiscordChannel(ctx, &shop)
	if err != nil {
		return r.fail(ctx, &shop, "DiscordReconcileFailed", err)
	}
	if discordReady {
		r.setCondition(&shop, shopv1alpha1.ConditionDiscordReady, metav1.ConditionTrue,
			"ChannelReady", "discord channel ready")
	} else {
		r.setCondition(&shop, shopv1alpha1.ConditionDiscordReady, metav1.ConditionFalse,
			"Provisioning", "waiting for DiscordChannel to become Ready")
		if requireDiscord() {
			return r.pending(ctx, &shop, "waiting for DiscordChannel Ready")
		}
	}

	// 4. Reconcile the database (CNPG Cluster or Redis CR). Block until Ready.
	db, err := r.reconcileDatabase(ctx, &shop, desired)
	if err != nil {
		return r.fail(ctx, &shop, "DatabaseReconcileFailed", err)
	}
	if !db.Ready {
		r.setCondition(&shop, shopv1alpha1.ConditionDatabaseReady, metav1.ConditionFalse,
			"Provisioning", "waiting for database to become Ready")
		return r.pending(ctx, &shop, "waiting for "+db.Type+" database Ready")
	}
	r.setCondition(&shop, shopv1alpha1.ConditionDatabaseReady, metav1.ConditionTrue,
		"DatabaseReady", "database is healthy")

	// 5. ConfigMap with the shop application configuration.
	cmData := map[string]string{
		"SHOP_NAME":      shop.Name,
		"APP_ENV":        "production",
		"SERVER_PORT":    fmt.Sprintf("%d", backendPort),
		"DB_TYPE":        db.Type,
		"DB_HOST":        db.Host,
		"DB_PORT":        fmt.Sprintf("%d", db.Port),
		"WALLET_ADDRESS": walletAddr,
	}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: configMapName(&shop), Namespace: shop.Namespace}}
	if err := r.applyOwned(ctx, &shop, cm, func() { buildConfigMap(cm, &shop, cmData) }); err != nil {
		return r.fail(ctx, &shop, "ConfigMapFailed", err)
	}

	// 6. Secret with the DB connection string and webhook URL.
	secData := map[string]string{
		"DATABASE_URL":        db.ConnString,
		"DISCORD_WEBHOOK_URL": webhookURL,
	}
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName(&shop), Namespace: shop.Namespace}}
	if err := r.applyOwned(ctx, &shop, sec, func() { buildSecret(sec, &shop, secData) }); err != nil {
		return r.fail(ctx, &shop, "SecretFailed", err)
	}

	// 7. Backend Deployment.
	beDep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: backendName(&shop), Namespace: shop.Namespace}}
	if err := r.applyOwned(ctx, &shop, beDep, func() { buildBackendDeployment(beDep, &shop, desired) }); err != nil {
		return r.fail(ctx, &shop, "BackendDeploymentFailed", err)
	}

	// 8. Frontend Deployment.
	feDep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: frontendName(&shop), Namespace: shop.Namespace}}
	if err := r.applyOwned(ctx, &shop, feDep, func() { buildFrontendDeployment(feDep, &shop, desired) }); err != nil {
		return r.fail(ctx, &shop, "FrontendDeploymentFailed", err)
	}

	// 9. Services (ClusterIP) + Ingress.
	beSvc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: backendName(&shop), Namespace: shop.Namespace}}
	if err := r.applyOwned(ctx, &shop, beSvc, func() { buildService(beSvc, &shop, "backend", backendPort) }); err != nil {
		return r.fail(ctx, &shop, "BackendServiceFailed", err)
	}
	feSvc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: frontendName(&shop), Namespace: shop.Namespace}}
	if err := r.applyOwned(ctx, &shop, feSvc, func() { buildService(feSvc, &shop, "frontend", frontendPort) }); err != nil {
		return r.fail(ctx, &shop, "FrontendServiceFailed", err)
	}
	ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: ingressName(&shop), Namespace: shop.Namespace}}
	if err := r.applyOwned(ctx, &shop, ing, func() { buildIngress(ing, &shop) }); err != nil {
		return r.fail(ctx, &shop, "IngressFailed", err)
	}

	// 10. ServiceMonitor + PodMonitor.
	if err := r.reconcileMonitors(ctx, &shop); err != nil {
		return r.fail(ctx, &shop, "MonitorsFailed", err)
	}

	// 11. Grafana dashboard ConfigMap.
	dash := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: dashboardName(&shop), Namespace: shop.Namespace}}
	if err := r.applyOwned(ctx, &shop, dash, func() { buildGrafanaDashboard(dash, &shop) }); err != nil {
		return r.fail(ctx, &shop, "DashboardFailed", err)
	}

	// 12. Owner references are set on every child inside applyOwned /
	//     upsertUnstructured, enabling cascading garbage collection.

	// 13. Update status: reflect backend readiness and overall phase.
	var liveBE appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Name: backendName(&shop), Namespace: shop.Namespace}, &liveBE); err == nil {
		shop.Status.ReadyReplicas = liveBE.Status.ReadyReplicas
	}

	if shop.Status.ReadyReplicas >= desired {
		shop.Status.Phase = shopv1alpha1.ShopPhaseReady
		r.setCondition(&shop, shopv1alpha1.ConditionReady, metav1.ConditionTrue,
			"AllResourcesReady", "shop is fully provisioned")
		if err := r.Status().Update(ctx, &shop); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("Shop ready", "shop", shop.Name, "url", shop.Status.URL, "replicas", desired)
		return ctrl.Result{}, nil
	}

	shop.Status.Phase = shopv1alpha1.ShopPhaseProvisioning
	r.setCondition(&shop, shopv1alpha1.ConditionReady, metav1.ConditionFalse,
		"Provisioning", "waiting for backend replicas to become ready")
	if err := r.Status().Update(ctx, &shop); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeueDelay}, nil
}

// applyOwned creates or updates a typed child object and stamps it with the
// Shop owner reference for garbage collection.
func (r *ShopReconciler) applyOwned(
	ctx context.Context, shop *shopv1alpha1.Shop, obj client.Object, mutate func(),
) error {
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, obj, func() error {
		mutate()
		return controllerutil.SetControllerReference(shop, obj, r.Scheme)
	})
	return err
}

// setCondition upserts a status condition with the current observed generation.
func (r *ShopReconciler) setCondition(
	shop *shopv1alpha1.Shop, condType string, status metav1.ConditionStatus, reason, msg string,
) {
	meta.SetStatusCondition(&shop.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: shop.Generation,
	})
}

// pending records a provisioning state and requeues.
func (r *ShopReconciler) pending(ctx context.Context, shop *shopv1alpha1.Shop, msg string) (ctrl.Result, error) {
	if shop.Status.Phase != shopv1alpha1.ShopPhaseReady {
		shop.Status.Phase = shopv1alpha1.ShopPhaseProvisioning
	}
	r.setCondition(shop, shopv1alpha1.ConditionReady, metav1.ConditionFalse, "Provisioning", msg)
	if err := r.Status().Update(ctx, shop); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeueDelay}, nil
}

// fail records a failed state and returns the error to trigger backoff.
func (r *ShopReconciler) fail(ctx context.Context, shop *shopv1alpha1.Shop, reason string, err error) (ctrl.Result, error) {
	shop.Status.Phase = shopv1alpha1.ShopPhaseFailed
	r.setCondition(shop, shopv1alpha1.ConditionReady, metav1.ConditionFalse, reason, err.Error())
	_ = r.Status().Update(ctx, shop)
	return ctrl.Result{}, err
}

// SetupWithManager sets up the controller with the Manager and watches the
// child resources it owns.
func (r *ShopReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&shopv1alpha1.Shop{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
		Owns(&networkingv1.Ingress{}).
		Owns(&shopv1alpha1.DiscordChannel{}).
		Named("shop").
		Complete(r)
}
