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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func gpuLimits() corev1.ResourceList {
	return corev1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")}
}

func TestPodRequestsGPU(t *testing.T) {
	tests := []struct {
		name string
		pod  corev1.Pod
		want bool
	}{
		{
			name: "regular container with nvidia.com/gpu",
			pod: corev1.Pod{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Resources: corev1.ResourceRequirements{Limits: gpuLimits()},
				}},
			}},
			want: true,
		},
		{
			name: "init container with nvidia.com/gpu",
			pod: corev1.Pod{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "app", Image: "busybox"}},
				InitContainers: []corev1.Container{{
					Resources: corev1.ResourceRequirements{Limits: gpuLimits()},
				}},
			}},
			want: true,
		},
		{
			name: "no GPU resources",
			pod: corev1.Pod{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
					},
				}},
			}},
			want: false,
		},
		{
			name: "no resource limits at all",
			pod:  corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}}},
			want: false,
		},
		{
			name: "empty pod spec",
			pod:  corev1.Pod{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := podRequestsGPU(tt.pod)
			if got != tt.want {
				t.Errorf("podRequestsGPU() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDetectActiveGPUWorkloads_NamespaceExclusion verifies that pods in the
// gpu-operator namespace are not counted as active user workloads.
func TestDetectActiveGPUWorkloads_NamespaceExclusion(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	systemPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "driver-pod", Namespace: gpuOperatorNamespace},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:      "driver",
				Image:     "nvcr.io/nvidia/driver:latest",
				Resources: corev1.ResourceRequirements{Limits: gpuLimits()},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(systemPod).WithStatusSubresource(systemPod).Build()
	r := &GpuReconciler{Client: fakeClient}

	active, err := r.detectActiveGPUWorkloads(context.Background())
	if err != nil {
		t.Fatalf("detectActiveGPUWorkloads() error = %v", err)
	}
	if len(active) != 0 {
		t.Errorf("expected no active workloads (gpu-operator pods excluded), got %v", active)
	}
}

// TestDetectActiveGPUWorkloads_TerminatingPodExclusion verifies that pods with
// DeletionTimestamp set are not counted - the device will be released when they stop.
func TestDetectActiveGPUWorkloads_TerminatingPodExclusion(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	now := metav1.Now()
	terminatingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "training-job",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{"example.com/protect"}, // required for fake client to accept DeletionTimestamp
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:      "trainer",
				Image:     "pytorch:latest",
				Resources: corev1.ResourceRequirements{Limits: gpuLimits()},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(terminatingPod).WithStatusSubresource(terminatingPod).Build()
	r := &GpuReconciler{Client: fakeClient}

	active, err := r.detectActiveGPUWorkloads(context.Background())
	if err != nil {
		t.Fatalf("detectActiveGPUWorkloads() error = %v", err)
	}
	if len(active) != 0 {
		t.Errorf("expected no active workloads (terminating pods excluded), got %v", active)
	}
}
