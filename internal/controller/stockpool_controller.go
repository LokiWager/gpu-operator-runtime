/*
Copyright 2026.
*/

package controller

import (
	"context"
	"fmt"
	"reflect"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

// StockPoolReconciler reconciles a StockPool object.
type StockPoolReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=runtime.lokiwager.io,resources=stockpools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=runtime.lokiwager.io,resources=stockpools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=runtime.lokiwager.io,resources=stockpools/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile moves the observed cluster state toward StockPool spec.
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

	depName := deploymentNameForPool(pool.Name)
	var dep appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: pool.Namespace, Name: depName}, &dep); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		newDep := desiredDeployment(pool, desired)
		if err := controllerutil.SetControllerReference(&pool, newDep, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, newDep); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}

	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != desired {
		dep.Spec.Replicas = ptr.To(desired)
		if err := r.Update(ctx, &dep); err != nil {
			return ctrl.Result{}, err
		}
	}

	next := runtimev1alpha1.StockPoolStatus{
		Available:          dep.Status.AvailableReplicas,
		Allocated:          maxInt32(desired-dep.Status.AvailableReplicas, 0),
		ObservedGeneration: pool.Generation,
		LastSyncTime:       metav1.NewTime(time.Now().UTC()),
	}
	if desired == 0 {
		next.Phase = runtimev1alpha1.PhaseEmpty
	} else if dep.Status.AvailableReplicas >= desired {
		next.Phase = runtimev1alpha1.PhaseReady
	} else {
		next.Phase = runtimev1alpha1.PhaseProgressing
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

// SetupWithManager sets up the controller with the Manager.
func (r *StockPoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Scheme == nil {
		r.Scheme = mgr.GetScheme()
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.StockPool{}).
		Owns(&appsv1.Deployment{}).
		Named("stockpool").
		Complete(r)
}

func statusEqual(a, b runtimev1alpha1.StockPoolStatus) bool {
	aa := a
	bb := b
	aa.LastSyncTime = metav1.Time{}
	bb.LastSyncTime = metav1.Time{}
	return reflect.DeepEqual(aa, bb)
}

func desiredDeployment(pool runtimev1alpha1.StockPool, replicas int32) *appsv1.Deployment {
	name := deploymentNameForPool(pool.Name)
	labels := map[string]string{
		"app.kubernetes.io/name":       "gpu-runtime-stockpool",
		"app.kubernetes.io/managed-by": "gpu-runtime-operator",
		"runtime.lokiwager.io/pool":    pool.Name,
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: pool.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(replicas),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"runtime.lokiwager.io/pool": pool.Name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"runtime.lokiwager.io/pool": pool.Name}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name:            "runtime-placeholder",
					Image:           "busybox:1.36",
					ImagePullPolicy: corev1.PullIfNotPresent,
					Command:         []string{"sh", "-c", "sleep 3600"},
					Env:             []corev1.EnvVar{{Name: "SPEC_NAME", Value: pool.Spec.SpecName}, {Name: "POOL_NAME", Value: pool.Name}},
				}}},
			},
		},
	}
}

func deploymentNameForPool(poolName string) string {
	return fmt.Sprintf("pool-%s", poolName)
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
