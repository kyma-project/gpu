/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gpuv1beta1 "github.com/kyma-project/gpu/api/v1beta1"
)

const (
	condDriverReady     = "DriverReady"
	condValidatorPassed = "ValidatorPassed"

	clusterPolicyName    = "cluster-policy"
	driverDaemonSetName  = "nvidia-driver-daemonset"
	gpuOperatorNamespace = "gpu-operator"
)

var clusterPolicyGVK = schema.GroupVersionKind{
	Group:   "nvidia.com",
	Version: "v1",
	Kind:    "ClusterPolicy",
}

// GpuStatusReconciler watches NVIDIA GPU Operator resources and syncs their
// health back onto the Gpu CR status. It runs independently of GpuReconciler
// so that install and status-sync concerns stay cleanly separated.
//
// Conditions managed:
//   - DriverReady:     nvidia-driver-daemonset is fully rolled out on all GPU nodes
//   - ValidatorPassed: ClusterPolicy reports state=ready (NVIDIA's own end-to-end check)
//
// Top-level state is computed from all four conditions (Preflight, HelmInstalled,
// DriverReady, ValidatorPassed) at the end of every sync:
//
//	all True  -> Ready
//	any False -> Error
//	otherwise -> Processing
type GpuStatusReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=nvidia.com,resources=clusterpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch

func (r *GpuStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Load the single Gpu CR - we use req.NamespacedName which is set by the enqueue
	// functions below to always point at the Gpu object, regardless of which watched
	// resource triggered the reconcile.
	gpu := &gpuv1beta1.Gpu{}
	if err := r.Get(ctx, req.NamespacedName, gpu); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching Gpu CR: %w", err)
	}

	if !gpu.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Only sync status once Helm has successfully installed.
	// Before that, DriverReady and ValidatorPassed are not meaningful.
	if !apimeta.IsStatusConditionTrue(gpu.Status.Conditions, condHelmInstalled) {
		return ctrl.Result{}, nil
	}

	driverReady, driverMsg := r.checkDriverDaemonSet(ctx)
	validatorPassed, validatorMsg := r.checkClusterPolicy(ctx)

	scratch := append([]metav1.Condition(nil), gpu.Status.Conditions...)
	setCondition(&scratch, condDriverReady, driverReady, driverMsg, gpu.Generation)
	setCondition(&scratch, condValidatorPassed, validatorPassed, validatorMsg, gpu.Generation)
	newState := computeState(scratch)

	if gpu.Status.State == newState &&
		conditionMatches(gpu.Status.Conditions, condDriverReady, driverReady) &&
		conditionMatches(gpu.Status.Conditions, condValidatorPassed, validatorPassed) {
		return ctrl.Result{RequeueAfter: requeueWarn}, nil
	}

	patch := client.MergeFrom(gpu.DeepCopy())
	gpu.Status.Conditions = scratch
	gpu.Status.State = newState

	if err := r.Status().Patch(ctx, gpu, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("patching Gpu status: %w", err)
	}

	logger.Info("status synced", "state", gpu.Status.State, "driverReady", driverReady, "validatorPassed", validatorPassed)
	return ctrl.Result{RequeueAfter: requeueWarn}, nil
}

// checkDriverDaemonSet returns true when the NVIDIA driver DaemonSet is fully rolled out.
func (r *GpuStatusReconciler) checkDriverDaemonSet(ctx context.Context) (bool, string) {
	ds := &appsv1.DaemonSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: driverDaemonSetName, Namespace: gpuOperatorNamespace}, ds); err != nil {
		if apierrors.IsNotFound(err) {
			return false, "nvidia-driver-daemonset not found; driver installation may still be in progress"
		}
		return false, fmt.Sprintf("error reading driver DaemonSet: %v", err)
	}

	desired := ds.Status.DesiredNumberScheduled
	ready := ds.Status.NumberReady
	available := ds.Status.NumberAvailable
	updated := ds.Status.UpdatedNumberScheduled

	if desired == 0 {
		return false, "driver DaemonSet has no scheduled pods; no GPU nodes may be present"
	}
	if ready < desired || available < desired || updated < desired {
		return false, fmt.Sprintf("driver DaemonSet: %d/%d nodes ready, %d/%d available, %d/%d updated", ready, desired, available, desired, updated, desired)
	}
	return true, fmt.Sprintf("driver DaemonSet: %d/%d nodes ready", ready, desired)
}

// checkClusterPolicy reads the NVIDIA ClusterPolicy status via unstructured client.
// ClusterPolicy.status.state is "ready" when NVIDIA's end-to-end validator has passed.
func (r *GpuStatusReconciler) checkClusterPolicy(ctx context.Context) (bool, string) {
	cp := &unstructured.Unstructured{}
	cp.SetGroupVersionKind(clusterPolicyGVK)

	if err := r.Get(ctx, types.NamespacedName{Name: clusterPolicyName}, cp); err != nil {
		if apierrors.IsNotFound(err) {
			return false, "ClusterPolicy not found; GPU Operator may still be starting"
		}
		return false, fmt.Sprintf("error reading ClusterPolicy: %v", err)
	}

	state, _, _ := unstructured.NestedString(cp.Object, "status", "state")
	switch state {
	case "ready":
		return true, "ClusterPolicy state is ready; NVIDIA validator passed"
	case "notReady":
		return false, "ClusterPolicy state is notReady; NVIDIA validator has not passed yet"
	case "ignored":
		// "ignored" means the operator decided this policy doesn't apply — treat as not ready
		return false, "ClusterPolicy state is ignored"
	default:
		return false, fmt.Sprintf("ClusterPolicy state is %q; waiting for ready", state)
	}
}

// computeState derives the top-level Gpu state from all four managed conditions.
// Any False -> Error (checked first). All True -> Ready. Otherwise -> Processing.
func computeState(conditions []metav1.Condition) gpuv1beta1.GpuState {
	managed := []string{condPreflight, condHelmInstalled, condDriverReady, condValidatorPassed}

	allTrue := true
	for _, t := range managed {
		c := apimeta.FindStatusCondition(conditions, t)
		if c != nil && c.Status == metav1.ConditionFalse {
			return gpuv1beta1.GpuStateError
		}
		if c == nil || c.Status != metav1.ConditionTrue {
			allTrue = false
		}
	}
	if allTrue {
		return gpuv1beta1.GpuStateReady
	}
	return gpuv1beta1.GpuStateProcessing
}

// conditionMatches returns true if the named condition already has the expected boolean status.
// Used to avoid a no-op status patch when nothing changed.
func conditionMatches(conditions []metav1.Condition, condType string, want bool) bool {
	c := apimeta.FindStatusCondition(conditions, condType)
	if c == nil {
		return false
	}
	if want {
		return c.Status == metav1.ConditionTrue
	}
	return c.Status == metav1.ConditionFalse
}

// setCondition is a thin wrapper around apimeta.SetStatusCondition that infers
// Status and Reason from the boolean so call sites stay readable.
func setCondition(conditions *[]metav1.Condition, condType string, ok bool, message string, generation int64) {
	status := metav1.ConditionTrue
	reason := "Ready"
	if !ok {
		status = metav1.ConditionFalse
		reason = "NotReady"
	}
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: generation,
	})
}

// SetupWithManager registers the status reconciler and wires up watches on
// ClusterPolicy and the driver DaemonSet. Both enqueue all existing Gpu CRs
// so no hardcoded name is needed — works regardless of what the CR is called.
func (r *GpuStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	enqueueGpu := handler.EnqueueRequestsFromMapFunc(
		func(ctx context.Context, _ client.Object) []reconcile.Request {
			var list gpuv1beta1.GpuList
			if err := r.List(ctx, &list); err != nil {
				log.FromContext(ctx).Error(err, "failed to list Gpu CRs; watch event will be lost")
				return nil
			}
			reqs := make([]reconcile.Request, len(list.Items))
			for i, gpu := range list.Items {
				reqs[i] = reconcile.Request{
					NamespacedName: types.NamespacedName{Name: gpu.Name},
				}
			}
			return reqs
		},
	)

	clusterPolicyObject := &unstructured.Unstructured{}
	clusterPolicyObject.SetGroupVersionKind(clusterPolicyGVK)

	return ctrl.NewControllerManagedBy(mgr).
		For(&gpuv1beta1.Gpu{}).
		Named("gpu-status").
		Watches(
			clusterPolicyObject,
			enqueueGpu,
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		Watches(
			&appsv1.DaemonSet{},
			enqueueGpu,
			builder.WithPredicates(
				predicate.NewPredicateFuncs(func(obj client.Object) bool {
					return obj.GetName() == driverDaemonSetName &&
						obj.GetNamespace() == gpuOperatorNamespace
				}),
			),
		).
		Complete(r)
}
