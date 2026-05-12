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
	"strings"

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
//   - DriverReady:     True when nvidia-driver-daemonset is fully rolled out on all GPU nodes
//   - ValidatorPassed: True when ClusterPolicy reports state=ready (NVIDIA's end-to-end check)
//
// Both conditions use Unknown while converging and False only for hard read errors.
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

	driverStatus, driverReason, driverMsg, driverInfo := r.checkDriverDaemonSet(ctx)
	validatorStatus, validatorReason, validatorMsg := r.checkClusterPolicy(ctx)

	scratch := append([]metav1.Condition(nil), gpu.Status.Conditions...)
	setCondition(&scratch, condDriverReady, driverStatus, driverReason, driverMsg, gpu.Generation)
	setCondition(&scratch, condValidatorPassed, validatorStatus, validatorReason, validatorMsg, gpu.Generation)
	apimeta.SetStatusCondition(&scratch, computeReadySummary(scratch, gpu.Generation))

	if conditionMatches(gpu.Status.Conditions, condDriverReady, driverStatus, driverReason, driverMsg) &&
		conditionMatches(gpu.Status.Conditions, condValidatorPassed, validatorStatus, validatorReason, validatorMsg) &&
		driverStatusMatches(gpu.Status.Driver, driverInfo) {
		return ctrl.Result{RequeueAfter: requeueWarn}, nil
	}

	patch := client.MergeFrom(gpu.DeepCopy())
	gpu.Status.Conditions = scratch
	gpu.Status.Driver = driverInfo

	if err := r.Status().Patch(ctx, gpu, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("patching Gpu status: %w", err)
	}

	logger.Info("status synced", "driverReady", driverStatus, "validatorPassed", validatorStatus)
	return ctrl.Result{RequeueAfter: requeueWarn}, nil
}

// checkDriverDaemonSet returns the condition status, reason, message, and observed
// driver info for DriverReady.
// Unknown = DaemonSet not yet present or pods still rolling out (outcome undetermined).
// False   = DaemonSet exists but is definitively not healthy (e.g. read error).
// True    = all nodes ready, available, and updated.
func (r *GpuStatusReconciler) checkDriverDaemonSet(ctx context.Context) (metav1.ConditionStatus, string, string, *gpuv1beta1.DriverStatus) {
	ds := &appsv1.DaemonSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: driverDaemonSetName, Namespace: gpuOperatorNamespace}, ds); err != nil {
		if apierrors.IsNotFound(err) {
			return metav1.ConditionUnknown, reasonWaiting, "nvidia-driver-daemonset not found; driver installation may still be in progress", nil
		}
		return metav1.ConditionFalse, reasonReadError, fmt.Sprintf("error reading driver DaemonSet: %v", err), nil
	}

	desired := ds.Status.DesiredNumberScheduled
	ready := ds.Status.NumberReady
	available := ds.Status.NumberAvailable
	updated := ds.Status.UpdatedNumberScheduled

	driverInfo := &gpuv1beta1.DriverStatus{
		NodesReady: ready,
		Version:    driverVersionFromDaemonSet(ds),
	}

	if desired == 0 {
		return metav1.ConditionUnknown, reasonWaiting, "driver DaemonSet has no scheduled pods; no GPU nodes may be present", driverInfo
	}
	if ready < desired || available < desired || updated < desired {
		return metav1.ConditionUnknown, reasonProgressing, fmt.Sprintf("driver DaemonSet: %d/%d nodes ready, %d/%d available, %d/%d updated", ready, desired, available, desired, updated, desired), driverInfo
	}
	return metav1.ConditionTrue, reasonReady, fmt.Sprintf("driver DaemonSet: %d/%d nodes ready", ready, desired), driverInfo
}

// checkClusterPolicy reads the NVIDIA ClusterPolicy status via unstructured client.
// Unknown = ClusterPolicy not yet present or still converging.
// False   = read error or ClusterPolicy in a terminal non-ready state.
// True    = ClusterPolicy.status.state is "ready" (NVIDIA's end-to-end validator passed).
func (r *GpuStatusReconciler) checkClusterPolicy(ctx context.Context) (metav1.ConditionStatus, string, string) {
	cp := &unstructured.Unstructured{}
	cp.SetGroupVersionKind(clusterPolicyGVK)

	if err := r.Get(ctx, types.NamespacedName{Name: clusterPolicyName}, cp); err != nil {
		if apierrors.IsNotFound(err) {
			return metav1.ConditionUnknown, reasonWaiting, "ClusterPolicy not found; GPU Operator may still be starting"
		}
		return metav1.ConditionFalse, reasonReadError, fmt.Sprintf("error reading ClusterPolicy: %v", err)
	}

	state, _, _ := unstructured.NestedString(cp.Object, "status", "state")
	switch state {
	case "ready":
		return metav1.ConditionTrue, reasonReady, "ClusterPolicy state is ready; NVIDIA validator passed"
	case "notReady":
		return metav1.ConditionUnknown, reasonProgressing, "ClusterPolicy state is notReady; NVIDIA validator has not passed yet"
	case "ignored":
		// "ignored" means the operator decided this policy doesn't apply
		return metav1.ConditionUnknown, reasonWaiting, "ClusterPolicy state is ignored"
	default:
		return metav1.ConditionUnknown, reasonProgressing, fmt.Sprintf("ClusterPolicy state is %q; waiting for ready", state)
	}
}

// driverVersionFromDaemonSet extracts the driver version from the DaemonSet's first
// container image tag (e.g. "nvcr.io/nvidia/driver:535.129.03-ubuntu22.04" -> "535.129.03").
// This reads from Spec (the desired image), not from individual pod statuses - it reports
// the version the cluster is converging toward. During a rolling update this will show the
// new version while nodesReady < desired, giving users a clear picture of progress.
// Returns empty string if the image tag is absent or unparseable.
func driverVersionFromDaemonSet(ds *appsv1.DaemonSet) string {
	if len(ds.Spec.Template.Spec.Containers) == 0 {
		return ""
	}
	image := ds.Spec.Template.Spec.Containers[0].Image
	if idx := strings.LastIndex(image, ":"); idx >= 0 {
		tag := image[idx+1:]
		// Tags are formatted as "<version>-<os>" (e.g. "535.129.03-ubuntu22.04").
		// Strip the OS suffix if present.
		if dashIdx := strings.Index(tag, "-"); dashIdx >= 0 {
			return tag[:dashIdx]
		}
		return tag
	}
	return ""
}

// driverStatusMatches returns true when the existing driver status already reflects
// the observed values, used to skip a no-op status patch.
func driverStatusMatches(existing *gpuv1beta1.DriverStatus, observed *gpuv1beta1.DriverStatus) bool {
	if observed == nil {
		return existing == nil
	}
	if existing == nil {
		return false
	}
	return existing.Version == observed.Version && existing.NodesReady == observed.NodesReady
}

// SetupWithManager registers the status reconciler and wires up watches on
// ClusterPolicy and the driver DaemonSet. Both enqueue all existing Gpu CRs
// so no hardcoded name is needed - works regardless of what the CR is called.
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
