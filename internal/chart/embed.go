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

package chart

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

//go:embed gpu-operator/*.tgz
var chartFS embed.FS

//go:embed values/*.yaml
var valuesFS embed.FS

// GPUOperatorChart returns the raw bytes of the latest embedded NVIDIA GPU Operator
// Helm chart archive (.tgz). When multiple versions are present, the highest
// semver version is selected.
func GPUOperatorChart() ([]byte, error) {
	name, err := latestChartFilename()
	if err != nil {
		return nil, err
	}
	data, err := chartFS.ReadFile("gpu-operator/" + name)
	if err != nil {
		return nil, fmt.Errorf("reading embedded chart %s: %w", name, err)
	}
	return data, nil
}

// GardenLinuxValues returns the embedded Helm values override for Garden Linux clusters.
func GardenLinuxValues() ([]byte, error) {
	data, err := valuesFS.ReadFile("values/gardenlinux.yaml")
	if err != nil {
		return nil, fmt.Errorf("reading embedded gardenlinux values: %w", err)
	}
	return data, nil
}

// GPUOperatorChartVersion returns the version string of the latest embedded chart.
func GPUOperatorChartVersion() (string, error) {
	name, err := latestChartFilename()
	if err != nil {
		return "", err
	}
	return versionFromFilename(name), nil
}

func latestChartFilename() (string, error) {
	entries, err := fs.ReadDir(chartFS, "gpu-operator")
	if err != nil {
		return "", fmt.Errorf("reading embedded gpu-operator directory: %w", err)
	}

	type chartEntry struct {
		name    string
		version *semver.Version
	}

	var charts []chartEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tgz") {
			continue
		}
		v := versionFromFilename(e.Name())
		sv, err := semver.NewVersion(v)
		if err != nil {
			continue
		}
		charts = append(charts, chartEntry{name: e.Name(), version: sv})
	}

	if len(charts) == 0 {
		return "", fmt.Errorf("no .tgz chart archive found in embedded gpu-operator directory")
	}

	sort.Slice(charts, func(i, j int) bool {
		return charts[i].version.GreaterThan(charts[j].version)
	})

	return charts[0].name, nil
}

func versionFromFilename(name string) string {
	name = strings.TrimPrefix(name, "gpu-operator-")
	name = strings.TrimSuffix(name, ".tgz")
	return name
}
