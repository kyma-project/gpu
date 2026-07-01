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

// Package setup wires test-level lifecycle helpers: cleanup registration and
// best-effort cluster-state dumping on failure.
package setup

import (
	"context"
	"testing"

	"github.com/kyma-project/gpu/tests/e2e/pkg/config"
)

// ShouldSkipCleanup reports whether t.Cleanup should leave resources behind
// for post-mortem inspection: the test failed and SKIP_CLEANUP is set.
func ShouldSkipCleanup(t *testing.T) bool {
	return t.Failed() && config.Get().SkipCleanup
}

// DeclareCleanup wires f into t.Cleanup, dumping cluster state first and
// honoring SKIP_CLEANUP. Mirrors the pattern from kyma-project/istio.
func DeclareCleanup(t *testing.T, f func()) {
	t.Helper()
	t.Cleanup(func() {
		t.Helper()
		DumpClusterResources(t)
		if ShouldSkipCleanup(t) {
			t.Logf("Tests failed, skipping cleanup")
			return
		}
		t.Logf("Cleaning up")
		f()
	})
}

// GetCleanupContext returns the context cleanup functions should use. It's
// detached from t.Context() because the test context may already be canceled
// when cleanup runs.
func GetCleanupContext() context.Context {
	return context.Background()
}
