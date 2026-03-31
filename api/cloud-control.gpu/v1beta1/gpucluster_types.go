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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Labels used on GpuCluster resources for indexing and selection.
const (
	LabelShootName       = "kyma-project.io/shoot-name"
	LabelGlobalAccountID = "kyma-project.io/global-account-id"
)

// ProviderType identifies the cloud provider of the Shoot cluster.
// +kubebuilder:validation:Enum=aws;gcp;azure
type ProviderType string

const (
	ProviderAws   ProviderType = "aws"
	ProviderGcp   ProviderType = "gcp"
	ProviderAzure ProviderType = "azure"
)

// GpuClusterState represents the high-level lifecycle state of a GpuCluster.
// +kubebuilder:validation:Enum=Pending;Provisioning;Ready;Warning;Error;Deleting
type GpuClusterState string

const (
	GpuClusterStatePending      GpuClusterState = "Pending"
	GpuClusterStateProvisioning GpuClusterState = "Provisioning"
	GpuClusterStateReady        GpuClusterState = "Ready"
	GpuClusterStateWarning      GpuClusterState = "Warning"
	GpuClusterStateError        GpuClusterState = "Error"
	GpuClusterStateDeleting     GpuClusterState = "Deleting"
)

// InstallPhase tracks the Helm install/upgrade lifecycle across looper cycles.
// +kubebuilder:validation:Enum=None;HelmInstalling;HelmInstalled;HealthChecking;Ready;HelmUpgrading;HelmUninstalling;HelmUninstalled
type InstallPhase string

const (
	InstallPhaseNone           InstallPhase = "None"
	InstallPhaseHelmInstalling InstallPhase = "HelmInstalling"
	InstallPhaseHelmInstalled  InstallPhase = "HelmInstalled"
	// InstallPhaseHealthChecking is the phase between Helm reporting the release as "deployed"
	// and the GPU module declaring the cluster Ready. Helm only knows the chart was applied
	// successfully - it does not wait for the NVIDIA GPU Operator's DaemonSets (driver, toolkit,
	// device-plugin, DCGM) to become healthy on every node. This phase is where the reconciler
	// verifies actual component health before transitioning to Ready.
	InstallPhaseHealthChecking   InstallPhase = "HealthChecking"
	InstallPhaseReady            InstallPhase = "Ready"
	InstallPhaseHelmUpgrading    InstallPhase = "HelmUpgrading"
	InstallPhaseHelmUninstalling InstallPhase = "HelmUninstalling"
	InstallPhaseHelmUninstalled  InstallPhase = "HelmUninstalled"
)

// DeletePhase tracks the deletion state machine across looper cycles.
// +kubebuilder:validation:Enum=None;HelmUninstalling;NamespaceDeleting;GpuCRDeleting;CleanupComplete
type DeletePhase string

const (
	DeletePhaseNone              DeletePhase = "None"
	DeletePhaseHelmUninstalling  DeletePhase = "HelmUninstalling"
	DeletePhaseNamespaceDeleting DeletePhase = "NamespaceDeleting"
	DeletePhaseGpuCRDeleting     DeletePhase = "GpuCRDeleting"
	DeletePhaseCleanupComplete   DeletePhase = "CleanupComplete"
)

// GpuPool represents a single GPU worker pool detected from the Shoot spec.
type GpuPool struct {
	// name is the worker pool name from the Shoot spec.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// machineType is the cloud provider machine type (e.g., p4d.24xlarge, a2-highgpu-1g).
	// +kubebuilder:validation:Required
	MachineType string `json:"machineType"`

	// minimum is the minimum number of nodes in the pool.
	// +optional
	Minimum int32 `json:"minimum,omitempty"`

	// maximum is the maximum number of nodes in the pool.
	// +optional
	Maximum int32 `json:"maximum,omitempty"`
}

// GpuClusterSpec defines the desired state of GpuCluster.
// One GpuCluster exists per SKR that has GPU worker pools and the gpu-module enabled.
type GpuClusterSpec struct {
	// kymaName is the name of the Kyma CR that represents this SKR.
	// +kubebuilder:validation:Required
	KymaName string `json:"kymaName"`

	// shootName is the Gardener Shoot name.
	// +kubebuilder:validation:Required
	ShootName string `json:"shootName"`

	// shootNamespace is the Gardener Shoot namespace (e.g., garden-project).
	// +kubebuilder:validation:Required
	ShootNamespace string `json:"shootNamespace"`

	// provider is the cloud provider detected from the Shoot (aws, gcp, azure).
	// +kubebuilder:validation:Required
	Provider ProviderType `json:"provider"`

	// gardenLinux indicates whether the Shoot uses Garden Linux as the node OS.
	// When true, the NVIDIA driver image is swapped to gardenlinux-nvidia-installer.
	// +optional
	GardenLinux bool `json:"gardenLinux,omitempty"`

	// gpuPools is the list of GPU worker pools detected from the Shoot spec.
	// +kubebuilder:validation:MinItems=1
	GpuPools []GpuPool `json:"gpuPools"`
}

// GpuClusterStatus defines the observed state of GpuCluster.
type GpuClusterStatus struct {
	// state is the high-level lifecycle state.
	// +optional
	State GpuClusterState `json:"state,omitempty"`

	// moduleEnabled indicates whether gpu-module is listed in the Kyma CR's .spec.modules[].
	// +optional
	ModuleEnabled bool `json:"moduleEnabled,omitempty"`

	// operatorInstalled indicates whether the NVIDIA GPU Operator Helm release exists in the SKR.
	// +optional
	OperatorInstalled bool `json:"operatorInstalled,omitempty"`

	// operatorVersion is the installed NVIDIA GPU Operator chart version.
	// +optional
	OperatorVersion string `json:"operatorVersion,omitempty"`

	// driverVersion is the NVIDIA driver version reported by the driver DaemonSet.
	// +optional
	DriverVersion string `json:"driverVersion,omitempty"`

	// nodesTotal is the total number of GPU nodes across all pools.
	// +optional
	NodesTotal int32 `json:"nodesTotal,omitempty"`

	// nodesReady is the number of GPU nodes with healthy NVIDIA drivers.
	// +optional
	NodesReady int32 `json:"nodesReady,omitempty"`

	// installPhase tracks the Helm install/upgrade lifecycle across looper cycles.
	// +optional
	InstallPhase InstallPhase `json:"installPhase,omitempty"`

	// deletePhase tracks the deletion state machine across looper cycles.
	// +optional
	DeletePhase DeletePhase `json:"deletePhase,omitempty"`

	// lastReconciled is the timestamp of the last successful reconciliation.
	// +optional
	LastReconciled *metav1.Time `json:"lastReconciled,omitempty"`

	// conditions represent the current state of the GpuCluster resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories={kyma-cloud-control}
// +kubebuilder:printcolumn:name="Kyma",type="string",JSONPath=".spec.kymaName"
// +kubebuilder:printcolumn:name="Shoot",type="string",JSONPath=".spec.shootName"
// +kubebuilder:printcolumn:name="Provider",type="string",JSONPath=".spec.provider"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// GpuCluster is the Schema for the gpuclusters API.
// It is an internal KCP resource — one per SKR that has GPU worker pools.
// Users never interact with this resource directly.
type GpuCluster struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GpuClusterSpec `json:"spec"`

	// +optional
	Status GpuClusterStatus `json:"status,omitzero"`
}

// Conditions returns a pointer to the status conditions slice.
func (in *GpuCluster) Conditions() *[]metav1.Condition {
	return &in.Status.Conditions
}

// +kubebuilder:object:root=true

// GpuClusterList contains a list of GpuCluster.
type GpuClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GpuCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GpuCluster{}, &GpuClusterList{})
}
