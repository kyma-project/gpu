/*
Copyright 2026 SAP SE or an SAP affiliate company and gpu contributors.

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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	gpuv1beta1 "github.com/kyma-project/gpu/api/v1beta1"
	"github.com/kyma-project/gpu/internal/chart"
	"github.com/kyma-project/gpu/internal/detection"
	"github.com/kyma-project/gpu/internal/helm"
)

const (
	requeueWarn = 30 * time.Second
	finalizer   = "gpu.kyma-project.io/gpu-operator"

	// condPreflight and condHelmInstalled are stable condition types - SetStatusCondition
	// updates them in place rather than accumulating one entry per state transition.
	condPreflight     = "Preflight"
	condHelmInstalled = "HelmInstalled"
)

// GpuReconciler reconciles a Gpu object.
type GpuReconciler struct {
	client.Client
	Installer *helm.Installer
}

// +kubebuilder:rbac:groups=gpu.kyma-project.io,resources=gpus,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gpu.kyma-project.io,resources=gpus/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gpu.kyma-project.io,resources=gpus/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="*",resources="*",verbs=get;list;watch;create;update;patch;delete

func (r *GpuReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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

	return r.reconcileNormal(ctx, gpu)
}

func (r *GpuReconciler) reconcileNormal(ctx context.Context, gpu *gpuv1beta1.Gpu) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(gpu, finalizer) {
		controllerutil.AddFinalizer(gpu, finalizer)
		if err := r.Update(ctx, gpu); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
		// Explicit requeue - don't rely solely on the watch event from the Update.
		return ctrl.Result{Requeue: true}, nil
	}

	// 1. pre-flight
	pre, err := detection.RunPreflight(ctx, r.Client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("running preflight: %w", err)
	}

	switch pre.Outcome {
	case detection.OutcomeWarn:
		logger.Info("preflight warning, requeueing", "reason", pre.Reason)
		if err := r.setPreflightCondition(ctx, gpu, metav1.ConditionFalse, "NotReady", pre.Reason, gpuv1beta1.GpuStateWarning); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueWarn}, nil

	case detection.OutcomeError:
		// Hard blocker (e.g. non-Garden-Linux GPU nodes) - stop until user resolves it.
		// No automatic requeue; the next reconcile is triggered by a CR or node change.
		logger.Info("preflight error, stopping", "reason", pre.Reason)
		if err := r.setPreflightCondition(ctx, gpu, metav1.ConditionFalse, "Failed", pre.Reason, gpuv1beta1.GpuStateError); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil

	default: // OutcomeProceed
	}

	// OutcomeProceed: all GPU nodes exist and run Garden Linux.
	// State is not set here - the Helm outcome owns the state transition.
	// If build steps below fail, status shows Preflight=True without a misleading Processing state.
	if err := r.setPreflightConditionOnly(ctx, gpu, metav1.ConditionTrue, "Passed", "all GPU nodes are running Garden Linux"); err != nil {
		return ctrl.Result{}, err
	}

	// 2. build values - preflight guarantees Garden Linux, so always true here
	chartData, err := chart.GPUOperatorChart()
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("loading embedded chart: %w", err)
	}

	values, err := helm.BuildValues(gpu.Spec, helm.ClusterInfo{GardenLinux: true})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("building helm values: %w", err)
	}

	// 3. install or upgrade
	if err := r.Installer.InstallOrUpgrade(ctx, chartData, values); err != nil {
		if statusErr := r.setHelmCondition(ctx, gpu, metav1.ConditionFalse, "Failed", err.Error(), gpuv1beta1.GpuStateError, ""); statusErr != nil {
			logger.Error(statusErr, "failed to update status after Helm error")
		}
		return ctrl.Result{}, fmt.Errorf("helm install/upgrade: %w", err)
	}

	chartVersion, err := chart.GPUOperatorChartVersion()
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading chart version: %w", err)
	}

	// Helm applied the manifests - pods are starting but not yet ready.
	// State stays Processing; a future status-sync reconcile transitions to Ready
	// once the DaemonSet reports all nodes healthy.
	if err := r.setHelmCondition(ctx, gpu, metav1.ConditionTrue, "Installed",
		fmt.Sprintf("GPU Operator %s installed successfully", chartVersion),
		gpuv1beta1.GpuStateProcessing,
		chartVersion,
	); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("GPU Operator reconciled, waiting for pods to become ready", "chartVersion", chartVersion)
	return ctrl.Result{}, nil
}

func (r *GpuReconciler) reconcileDelete(ctx context.Context, gpu *gpuv1beta1.Gpu) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(gpu, finalizer) {
		return ctrl.Result{}, nil
	}

	logger.Info("Gpu CR deleted, uninstalling GPU Operator")

	// Best-effort status update - do not block deletion if this fails.
	// The critical path is Uninstall and finalizer removal; status is cosmetic here.
	if err := r.setHelmCondition(ctx, gpu, metav1.ConditionFalse, "Uninstalling", "uninstalling GPU Operator", gpuv1beta1.GpuStateDeleting, ""); err != nil {
		logger.Error(err, "failed to update status before uninstall, continuing")
	}

	// Uninstall is idempotent - returns nil if the release is already gone.
	if err := r.Installer.Uninstall(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("helm uninstall: %w", err)
	}

	controllerutil.RemoveFinalizer(gpu, finalizer)
	if err := r.Update(ctx, gpu); err != nil {
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// setPreflightCondition patches the Preflight condition and overall state in one call.
func (r *GpuReconciler) setPreflightCondition(ctx context.Context, gpu *gpuv1beta1.Gpu, status metav1.ConditionStatus, reason, message string, state gpuv1beta1.GpuState) error {
	patch := client.MergeFrom(gpu.DeepCopy())
	gpu.Status.State = state
	apimeta.SetStatusCondition(&gpu.Status.Conditions, metav1.Condition{
		Type:               condPreflight,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: gpu.Generation,
	})
	if err := r.Status().Patch(ctx, gpu, patch); err != nil {
		return fmt.Errorf("patching Preflight condition: %w", err)
	}
	return nil
}

// setPreflightConditionOnly patches only the Preflight condition without changing state.
// Used on OutcomeProceed so that intermediate build failures don't leave a misleading
// Processing state — the Helm outcome owns the state transition.
func (r *GpuReconciler) setPreflightConditionOnly(ctx context.Context, gpu *gpuv1beta1.Gpu, status metav1.ConditionStatus, reason, message string) error {
	patch := client.MergeFrom(gpu.DeepCopy())
	apimeta.SetStatusCondition(&gpu.Status.Conditions, metav1.Condition{
		Type:               condPreflight,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: gpu.Generation,
	})
	if err := r.Status().Patch(ctx, gpu, patch); err != nil {
		return fmt.Errorf("patching Preflight condition: %w", err)
	}
	return nil
}

// setHelmCondition patches the HelmInstalled condition, overall state, and optionally
// operatorVersion - all in a single status patch to avoid split-brain between the two fields.
func (r *GpuReconciler) setHelmCondition(ctx context.Context, gpu *gpuv1beta1.Gpu, status metav1.ConditionStatus, reason, message string, state gpuv1beta1.GpuState, operatorVersion string) error {
	patch := client.MergeFrom(gpu.DeepCopy())
	gpu.Status.State = state
	if operatorVersion != "" {
		gpu.Status.OperatorVersion = operatorVersion
	}
	apimeta.SetStatusCondition(&gpu.Status.Conditions, metav1.Condition{
		Type:               condHelmInstalled,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: gpu.Generation,
	})
	if err := r.Status().Patch(ctx, gpu, patch); err != nil {
		return fmt.Errorf("patching HelmInstalled condition: %w", err)
	}
	return nil
}

// SetupWithManager registers the controller with the manager.
func (r *GpuReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gpuv1beta1.Gpu{}).
		Named("gpu").
		Complete(r)
}
