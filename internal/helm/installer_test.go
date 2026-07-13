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

package helm

import (
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"

	gpuchart "github.com/kyma-project/gpu/internal/chart"
)

func TestLoadChart(t *testing.T) {
	data, err := gpuchart.GPUOperatorChart()
	if err != nil {
		t.Fatalf("GPUOperatorChart() error: %v", err)
	}

	chrt, err := loadChart(data)
	if err != nil {
		t.Fatalf("loadChart() error: %v", err)
	}

	if chrt.Metadata == nil {
		t.Fatal("loaded chart has no metadata")
	}
	if chrt.Metadata.Name == "" {
		t.Error("chart metadata has empty name")
	}
	if chrt.Metadata.Version == "" {
		t.Error("chart metadata has empty version")
	}
	t.Logf("loaded chart: %s@%s", chrt.Metadata.Name, chrt.Metadata.Version)
}

func TestLoadChartRejectsGarbage(t *testing.T) {
	_, err := loadChart([]byte("not a chart"))
	if err == nil {
		t.Fatal("expected error for invalid chart data, got nil")
	}
}

func TestValuesEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b map[string]any
		want bool
	}{
		{
			name: "both empty",
			a:    map[string]any{},
			b:    map[string]any{},
			want: true,
		},
		{
			name: "empty vs nil",
			a:    nil,
			b:    map[string]any{},
			want: true,
		},
		{
			name: "identical",
			a:    map[string]any{"driver": map[string]any{"version": "590", "enabled": true}},
			b:    map[string]any{"driver": map[string]any{"version": "590", "enabled": true}},
			want: true,
		},
		{
			name: "key order differs",
			a:    map[string]any{"a": "1", "b": "2"},
			b:    map[string]any{"b": "2", "a": "1"},
			want: true,
		},
		{
			// The float64-from-YAML vs int-from-Go case the fingerprint must tolerate:
			// JSON marshals both as the same number, so they compare equal.
			name: "float64 vs int same value",
			a:    map[string]any{"replicas": float64(4)},
			b:    map[string]any{"replicas": 4},
			want: true,
		},
		{
			name: "different driver version",
			a:    map[string]any{"driver": map[string]any{"version": "590"}},
			b:    map[string]any{"driver": map[string]any{"version": "595"}},
			want: false,
		},
		{
			name: "extra key",
			a:    map[string]any{"driver": map[string]any{"version": "590"}},
			b:    map[string]any{"driver": map[string]any{"version": "590"}, "extra": true},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := valuesEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("valuesEqual() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReleaseUpToDate(t *testing.T) {
	values := map[string]any{"driver": map[string]any{"version": "590"}}
	deployedRel := func(version string, status release.Status, config map[string]any) *release.Release {
		return &release.Release{
			Info:   &release.Info{Status: status},
			Chart:  &chart.Chart{Metadata: &chart.Metadata{Version: version}},
			Config: config,
		}
	}
	desired := &chart.Chart{Metadata: &chart.Metadata{Version: "v26.3.3"}}

	tests := []struct {
		name string
		rel  *release.Release
		want bool
	}{
		{
			name: "same version, same values, deployed",
			rel:  deployedRel("v26.3.3", release.StatusDeployed, map[string]any{"driver": map[string]any{"version": "590"}}),
			want: true,
		},
		{
			name: "failed release always re-upgrades",
			rel:  deployedRel("v26.3.3", release.StatusFailed, map[string]any{"driver": map[string]any{"version": "590"}}),
			want: false,
		},
		{
			name: "chart version changed",
			rel:  deployedRel("v26.3.2", release.StatusDeployed, map[string]any{"driver": map[string]any{"version": "590"}}),
			want: false,
		},
		{
			name: "values changed",
			rel:  deployedRel("v26.3.3", release.StatusDeployed, map[string]any{"driver": map[string]any{"version": "595"}}),
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := releaseUpToDate(tc.rel, desired, values); got != tc.want {
				t.Errorf("releaseUpToDate() = %v, want %v", got, tc.want)
			}
		})
	}
}
