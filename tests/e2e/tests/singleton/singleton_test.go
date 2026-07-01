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

// Package singleton verifies the CEL admission rule on the Gpu CRD: the only
// accepted CR name is "gpu". Any other name must be rejected at admission
// time, before the controller ever sees the object.
package singleton

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gpuhelper "github.com/kyma-project/gpu/tests/e2e/pkg/helpers/gpu"
)

// TestSingletonRejection applies a Gpu CR with a non-singleton name and
// expects the apiserver to reject it via the CEL validation rule. The
// controller's defense-in-depth reconcile check is verified by unit + envtest
// coverage; this test specifically pins the admission-layer guard.
func TestSingletonRejection(t *testing.T) {
	_, err := gpuhelper.NewBuilder().WithName("not-gpu").Apply(t)
	require.Error(t, err, "apiserver must reject a non-singleton Gpu CR name")

	// The CEL rule on the CRD references the singleton constraint by name.
	// Match loosely: "gpu" appears somewhere in the violation message
	// (either as the required name or the field name).
	if !strings.Contains(strings.ToLower(err.Error()), "gpu") {
		t.Fatalf("expected CEL rejection message to mention the singleton constraint, got: %v", err)
	}
	t.Logf("apiserver rejected non-singleton Gpu CR as expected: %v", err)
}
