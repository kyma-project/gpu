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

// DriverSpec allows optional override of the NVIDIA driver version.
type DriverSpec struct {
	// version is the NVIDIA driver version to install (e.g., "535.129.03").
	// If empty, the default version from the GPU Operator chart is used.
	// +optional
	Version string `json:"version,omitempty"`
}

// GpuSpec defines the desired state of the Gpu resource.
// Users can optionally override driver version and operator version.
// All other configuration uses sensible defaults from the NVIDIA GPU Operator chart.
type GpuSpec struct {
	// driver allows optional override of the NVIDIA driver configuration.
	// +optional
	Driver *DriverSpec `json:"driver,omitempty"`

	// operatorVersion is the NVIDIA GPU Operator chart version to install.
	// If empty, the version embedded in the gpu module binary is used.
	// +optional
	OperatorVersion string `json:"operatorVersion,omitempty"`
}

// DriverStatus reports the observed state of the NVIDIA driver across GPU nodes.
type DriverStatus struct {
	// version is the NVIDIA driver version reported by the driver DaemonSet.
	// +optional
	Version string `json:"version,omitempty"`

	// nodesReady is the number of GPU nodes with healthy NVIDIA drivers.
	// +optional
	NodesReady int32 `json:"nodesReady,omitempty"`
}

// GpuStatus defines the observed state of the Gpu resource.
type GpuStatus struct {
	// operatorVersion is the installed NVIDIA GPU Operator chart version.
	// +optional
	OperatorVersion string `json:"operatorVersion,omitempty"`

	// driver reports the observed state of the NVIDIA driver across GPU nodes.
	// +optional
	Driver *DriverStatus `json:"driver,omitempty"`

	// conditions represent the current state of the Gpu resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Driver Version",type="string",JSONPath=".status.driver.version"
// +kubebuilder:printcolumn:name="Nodes Ready",type="integer",JSONPath=".status.driver.nodesReady"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Gpu is the user-facing resource for managing GPU support in a Kyma cluster.
// One Gpu resource exists per cluster. It configures the NVIDIA GPU Operator
// and reports GPU health status.
type Gpu struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Spec GpuSpec `json:"spec,omitempty"`

	// +optional
	Status GpuStatus `json:"status,omitempty"`
}

// Conditions returns a pointer to the status conditions slice.
func (in *Gpu) Conditions() *[]metav1.Condition {
	return &in.Status.Conditions
}

// +kubebuilder:object:root=true

// GpuList contains a list of Gpu.
type GpuList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Gpu `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Gpu{}, &GpuList{})
}
