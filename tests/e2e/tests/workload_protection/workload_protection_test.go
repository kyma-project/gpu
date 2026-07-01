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

// Package workload_protection verifies the operator blocks Gpu CR deletion
// while GPU workloads are running, and clears its finalizer once the
// workloads are gone.
package workload_protection

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	assertgpu "github.com/kyma-project/gpu/tests/e2e/pkg/asserts/gpu"
	gpuhelper "github.com/kyma-project/gpu/tests/e2e/pkg/helpers/gpu"
	"github.com/kyma-project/gpu/tests/e2e/pkg/helpers/workload"
)

const readinessTimeout = 20 * time.Minute

// TestWorkloadProtection verifies the deletion guard: while a Pod is
// consuming nvidia.com/gpu, deleting the Gpu CR must surface
// WorkloadProtection=False with reason=ActiveGPUWorkloads and the finalizer
// must stay attached. Once the Pod goes away, the operator must release the
// finalizer and the CR must disappear.
func TestWorkloadProtection(t *testing.T) {
	cr, err := gpuhelper.NewBuilder().Apply(t)
	require.NoError(t, err, "applying Gpu CR")

	t.Log("Waiting for Gpu CR to reach Ready=True before deploying a GPU workload")
	assertgpu.AssertReady(t, cr.Name, assertgpu.WithTimeout(readinessTimeout))

	pod, err := workload.DeployGPUPod(t)
	require.NoError(t, err, "deploying GPU pod")
	require.NotNil(t, pod)

	t.Log("Issuing delete on Gpu CR - operator should block via finalizer")
	require.NoError(t, gpuhelper.Delete(t, cr), "issuing Gpu CR delete")

	// The controller writes WorkloadProtection=False with the
	// ActiveGPUWorkloads reason when GPU pods exist at deletion time.
	assertgpu.AssertConditionReason(
		t, cr.Name,
		assertgpu.ConditionWorkloadProtection,
		metav1.ConditionFalse,
		"ActiveGPUWorkloads",
		assertgpu.WithTimeout(3*time.Minute),
	)

	t.Log("Removing the GPU workload - finalizer should clear and the Gpu CR should disappear")
	require.NoError(t, workload.DeletePod(t, pod), "deleting GPU pod")
	require.NoError(t, gpuhelper.WaitForDeletion(t, cr), "waiting for Gpu CR to be removed")
}
