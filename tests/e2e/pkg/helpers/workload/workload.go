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

// Package workload builds a Pod that requests an NVIDIA GPU. It's used by the
// workload-protection e2e test to verify that deletion of the Gpu CR is
// blocked while a GPU workload is active.
package workload

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"

	"github.com/kyma-project/gpu/tests/e2e/pkg/config"
	"github.com/kyma-project/gpu/tests/e2e/pkg/helpers/client"
	"github.com/kyma-project/gpu/tests/e2e/pkg/setup"
)

// Options control the GPU pod that DeployGPUPod creates.
type Options struct {
	Namespace    string
	Name         string
	Image        string
	GPUResource  corev1.ResourceName
	GPUCount     int64
	WaitForReady bool
}

// Option mutates Options.
type Option func(*Options)

func WithNamespace(ns string) Option  { return func(o *Options) { o.Namespace = ns } }
func WithName(name string) Option     { return func(o *Options) { o.Name = name } }
func WithImage(image string) Option   { return func(o *Options) { o.Image = image } }
func WithGPUCount(count int64) Option { return func(o *Options) { o.GPUCount = count } }
func WithoutWaitForReady() Option     { return func(o *Options) { o.WaitForReady = false } }

// DeployGPUPod creates a Pod requesting a single nvidia.com/gpu resource and
// optionally waits for it to be Running. Cleanup is registered automatically.
//
// Returns the created pod and an error. Per istio's helper guidelines, no
// assertions live inside this helper.
func DeployGPUPod(t *testing.T, opts ...Option) (*corev1.Pod, error) {
	t.Helper()

	o := &Options{
		Namespace:    "default",
		Name:         "gpu-workload",
		Image:        "nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda11.7.1-ubuntu20.04",
		GPUResource:  corev1.ResourceName("nvidia.com/gpu"),
		GPUCount:     1,
		WaitForReady: true,
	}
	for _, opt := range opts {
		opt(o)
	}

	r, err := client.ResourcesClient(t)
	if err != nil {
		return nil, err
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      o.Name,
			Namespace: o.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/name": o.Name},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "gpu",
				Image:   o.Image,
				Command: []string{"sh", "-c", "sleep 3600"},
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						o.GPUResource: *resource.NewQuantity(o.GPUCount, resource.DecimalSI),
					},
				},
			}},
		},
	}

	t.Logf("Creating GPU pod %s/%s with limit %s=%d", pod.Namespace, pod.Name, o.GPUResource, o.GPUCount)
	if err := r.Create(t.Context(), pod); err != nil {
		t.Logf("Failed to create GPU pod: %v", err)
		return nil, err
	}

	setup.DeclareCleanup(t, func() {
		t.Logf("Cleaning up GPU pod %s/%s", pod.Namespace, pod.Name)
		if err := r.Delete(setup.GetCleanupContext(), pod); err != nil {
			t.Logf("Failed to delete GPU pod: %v", err)
		}
	})

	if !o.WaitForReady {
		return pod, nil
	}

	timeout := min(config.Get().TestTimeout, 5*time.Minute)
	if err := wait.For(
		conditions.New(r).PodRunning(pod),
		wait.WithTimeout(timeout),
	); err != nil {
		t.Logf("GPU pod did not reach Running: %v", err)
		return pod, err
	}
	t.Logf("GPU pod %s/%s is Running", pod.Namespace, pod.Name)
	return pod, nil
}

// DeletePod removes the pod and waits for it to be gone. Used by tests that
// want explicit ordering (delete pod -> verify Gpu CR finalizer clears).
func DeletePod(t *testing.T, pod *corev1.Pod) error {
	t.Helper()

	r, err := client.ResourcesClient(t)
	if err != nil {
		return err
	}
	if err := r.Delete(t.Context(), pod); err != nil {
		t.Logf("Failed to delete GPU pod: %v", err)
		return err
	}
	return wait.For(
		conditions.New(r).ResourceDeleted(pod),
		wait.WithTimeout(2*time.Minute),
	)
}
