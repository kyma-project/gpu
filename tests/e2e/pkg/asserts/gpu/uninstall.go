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

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/e2e-framework/klient/wait"

	"github.com/kyma-project/gpu/tests/e2e/pkg/helpers/client"
)

// uninstallTimeout caps the wait for the operator namespace and ClusterPolicy
// to disappear after the Gpu CR is deleted. The controller's Helm uninstall
// blocks while GPU driver pods terminate (kernel module unload), which on real
// hardware takes 15+ minutes. Add margin for namespace foreground deletion on
// top of that.
const uninstallTimeout = 25 * time.Minute

// AssertOperatorNamespaceGone blocks until the gpu-operator namespace no
// longer exists on the apiserver. Use after the Gpu CR has been deleted to
// prove the controller cleaned up the helm release.
func AssertOperatorNamespaceGone(t *testing.T, namespace string, opts ...Option) {
	t.Helper()
	o := resolveOptions(opts...)
	if o.Timeout < uninstallTimeout {
		o.Timeout = uninstallTimeout
	}

	r, err := client.ResourcesClient(t)
	require.NoError(t, err, "creating resources client")

	err = wait.For(func(ctx context.Context) (bool, error) {
		ns := &corev1.Namespace{}
		getErr := r.GetControllerRuntimeClient().Get(ctx, types.NamespacedName{Name: namespace}, ns)
		if apierrors.IsNotFound(getErr) {
			return true, nil
		}
		if getErr != nil {
			t.Logf("fetching namespace %q: %v", namespace, getErr)
			return false, nil
		}
		t.Logf("namespace %q still present (phase=%s)", namespace, ns.Status.Phase)
		return false, nil
	}, wait.WithTimeout(o.Timeout), wait.WithInterval(o.Interval))

	require.NoError(t, err, "waiting for namespace %q to be gone", namespace)
}

// AssertClusterPolicyGone blocks until no NVIDIA ClusterPolicy objects remain.
// Helm uninstall removes them as part of the release teardown; this is a
// belt-and-braces check that the chart was fully unwound.
func AssertClusterPolicyGone(t *testing.T, opts ...Option) {
	t.Helper()
	o := resolveOptions(opts...)
	if o.Timeout < uninstallTimeout {
		o.Timeout = uninstallTimeout
	}

	r, err := client.ResourcesClient(t)
	require.NoError(t, err, "creating resources client")

	gvk := schema.GroupVersionKind{Group: "nvidia.com", Version: "v1", Kind: "ClusterPolicyList"}

	err = wait.For(func(ctx context.Context) (bool, error) {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(gvk)
		listErr := r.GetControllerRuntimeClient().List(ctx, list)
		if listErr != nil {
			// CRD itself may be gone after a full uninstall - IsNotFound covers the
			// object-level 404; NoKindMatchError / NoResourceMatchError cover the case
			// where the CRD is removed and the REST mapper no longer knows the type.
			if apierrors.IsNotFound(listErr) ||
				apimeta.IsNoMatchError(listErr) {
				return true, nil
			}
			t.Logf("listing ClusterPolicy: %v", listErr)
			return false, nil
		}
		if len(list.Items) == 0 {
			return true, nil
		}
		t.Logf("ClusterPolicy list still has %d item(s)", len(list.Items))
		return false, nil
	}, wait.WithTimeout(o.Timeout), wait.WithInterval(o.Interval))

	require.NoError(t, err, "waiting for ClusterPolicy resources to be gone")
}
