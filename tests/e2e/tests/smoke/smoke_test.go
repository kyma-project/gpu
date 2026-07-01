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

// Package smoke is the GPU operator happy-path e2e. It applies the singleton
// Gpu CR, waits for Ready=True with all four input conditions, then drives an
// explicit uninstall to prove the operator tears down cleanly.
package smoke

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	assertgpu "github.com/kyma-project/gpu/tests/e2e/pkg/asserts/gpu"
	gpuhelper "github.com/kyma-project/gpu/tests/e2e/pkg/helpers/gpu"
)

// readinessTimeout is generous: a cold install pulls the GPU operator chart,
// schedules the driver DaemonSet, and waits for ClusterPolicy validation. On
// a fresh GPU node this can take 15+ minutes.
const readinessTimeout = 20 * time.Minute

// gpuOperatorNamespace mirrors the constant in internal/controller. Hard-coded
// because the helm release name is fixed by the chart, not configurable.
const gpuOperatorNamespace = "gpu-operator"

// TestSmoke exercises the GPU operator's happy path AND its uninstall path:
//
//  1. Apply the singleton Gpu CR.
//  2. Wait for Ready=True and assert every input condition individually
//     (Preflight, HelmInstalled, DriverReady, ValidatorPassed).
//  3. Verify status.operatorVersion and status.driver.nodesReady are populated.
//  4. Delete the Gpu CR and assert the controller fully unwinds: the CR is
//     gone, the gpu-operator namespace is gone, and no ClusterPolicy remains.
//
// Step 4 catches regressions in the deletion path that would otherwise leave
// the cluster in a half-installed state - a class of bug that's invisible to
// unit + envtest but immediately breaks the next install.
func TestSmoke(t *testing.T) {
	cr, err := gpuhelper.NewBuilder().Apply(t)
	require.NoError(t, err, "applying Gpu CR")
	require.NotNil(t, cr)

	t.Log("Waiting for Gpu CR to reach Ready=True - this can take 15+ minutes on a cold cluster")
	assertgpu.AssertReady(t, cr.Name, assertgpu.WithTimeout(readinessTimeout))

	// Verify each input condition individually so a failure points at the
	// specific subsystem that didn't reach True (preflight, helm, driver
	// rollout, or ClusterPolicy validation).
	for _, condType := range []string{
		assertgpu.ConditionPreflight,
		assertgpu.ConditionHelmInstalled,
		assertgpu.ConditionDriverReady,
		assertgpu.ConditionValidatorPassed,
	} {
		assertgpu.AssertCondition(t, cr.Name, condType, metav1.ConditionTrue, assertgpu.WithTimeout(2*time.Minute))
	}

	version := assertgpu.AssertOperatorVersion(t, cr.Name)
	t.Logf("Gpu CR reports operatorVersion=%s", version)

	assertgpu.AssertDriverNodesReady(t, cr.Name, 1)

	// Drive the uninstall explicitly rather than relying on the deferred
	// cleanup, so we can assert on the resulting cluster state. The cleanup
	// registered by Apply is idempotent and will no-op if the CR is already
	// gone.
	t.Log("Driving explicit uninstall to verify the operator tears down cleanly")
	require.NoError(t, gpuhelper.Delete(t, cr), "deleting Gpu CR")
	require.NoError(t, gpuhelper.WaitForDeletion(t, cr), "waiting for Gpu CR to disappear")

	assertgpu.AssertOperatorNamespaceGone(t, gpuOperatorNamespace)
	assertgpu.AssertClusterPolicyGone(t)
}
