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

// Package gpu provides reusable status assertions for the Gpu CR.
// Helpers in helpers/gpu return errors; assertions live here and use require.
package gpu

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"

	gpuv1beta1 "github.com/kyma-project/gpu/api/v1beta1"
	"github.com/kyma-project/gpu/tests/e2e/pkg/config"
	"github.com/kyma-project/gpu/tests/e2e/pkg/helpers/client"
)

// ConditionTypes are the Gpu CR conditions the operator manages. Tests
// reference these instead of bare strings so a rename in the controller
// surfaces as a compile error here.
const (
	ConditionReady              = "Ready"
	ConditionPreflight          = "Preflight"
	ConditionHelmInstalled      = "HelmInstalled"
	ConditionDriverReady        = "DriverReady"
	ConditionValidatorPassed    = "ValidatorPassed"
	ConditionWorkloadProtection = "WorkloadProtection"
)

// AssertOptions configures the wait/poll behavior for status assertions.
type AssertOptions struct {
	Timeout  time.Duration
	Interval time.Duration
}

// Option mutates AssertOptions.
type Option func(*AssertOptions)

func WithTimeout(d time.Duration) Option  { return func(o *AssertOptions) { o.Timeout = d } }
func WithInterval(d time.Duration) Option { return func(o *AssertOptions) { o.Interval = d } }

func resolveOptions(opts ...Option) *AssertOptions {
	o := &AssertOptions{
		Timeout:  config.Get().TestTimeout,
		Interval: 5 * time.Second,
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// AssertCondition blocks until the named condition reaches the expected
// status on the Gpu CR, failing the test if the timeout fires first.
func AssertCondition(t *testing.T, name, condType string, status metav1.ConditionStatus, opts ...Option) {
	t.Helper()
	o := resolveOptions(opts...)

	r, err := client.ResourcesClient(t)
	require.NoError(t, err, "creating resources client")

	err = wait.For(func(ctx context.Context) (bool, error) {
		gpu, getErr := fetchGpu(ctx, r, name)
		if getErr != nil {
			t.Logf("fetching Gpu %q: %v", name, getErr)
			return false, nil
		}
		cond := apimeta.FindStatusCondition(gpu.Status.Conditions, condType)
		if cond == nil {
			t.Logf("condition %q not yet present on Gpu %q", condType, name)
			return false, nil
		}
		if cond.Status != status {
			t.Logf("condition %q on Gpu %q: want %s, got %s (reason=%s message=%q)",
				condType, name, status, cond.Status, cond.Reason, cond.Message)
			return false, nil
		}
		return true, nil
	}, wait.WithTimeout(o.Timeout), wait.WithInterval(o.Interval))

	require.NoError(t, err, "waiting for condition %s=%s on Gpu %q", condType, status, name)
}

// AssertReady is the convenience for the happy path: wait until
// Ready=True on the named CR.
func AssertReady(t *testing.T, name string, opts ...Option) {
	t.Helper()
	AssertCondition(t, name, ConditionReady, metav1.ConditionTrue, opts...)
}

// AssertConditionReason waits until the named condition has both the
// expected status AND the expected reason. Use this to verify specific
// failure modes (e.g. ForbiddenCRName, ActiveGPUWorkloads).
func AssertConditionReason(
	t *testing.T,
	name, condType string,
	status metav1.ConditionStatus,
	reason string,
	opts ...Option,
) {
	t.Helper()
	o := resolveOptions(opts...)

	r, err := client.ResourcesClient(t)
	require.NoError(t, err, "creating resources client")

	err = wait.For(func(ctx context.Context) (bool, error) {
		gpu, getErr := fetchGpu(ctx, r, name)
		if getErr != nil {
			t.Logf("fetching Gpu %q: %v", name, getErr)
			return false, nil
		}
		cond := apimeta.FindStatusCondition(gpu.Status.Conditions, condType)
		if cond == nil {
			return false, nil
		}
		if cond.Status != status || cond.Reason != reason {
			t.Logf("condition %q on %q: want %s/%s, got %s/%s",
				condType, name, status, reason, cond.Status, cond.Reason)
			return false, nil
		}
		return true, nil
	}, wait.WithTimeout(o.Timeout), wait.WithInterval(o.Interval))

	require.NoError(t, err, "waiting for condition %s=%s reason=%s on Gpu %q", condType, status, reason, name)
}

// AssertOperatorVersion blocks until status.operatorVersion is non-empty,
// then returns it for inspection.
func AssertOperatorVersion(t *testing.T, name string, opts ...Option) string {
	t.Helper()
	o := resolveOptions(opts...)

	r, err := client.ResourcesClient(t)
	require.NoError(t, err)

	var version string
	err = wait.For(func(ctx context.Context) (bool, error) {
		gpu, getErr := fetchGpu(ctx, r, name)
		if getErr != nil {
			return false, nil
		}
		version = gpu.Status.OperatorVersion
		return version != "", nil
	}, wait.WithTimeout(o.Timeout), wait.WithInterval(o.Interval))

	require.NoError(t, err, "waiting for status.operatorVersion on Gpu %q", name)
	return version
}

// AssertDriverNodesReady blocks until status.driver.nodesReady is >= the
// expected count.
func AssertDriverNodesReady(t *testing.T, name string, minNodes int32, opts ...Option) {
	t.Helper()
	o := resolveOptions(opts...)

	r, err := client.ResourcesClient(t)
	require.NoError(t, err)

	err = wait.For(func(ctx context.Context) (bool, error) {
		gpu, getErr := fetchGpu(ctx, r, name)
		if getErr != nil {
			return false, nil
		}
		if gpu.Status.Driver == nil {
			return false, nil
		}
		if gpu.Status.Driver.NodesReady < minNodes {
			t.Logf("Gpu %q: driver.nodesReady=%d, want >= %d", name, gpu.Status.Driver.NodesReady, minNodes)
			return false, nil
		}
		return true, nil
	}, wait.WithTimeout(o.Timeout), wait.WithInterval(o.Interval))

	require.NoError(t, err, "waiting for driver.nodesReady >= %d on Gpu %q", minNodes, name)
}

func fetchGpu(ctx context.Context, r *resources.Resources, name string) (*gpuv1beta1.Gpu, error) {
	gpu := &gpuv1beta1.Gpu{}
	if err := r.GetControllerRuntimeClient().Get(ctx, types.NamespacedName{Name: name}, gpu); err != nil {
		return nil, err
	}
	return gpu, nil
}
