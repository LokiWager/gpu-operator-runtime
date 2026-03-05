package controllers

import (
	"context"
	"reflect"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/pkg/operator/apis/runtime/v1alpha1"
)

type StockPoolReconciler struct {
	client.Client
}

func (r *StockPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var pool runtimev1alpha1.StockPool
	if err := r.Get(ctx, req.NamespacedName, &pool); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	desired := pool.Spec.Replicas
	if desired < 0 {
		desired = 0
	}

	next := runtimev1alpha1.StockPoolStatus{
		Available:          desired,
		Allocated:          0,
		ObservedGeneration: pool.Generation,
		LastSyncTime:       metav1.NewTime(time.Now().UTC()),
	}
	if desired == 0 {
		next.Phase = runtimev1alpha1.PhaseEmpty
	} else {
		next.Phase = runtimev1alpha1.PhaseReady
	}

	if statusEqual(pool.Status, next) {
		return ctrl.Result{}, nil
	}

	pool.Status = next
	if err := r.Status().Update(ctx, &pool); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *StockPoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.StockPool{}).
		Complete(r)
}

func statusEqual(a, b runtimev1alpha1.StockPoolStatus) bool {
	aa := a
	bb := b
	aa.LastSyncTime = metav1.Time{}
	bb.LastSyncTime = metav1.Time{}
	return reflect.DeepEqual(aa, bb)
}
