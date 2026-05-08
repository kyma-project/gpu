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

	"github.com/kyma-project/gpu/internal/chart"
)

func TestLoadChart(t *testing.T) {
	data, err := chart.GPUOperatorChart()
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
