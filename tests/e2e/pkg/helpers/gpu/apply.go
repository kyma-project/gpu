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

package gpu

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"

	gpuv1beta1 "github.com/kyma-project/gpu/api/v1beta1"
	"github.com/kyma-project/gpu/tests/e2e/pkg/config"
	"github.com/kyma-project/gpu/tests/e2e/pkg/helpers/client"
	"github.com/kyma-project/gpu/tests/e2e/pkg/setup"
)

// gpuOperatorNamespace is the namespace the NVIDIA GPU Operator chart is
// installed into. The controller deletes it during uninstall; we must wait for
// it to disappear before declaring cleanup done, otherwise the next test finds
// leftover resources and fails to create a fresh Gpu CR.
const gpuOperatorNamespace = "gpu-operator"

// namespaceCleanupTimeout must cover the full Helm uninstall wait (up to 20 min,
// controller-side) plus namespace foreground termination on top. GPU driver pod
// shutdown (kernel module unload) dominates and takes 15+ min on real hardware.
const namespaceCleanupTimeout = 25 * time.Minute

// Apply creates the Gpu CR on the cluster and registers cleanup via t.Cleanup.
// It does NOT wait for readiness - callers that need readiness should call the
// asserts package directly. Returns the created CR or an error.
//
// Per istio's helper guidelines, this function returns errors instead of
// asserting; callers are expected to require.NoError on the result.
func (b *Builder) Apply(t *testing.T) (*gpuv1beta1.Gpu, error) {
	t.Helper()

	r, err := client.ResourcesClient(t)
	if err != nil {
		return nil, err
	}

	cr := b.Build()
	logGpuCR(t, cr)

	if err := r.Create(t.Context(), cr); err != nil {
		t.Logf("Failed to create Gpu CR %q: %v", cr.Name, err)
		return nil, err
	}

	registerCleanup(t, cr)
	t.Logf("Gpu CR %q created", cr.Name)
	return cr, nil
}

// Delete removes the Gpu CR. Idempotent - returns nil if the CR is already
// gone. Does not wait for the finalizer to clear.
func Delete(t *testing.T, cr *gpuv1beta1.Gpu) error {
	t.Helper()

	r, err := client.ResourcesClient(t)
	if err != nil {
		return err
	}
	if err := r.Delete(setup.GetCleanupContext(), cr); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		t.Logf("Failed to delete Gpu CR %q: %v", cr.Name, err)
		return err
	}
	return nil
}

// WaitForDeletion blocks until the Gpu CR is no longer present on the
// apiserver. Uses the configured TEST_TIMEOUT.
func WaitForDeletion(t *testing.T, cr *gpuv1beta1.Gpu) error {
	t.Helper()

	r, err := client.ResourcesClient(t)
	if err != nil {
		return err
	}
	return wait.For(
		conditions.New(r).ResourceDeleted(cr),
		wait.WithTimeout(config.Get().TestTimeout),
	)
}

func registerCleanup(t *testing.T, cr *gpuv1beta1.Gpu) {
	t.Helper()
	setup.DeclareCleanup(t, func() {
		t.Logf("Cleaning up Gpu CR %q", cr.Name)
		if err := Delete(t, cr); err != nil {
			t.Logf("Failed to delete Gpu CR during cleanup: %v", err)
			return
		}
		if err := WaitForDeletion(t, cr); err != nil {
			t.Logf("Gpu CR %q did not finish deleting: %v", cr.Name, err)
			return
		}
		// Wait for the gpu-operator namespace to be fully gone before returning.
		// The controller removes the Gpu CR finalizer before the namespace finishes
		// terminating, so WaitForDeletion above returns while driver pods are still
		// shutting down. Without this wait, a subsequent test would find leftover
		// resources and fail to create a fresh Gpu CR.
		t.Logf("Waiting for namespace %q to be fully terminated (driver pods must stop)", gpuOperatorNamespace)
		r, err := client.ResourcesClient(t)
		if err != nil {
			t.Logf("Could not create resources client for namespace wait: %v", err)
			return
		}
		waitErr := wait.For(func(ctx context.Context) (bool, error) {
			ns := &corev1.Namespace{}
			getErr := r.GetControllerRuntimeClient().Get(ctx, types.NamespacedName{Name: gpuOperatorNamespace}, ns)
			if apierrors.IsNotFound(getErr) {
				return true, nil
			}
			if getErr != nil {
				t.Logf("fetching namespace %q: %v", gpuOperatorNamespace, getErr)
				return false, nil
			}
			t.Logf("namespace %q still terminating (phase=%s)", gpuOperatorNamespace, ns.Status.Phase)
			return false, nil
		}, wait.WithTimeout(namespaceCleanupTimeout), wait.WithInterval(10*time.Second))
		if waitErr != nil {
			t.Logf("namespace %q did not terminate within %s: %v", gpuOperatorNamespace, namespaceCleanupTimeout, waitErr)
		}
	})
}
