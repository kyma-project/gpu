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

package chart

import (
	"sort"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
)

func TestGPUOperatorChart(t *testing.T) {
	data, err := GPUOperatorChart()
	if err != nil {
		t.Fatalf("GPUOperatorChart() returned error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("GPUOperatorChart() returned empty bytes")
	}
	if data[0] != 0x1f || data[1] != 0x8b {
		t.Fatalf("expected gzip magic bytes (1f 8b), got: %02x %02x", data[0], data[1])
	}
}

func TestGPUOperatorChartVersion(t *testing.T) {
	version, err := GPUOperatorChartVersion()
	if err != nil {
		t.Fatalf("GPUOperatorChartVersion() returned error: %v", err)
	}
	if version == "" {
		t.Fatal("GPUOperatorChartVersion() returned empty string")
	}
	t.Logf("Embedded chart version: %s", version)
}

func TestGardenLinuxValues(t *testing.T) {
	data, err := GardenLinuxValues()
	if err != nil {
		t.Fatalf("GardenLinuxValues() returned error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("GardenLinuxValues() returned empty bytes")
	}
	if !strings.Contains(string(data), "usePrecompiled: true") {
		t.Fatal("GardenLinuxValues() does not contain expected 'usePrecompiled: true'")
	}
}

func TestVersionFromFilename(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"gpu-operator-v26.3.1.tgz", "v26.3.1"},
		{"gpu-operator-v25.6.0.tgz", "v25.6.0"},
		{"gpu-operator-v24.9.2.tgz", "v24.9.2"},
	}
	for _, tt := range tests {
		got := versionFromFilename(tt.filename)
		if got != tt.expected {
			t.Errorf("versionFromFilename(%q) = %q, want %q", tt.filename, got, tt.expected)
		}
	}
}

func TestLatestVersionSorting(t *testing.T) {
	filenames := []string{
		"gpu-operator-v24.9.2.tgz",
		"gpu-operator-v26.3.1.tgz",
		"gpu-operator-v25.6.0.tgz",
		"gpu-operator-v26.3.0.tgz",
	}

	type chartEntry struct {
		name    string
		version *semver.Version
	}

	var charts []chartEntry
	for _, f := range filenames {
		v := versionFromFilename(f)
		sv, err := semver.NewVersion(v)
		if err != nil {
			t.Fatalf("failed to parse version from %q: %v", f, err)
		}
		charts = append(charts, chartEntry{name: f, version: sv})
	}

	sort.Slice(charts, func(i, j int) bool {
		return charts[i].version.GreaterThan(charts[j].version)
	})

	if charts[0].name != "gpu-operator-v26.3.1.tgz" {
		t.Errorf("expected latest to be gpu-operator-v26.3.1.tgz, got %s", charts[0].name)
	}
	if charts[len(charts)-1].name != "gpu-operator-v24.9.2.tgz" {
		t.Errorf("expected oldest to be gpu-operator-v24.9.2.tgz, got %s", charts[len(charts)-1].name)
	}
}
