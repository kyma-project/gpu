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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gpuv1beta1 "github.com/kyma-project/gpu/api/v1beta1"
	"github.com/kyma-project/gpu/internal/chart"
	"github.com/kyma-project/gpu/internal/detection"
	"github.com/kyma-project/gpu/internal/helm"
)

const (
	requeueWarn          = 30 * time.Second
	finalizer            = "gpu.kyma-project.io/gpu-operator"
	gpuOperatorNamespace = "gpu-operator"
	driverAppLabel       = "nvidia-driver-daemonset"
	clusterPolicyName    = "cluster-policy"
	expectedCRName       = "gpu"
)

var clusterPolicyGVK = schema.GroupVersionKind{
	Group:   "nvidia.com",
	Version: "v1",
	Kind:    "ClusterPolicy",
}

// GpuReconciler reconciles a Gpu object. It owns the full lifecycle: install/upgrade
// of the embedded NVIDIA GPU Operator chart, deletion, and observation of NVIDIA
// resources (driver DaemonSet, ClusterPolicy) to maintain status conditions.
type GpuReconciler struct {
	client.Client
	Installer helm.Installer
}

// RBAC design note
//
// Permissions are scoped to exactly what the operator's reconciliation logic and the
// embedded NVIDIA GPU Operator Helm chart require. A wildcard grant (*/*) is intentionally
// avoided - see internal/chart/rbac_test.go (TestChartResourcesCoveredByRBAC) which fails
// CI if the chart produces a resource type not covered by these markers.
//
// The rbac.authorization.k8s.io grant (clusterroles, clusterrolebindings, roles, rolebindings)
// is a necessary consequence of using the Helm SDK: the NVIDIA GPU Operator chart creates
// ServiceAccounts and RBAC for its own components (gpu-operator, NFD, device-plugin, etc.)
// and Helm must apply those during install and upgrade. Without this grant, Helm fails with
// a 403 when applying chart resources.
//
// The escalate and bind verbs are required because the NVIDIA GPU Operator chart creates
// ClusterRoles (e.g. gpu-operator, node-feature-discovery-worker) that include permissions
// the controller itself holds. Kubernetes blocks granting permissions not currently held by
// the caller unless escalate is present; bind is required to create ClusterRoleBindings that
// reference those roles. Without these verbs, Helm fails with a 403 during chart install.
// See: https://kubernetes.io/docs/reference/access-authn-authz/rbac/#privilege-escalation-prevention-and-bootstrapping

// +kubebuilder:rbac:groups=gpu.kyma-project.io,resources=gpus,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gpu.kyma-project.io,resources=gpus/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gpu.kyma-project.io,resources=gpus/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts;configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments;daemonsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings;roles;rolebindings,verbs=get;list;watch;create;update;patch;delete;bind;escalate
// +kubebuilder:rbac:groups=nvidia.com,resources=clusterpolicies;nvidiadrivers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=nfd.k8s-sigs.io,resources=nodefeaturerules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=podmonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=security.openshift.io,resources=securitycontextconstraints,verbs=get;list;watch;create;update;patch;delete

// Reconcile drives a single sync cycle for a Gpu CR.
//
// Flow (happy path):
//  1. Add finalizer if missing (return; the resulting watch event re-triggers).
//  2. Pre-flight check (supported, homogeneous OS across all GPU nodes).
//  3. Helm install or upgrade of the embedded NVIDIA GPU Operator chart.
//  4. Read driver DaemonSet -> DriverReady condition.
//  5. Read ClusterPolicy -> ValidatorPassed condition.
//  6. Compute Ready summary and apply all owned status fields via Server-Side Apply.
//
// Each early exit (preflight Warn/Error, helm failure) writes the conditions known
// at that point and returns; status checks only run once Helm has succeeded.
func (r *GpuReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	gpu := &gpuv1beta1.Gpu{}
	if err := r.Get(ctx, req.NamespacedName, gpu); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching Gpu CR: %w", err)
	}

	if !gpu.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, gpu)
	}

	// Singleton enforcement (defense-in-depth; CEL is the primary gate).
	if gpu.Name != expectedCRName {
		if err := r.applyStatus(ctx, gpu.Name, statusUpdate{
			conditions: []metav1.Condition{{
				Type:    condReady,
				Status:  metav1.ConditionFalse,
				Reason:  reasonForbiddenName,
				Message: fmt.Sprintf("only a singleton Gpu CR named %q is reconciled; this CR is ignored", expectedCRName),
			}},
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(gpu, finalizer) {
		controllerutil.AddFinalizer(gpu, finalizer)
		if err := r.Update(ctx, gpu); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
		// Update generates a watch event that re-triggers reconcile - no explicit requeue needed.
		return ctrl.Result{}, nil
	}

	// 1. preflight
	preflightCond, detectedOS, result, err := r.runPreflight(ctx, gpu)
	if err != nil || preflightCond == nil {
		return result, err
	}

	// 2. helm install or upgrade
	helmCond, chartVersion, err := r.installOrUpgrade(ctx, gpu, preflightCond, detectedOS)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 3. check driver DaemonSet -> set DriverReady condition
	driverStatus, driverReason, driverMsg, driverInfo := r.checkDriverDaemonSet(ctx)
	driverCond := metav1.Condition{Type: condDriverReady, Status: driverStatus, Reason: driverReason, Message: driverMsg}

	// 4. check ClusterPolicy -> set ValidatorPassed condition
	validatorStatus, validatorReason, validatorMsg := r.checkClusterPolicy(ctx)
	validatorCond := metav1.Condition{Type: condValidatorPassed, Status: validatorStatus, Reason: validatorReason, Message: validatorMsg}

	// 5. apply all conditions + Ready summary + observed fields
	if err := r.applyStatus(ctx, gpu.Name, statusUpdate{
		conditions:      []metav1.Condition{*preflightCond, helmCond, driverCond, validatorCond},
		includeReady:    true,
		operatorVersion: chartVersion,
		driver:          driverInfo,
	}); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("reconciled",
		"chartVersion", chartVersion,
		"driverReady", driverStatus,
		"validatorPassed", validatorStatus,
	)
	// Polling safety net: ClusterPolicy CRD is not watched (installed by Helm),
	// so a periodic requeue is needed to pick up state transitions there.
	return ctrl.Result{RequeueAfter: requeueWarn}, nil
}

// runPreflight evaluates the cluster's GPU-node OS state and returns the resulting
// Preflight condition, the detected OS type, and reconcile control flow.
//
// On Proceed it returns the True condition and detected OS - the caller continues
// with installation. On Warn or Error it writes the appropriate condition itself and
// returns nil for the condition, signaling the caller to return the supplied result/err.
func (r *GpuReconciler) runPreflight(ctx context.Context, gpu *gpuv1beta1.Gpu) (*metav1.Condition, detection.OSType, ctrl.Result, error) {
	logger := log.FromContext(ctx)

	pre, err := detection.RunPreflight(ctx, r.Client)
	if err != nil {
		return nil, detection.OSTypeUnknown, ctrl.Result{}, fmt.Errorf("running preflight: %w", err)
	}

	switch pre.Outcome {
	case detection.OutcomeWarn:
		logger.Info("preflight warning, requeueing", "reason", pre.Reason)
		if err := r.applyStatus(ctx, gpu.Name, statusUpdate{
			conditions: []metav1.Condition{
				{Type: condPreflight, Status: metav1.ConditionUnknown, Reason: reasonWaiting, Message: pre.Reason},
			},
		}); err != nil {
			return nil, detection.OSTypeUnknown, ctrl.Result{}, err
		}
		return nil, detection.OSTypeUnknown, ctrl.Result{RequeueAfter: requeueWarn}, nil

	case detection.OutcomeError:
		// Hard blocker - stop until user resolves it.
		// No automatic requeue; the next reconcile is triggered by a CR or node change.
		logger.Info("preflight error, stopping", "reason", pre.Reason)
		if err := r.applyStatus(ctx, gpu.Name, statusUpdate{
			conditions: []metav1.Condition{
				{Type: condPreflight, Status: metav1.ConditionFalse, Reason: reasonFailed, Message: pre.Reason},
			},
		}); err != nil {
			return nil, detection.OSTypeUnknown, ctrl.Result{}, err
		}
		return nil, detection.OSTypeUnknown, ctrl.Result{}, nil
	}

	return &metav1.Condition{
		Type:    condPreflight,
		Status:  metav1.ConditionTrue,
		Reason:  reasonPassed,
		Message: fmt.Sprintf("all GPU nodes are running %s", pre.OS),
	}, pre.OS, ctrl.Result{}, nil
}

// installOrUpgrade loads the embedded chart, builds values from the Gpu spec, and
// drives Helm. On failure it writes Preflight + HelmInstalled=False itself (so callers
// don't need to) and returns a wrapped error. On success it returns the
// HelmInstalled=True condition along with the chart version.
func (r *GpuReconciler) installOrUpgrade(ctx context.Context, gpu *gpuv1beta1.Gpu, preflightCond *metav1.Condition, osType detection.OSType) (metav1.Condition, string, error) {
	logger := log.FromContext(ctx)

	chartData, err := chart.GPUOperatorChart()
	if err != nil {
		return metav1.Condition{}, "", fmt.Errorf("loading embedded chart: %w", err)
	}

	values, err := helm.BuildValues(gpu.Spec, helm.ClusterInfo{OS: osType})
	if err != nil {
		return metav1.Condition{}, "", fmt.Errorf("building helm values: %w", err)
	}

	if err := r.Installer.InstallOrUpgrade(ctx, chartData, values); err != nil {
		statusErr := r.applyStatus(ctx, gpu.Name, statusUpdate{
			conditions: []metav1.Condition{
				*preflightCond,
				{Type: condHelmInstalled, Status: metav1.ConditionFalse, Reason: reasonFailed, Message: err.Error()},
			},
		})
		if statusErr != nil {
			logger.Error(statusErr, "failed to update status after Helm error")
		}
		return metav1.Condition{}, "", fmt.Errorf("helm install/upgrade: %w", err)
	}

	chartVersion, err := chart.GPUOperatorChartVersion()
	if err != nil {
		return metav1.Condition{}, "", fmt.Errorf("reading chart version: %w", err)
	}

	return metav1.Condition{
		Type:    condHelmInstalled,
		Status:  metav1.ConditionTrue,
		Reason:  reasonInstalled,
		Message: fmt.Sprintf("GPU Operator %s installed successfully", chartVersion),
	}, chartVersion, nil
}

func (r *GpuReconciler) reconcileDelete(ctx context.Context, gpu *gpuv1beta1.Gpu) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(gpu, finalizer) {
		return ctrl.Result{}, nil
	}

	// Rogue CR (name != expectedCRName) somehow has our finalizer. Drop it
	// without calling Helm - Uninstall would target the real release.
	if gpu.Name != expectedCRName {
		controllerutil.RemoveFinalizer(gpu, finalizer)
		if err := r.Update(ctx, gpu); err != nil {
			return ctrl.Result{}, fmt.Errorf("removing finalizer from rogue CR: %w", err)
		}
		return ctrl.Result{}, nil
	}

	logger.Info("Gpu CR deleted, uninstalling GPU Operator")

	// Best-effort status update - do not block deletion if this fails.
	// Unknown = in-progress; the uninstall outcome has not yet been determined.
	if err := r.applyStatus(ctx, gpu.Name, statusUpdate{
		conditions: []metav1.Condition{
			{Type: condHelmInstalled, Status: metav1.ConditionUnknown, Reason: reasonUninstalling, Message: "uninstalling GPU Operator"},
		},
	}); err != nil {
		logger.Error(err, "failed to update status before uninstall, continuing")
	}

	// Block deletion if user workloads are still consuming GPU resources.
	active, err := r.detectActiveGPUWorkloads(ctx)
	if err != nil {
		logger.Error(err, "failed to check for active GPU workloads, proceeding with uninstall")
	} else if len(active) > 0 {
		msg := fmt.Sprintf("deletion blocked: %d pod(s) are actively using GPU resources: %v; delete or wait for these workloads to complete before removing GPU support", len(active), active)
		if statusErr := r.applyStatus(ctx, gpu.Name, statusUpdate{
			conditions: []metav1.Condition{
				{Type: condWorkloadProtection, Status: metav1.ConditionFalse, Reason: reasonActiveWorkloads, Message: msg},
			},
		}); statusErr != nil {
			logger.Error(statusErr, "failed to update WorkloadProtection status")
		}
		return ctrl.Result{RequeueAfter: requeueWarn}, nil
	}

	// Uninstall is idempotent - returns nil if the release is already gone.
	// Does not wait for pods to terminate; namespace deletion below blocks
	// on child resources via foreground propagation.
	if err := r.Installer.Uninstall(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("helm uninstall: %w", err)
	}

	// Helm never deletes namespaces; clean it up explicitly with foreground propagation
	// so all child resources are gone before the namespace itself disappears.
	if err := r.deleteNamespace(ctx, gpuOperatorNamespace); err != nil {
		return ctrl.Result{}, err
	}

	return r.removeFinalizer(ctx, gpu.Name)
}

func (r *GpuReconciler) deleteNamespace(ctx context.Context, name string) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := r.Delete(ctx, ns, client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("deleting namespace %s: %w", name, err)
	}
	return nil
}

func (r *GpuReconciler) removeFinalizer(ctx context.Context, name string) (ctrl.Result, error) {
	live := &gpuv1beta1.Gpu{}
	if err := r.Get(ctx, types.NamespacedName{Name: name}, live); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("re-fetching Gpu CR for finalizer removal: %w", err)
	}
	controllerutil.RemoveFinalizer(live, finalizer)
	if err := r.Update(ctx, live); err != nil {
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// statusUpdate carries the partial status the reconciler wants to write in this cycle.
// Only the conditions listed here are applied; absent ones are left untouched on the
// live object. operatorVersion and driver are applied only when non-zero / non-nil.
type statusUpdate struct {
	conditions      []metav1.Condition
	includeReady    bool
	operatorVersion string
	driver          *gpuv1beta1.DriverStatus
}

// applyStatus merges the requested updates into the live status and writes them via
// a strategic-merge patch. It re-reads the CR so SetStatusCondition can preserve
// LastTransitionTime, computes the Ready summary when requested, and returns without
// issuing the patch when the resulting status equals what's already stored.
//
// Single-writer assumption: this controller owns every field in .status. If a second
// controller is ever introduced that writes a different subset of .status, switch
// this method to Server-Side Apply with a FieldOwner so the two writers don't clobber
// each other's fields.
func (r *GpuReconciler) applyStatus(ctx context.Context, name string, upd statusUpdate) error {
	live := &gpuv1beta1.Gpu{}
	if err := r.Get(ctx, types.NamespacedName{Name: name}, live); err != nil {
		return fmt.Errorf("re-fetching Gpu CR for status update: %w", err)
	}
	original := live.DeepCopy()

	for _, c := range upd.conditions {
		c.ObservedGeneration = live.Generation
		apimeta.SetStatusCondition(&live.Status.Conditions, c)
	}
	if upd.includeReady {
		apimeta.SetStatusCondition(&live.Status.Conditions, computeReadySummary(live.Status.Conditions, live.Generation))
	}
	if upd.operatorVersion != "" {
		live.Status.OperatorVersion = upd.operatorVersion
	}
	if upd.driver != nil {
		live.Status.Driver = upd.driver
	}

	if equality.Semantic.DeepEqual(original.Status, live.Status) {
		return nil
	}

	if err := r.Status().Patch(ctx, live, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("patching Gpu status: %w", err)
	}
	return nil
}

// checkDriverDaemonSet aggregates state across every nvidia-driver-daemonset in the
// gpu-operator namespace. NVIDIA creates one DaemonSet per kernel version, so a cluster
// with mixed kernels has multiple DaemonSets - DriverReady=True only when every node on
// every kernel version has a ready driver.
//
// Unknown = DaemonSet not yet present or pods still rolling out.
// False   = list error.
// True    = all nodes ready, available, and updated across all driver DaemonSets.
func (r *GpuReconciler) checkDriverDaemonSet(ctx context.Context) (metav1.ConditionStatus, string, string, *gpuv1beta1.DriverStatus) {
	dsList := &appsv1.DaemonSetList{}
	if err := r.List(ctx, dsList,
		client.InNamespace(gpuOperatorNamespace),
		client.MatchingLabels{"app": driverAppLabel},
	); err != nil {
		// Return an empty (not nil) DriverStatus so applyStatus clears stale nodesReady/version from a previous successful read.
		return metav1.ConditionFalse, reasonReadError, fmt.Sprintf("error listing driver DaemonSets: %v", err), &gpuv1beta1.DriverStatus{}
	}
	if len(dsList.Items) == 0 {
		return metav1.ConditionUnknown, reasonWaiting, "nvidia-driver-daemonset not found; driver installation may still be in progress", &gpuv1beta1.DriverStatus{}
	}

	var totalDesired, totalReady, totalAvailable, totalUpdated int32
	for i := range dsList.Items {
		ds := &dsList.Items[i]
		totalDesired += ds.Status.DesiredNumberScheduled
		totalReady += ds.Status.NumberReady
		totalAvailable += ds.Status.NumberAvailable
		totalUpdated += ds.Status.UpdatedNumberScheduled
	}

	// Version: report only when all DaemonSets agree (i.e. not mid-upgrade).
	version := driverVersionFromDaemonSet(&dsList.Items[0])
	for i := 1; i < len(dsList.Items); i++ {
		if driverVersionFromDaemonSet(&dsList.Items[i]) != version {
			version = ""
			break
		}
	}

	driverInfo := &gpuv1beta1.DriverStatus{
		NodesReady: totalReady,
		Version:    version,
	}

	if totalDesired == 0 {
		return metav1.ConditionUnknown, reasonWaiting, "driver DaemonSet has no scheduled pods; no GPU nodes may be present", driverInfo
	}
	if totalReady < totalDesired || totalAvailable < totalDesired || totalUpdated < totalDesired {
		return metav1.ConditionUnknown, reasonProgressing, fmt.Sprintf("driver DaemonSet: %d/%d nodes ready, %d/%d available, %d/%d updated", totalReady, totalDesired, totalAvailable, totalDesired, totalUpdated, totalDesired), driverInfo
	}
	return metav1.ConditionTrue, reasonReady, fmt.Sprintf("driver DaemonSet: %d/%d nodes ready", totalReady, totalDesired), driverInfo
}

// checkClusterPolicy reads the NVIDIA ClusterPolicy status via unstructured client.
//
// Unknown = ClusterPolicy not yet present or still converging.
// False   = read error.
// True    = ClusterPolicy.status.state is "ready" (NVIDIA's end-to-end validator passed).
func (r *GpuReconciler) checkClusterPolicy(ctx context.Context) (metav1.ConditionStatus, string, string) {
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
		return metav1.ConditionUnknown, reasonWaiting, "ClusterPolicy state is ignored"
	default:
		return metav1.ConditionUnknown, reasonProgressing, fmt.Sprintf("ClusterPolicy state is %q; waiting for ready", state)
	}
}

// driverVersionFromDaemonSet extracts the driver version from the DaemonSet's first
// container image tag (e.g. "nvcr.io/nvidia/driver:535.129.03-ubuntu22.04" -> "535.129.03").
// This reads from Spec (the desired image), not from individual pod statuses - it reports
// the version the cluster is converging toward. Returns empty string when unparseable.
func driverVersionFromDaemonSet(ds *appsv1.DaemonSet) string {
	if len(ds.Spec.Template.Spec.Containers) == 0 {
		return ""
	}
	image := ds.Spec.Template.Spec.Containers[0].Image
	idx := strings.LastIndex(image, ":")
	if idx < 0 {
		return ""
	}
	tag := image[idx+1:]
	// Tags are formatted as "<version>-<os>" (e.g. "535.129.03-ubuntu22.04").
	if before, _, found := strings.Cut(tag, "-"); found {
		return before
	}
	return tag
}

// gpuResourcePrefixes lists the Kubernetes extended resource names used by the NVIDIA
// device plugin. All supported instance types (g4dn, g6, g2-, Standard_NC) use T4/L4/A10
// GPUs which expose nvidia.com/gpu only. When MIG support is added (requires A100/H100),
// add "nvidia.com/mig-*" to this list to cover MIG partition resources (e.g. nvidia.com/mig-1g.5gb).
var gpuResourcePrefixes = []string{"nvidia.com/gpu"}

// detectActiveGPUWorkloads returns the namespace/name of every Running or Pending pod
// (outside the gpu-operator namespace) that has a GPU resource request. An error listing
// pods is treated as best-effort - the caller logs it and continues.
func (r *GpuReconciler) detectActiveGPUWorkloads(ctx context.Context) ([]string, error) {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList); err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	var active []string
	for _, pod := range podList.Items {
		if pod.Namespace == gpuOperatorNamespace {
			continue
		}
		if pod.DeletionTimestamp != nil {
			// Pod is already terminating - the GPU device will be released once it stops.
			continue
		}
		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
			continue
		}
		if podRequestsGPU(pod) {
			active = append(active, fmt.Sprintf("%s/%s", pod.Namespace, pod.Name))
		}
	}
	return active, nil
}

// podRequestsGPU returns true if any regular or init container in the pod requests
// a GPU resource (nvidia.com/gpu). MIG variants are not checked - no supported instance
// type offers MIG-capable hardware.
func podRequestsGPU(pod corev1.Pod) bool {
	for _, containers := range [][]corev1.Container{pod.Spec.Containers, pod.Spec.InitContainers} {
		for _, c := range containers {
			for resourceName := range c.Resources.Limits {
				name := string(resourceName)
				for _, prefix := range gpuResourcePrefixes {
					if strings.HasPrefix(name, prefix) {
						return true
					}
				}
			}
		}
	}
	return false
}

// SetupWithManager registers the controller and wires up watches on Nodes and
// driver DaemonSets so preflight errors and driver-rollout state transitions both
// trigger reconciliation. ClusterPolicy is intentionally not watched: the CRD is
// installed by Helm and is absent on a fresh cluster - the periodic requeue picks
// up state changes there.
func (r *GpuReconciler) SetupWithManager(mgr ctrl.Manager) error {
	enqueueGpu := handler.EnqueueRequestsFromMapFunc(
		func(ctx context.Context, _ client.Object) []reconcile.Request {
			var list gpuv1beta1.GpuList
			if err := r.List(ctx, &list); err != nil {
				log.FromContext(ctx).Error(err, "failed to list Gpu CRs; watch event will be lost")
				return nil
			}
			reqs := make([]reconcile.Request, len(list.Items))
			for i, gpu := range list.Items {
				reqs[i] = reconcile.Request{NamespacedName: types.NamespacedName{Name: gpu.Name}}
			}
			return reqs
		},
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&gpuv1beta1.Gpu{}).
		Named("gpu").
		Watches(
			&corev1.Node{},
			enqueueGpu,
			builder.WithPredicates(gpuNodeChangedPredicate()),
		).
		Watches(
			&appsv1.DaemonSet{},
			enqueueGpu,
			builder.WithPredicates(driverDaemonSetPredicate()),
		).
		Complete(r)
}

// gpuNodeChangedPredicate returns a predicate that fires only for meaningful GPU node
// changes: creation, deletion, label changes that affect GPU membership, or OS image
// changes. Kubelet heartbeats (which bump ResourceVersion every ~10s) are filtered out.
func gpuNodeChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return detection.IsGPUNode(e.Object.GetLabels())
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return detection.IsGPUNode(e.Object.GetLabels())
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNode, ok1 := e.ObjectOld.(*corev1.Node)
			newNode, ok2 := e.ObjectNew.(*corev1.Node)
			if !ok1 || !ok2 {
				return false
			}
			wasGPU := detection.IsGPUNode(oldNode.Labels)
			isGPU := detection.IsGPUNode(newNode.Labels)
			if !wasGPU && !isGPU {
				return false
			}
			if wasGPU != isGPU {
				return true
			}
			return oldNode.Status.NodeInfo.OSImage != newNode.Status.NodeInfo.OSImage
		},
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

// driverDaemonSetPredicate fires only for DaemonSets matching the NVIDIA driver naming
// convention in the gpu-operator namespace, so unrelated DaemonSet changes elsewhere in
// the cluster don't trigger reconciles.
func driverDaemonSetPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == gpuOperatorNamespace &&
			obj.GetLabels()["app"] == driverAppLabel
	})
}
